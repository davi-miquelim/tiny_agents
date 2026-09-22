// Package agent runs OpenAI-compatible chat turns with tools, typed handoffs,
// and portable history. Default model host is OpenRouter.
package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/davi-miquelim/tiny_agents/model"
	"github.com/davi-miquelim/tiny_agents/tool"
)

const (
	defaultMaxToolRounds = 8
	defaultToolTimeout   = 30 * time.Second
	defaultContextWindow = 128_000
)

// errStreamForwarded means the stream consumer already received the error chunk.
var errStreamForwarded = errors.New("stream error forwarded")

// Agent holds configuration for a completion turn.
//
// Prompt contract when ResultTool is set:
//   - Everything the user should read goes in assistant message text (content).
//   - Machine handoff fields go only in the result tool arguments.
//   - The result tool ends the turn (it is not executed as a normal tool loop step).
type Agent struct {
	Role         string
	Goals        []string
	Instructions string
	// Provider is a typed catalog name (model.OpenRouter, model.OpenAI, …). Required.
	// Unrelated to ModelParams.Provider (OpenRouter routing JSON).
	Provider    model.Provider
	ModelParams model.Parameters
	SessionID   string // OpenRouter sticky routing key for multi-turn workflows
	Stream      bool
	// Tools are executable tools. Schemas are derived for the API.
	Tools []tool.CallableTool
	// ResultTool is the handoff schema only (never Call'd). Zero name = plain Complete.
	ResultTool    tool.Tool
	MaxToolRounds int           // 0 = default 8
	ToolTimeout   time.Duration // 0 = default 30s
	// Compaction controls automatic history summarization. Zero value is on,
	// with a 128000 token window. Set CompactAt(n) or NoCompaction().
	Compaction Compaction
}

// Compaction is the Complete / CompleteTurn summarization policy.
// Zero value is on (window 128000). Construct with CompactAt or NoCompaction.
type Compaction struct {
	disable bool
	window  int // 0 = 128000
}

// NoCompaction skips automatic summarization.
func NoCompaction() Compaction {
	return Compaction{disable: true}
}

// CompactAt summarizes when Tokens reaches window (0 = 128000).
// A negative window disables compaction.
func CompactAt(window int) Compaction {
	if window < 0 {
		return NoCompaction()
	}
	return Compaction{window: window}
}

// MessagesBuffer is an agent-agnostic conversation transcript.
// Pass the same buffer into any agent's Complete / CompleteTurn loop.
//
// It stores portable turns only (user + assistant text). Per-agent system
// prompts and tool-call traffic stay in a request-local buffer built inside
// each loop, so one agent’s tools/role never leak into the next. The following
// turn does not see prior tool_calls or tool results—only assistant text.
//
// Tokens is the last prompt_tokens value reported by the API for a turn that
// used this buffer. It is never estimated locally; 0 means no usage has been
// observed yet (e.g. a freshly summarized buffer).
type MessagesBuffer struct {
	Messages []model.Message
	Tokens   int
}

// CompleteResult is the outcome of Complete.
// When Stream is non-nil, drain it (or call DrainComplete) before the next turn
// so history is fully written.
type CompleteResult struct {
	Response *model.ChatResponse
	Stream   <-chan model.ChatStreamChunk
}

// TurnResult is the outcome of CompleteTurn.
//
// When Stream is non-nil, drain it first; Content and Data are populated before
// the channel is closed. Then call Wait to observe any stream/turn error.
type TurnResult[T any] struct {
	Content string
	Data    T
	Stream  <-chan model.ChatStreamChunk

	done chan struct{}
	err  error
}

// Wait blocks until a streaming turn finishes. For non-streaming turns it
// returns nil immediately (the error was already returned from CompleteTurn).
func (r *TurnResult[T]) Wait() error {
	if r == nil {
		return fmt.Errorf("agent.TurnResult.Wait: nil result")
	}
	if r.done == nil {
		return nil
	}
	<-r.done
	return r.err
}

// DrainTurn consumes Stream (if any) and returns the turn error via Wait.
func DrainTurn[T any](res *TurnResult[T]) error {
	if res == nil {
		return fmt.Errorf("agent.DrainTurn: nil result")
	}
	if res.Stream != nil {
		for range res.Stream {
		}
	}
	return res.Wait()
}

// DrainComplete consumes Stream (if any) and returns a stream error if one was sent.
func DrainComplete(res CompleteResult) error {
	if res.Stream == nil {
		return nil
	}
	for chunk := range res.Stream {
		if chunk.Error != nil {
			return fmt.Errorf("%s", cmp.Or(chunk.Error.Message, "stream error"))
		}
	}
	return nil
}

func buildSystemPrompt(role string, goals []string, instructions string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Role: %s\n\n## Goals:\n", role)
	for _, goal := range goals {
		fmt.Fprintf(&b, "- %s\n", goal)
	}
	fmt.Fprintf(&b, "\n## Instructions:\n%s\n", instructions)
	return b.String()
}

func maxToolRounds(a Agent) int {
	return cmp.Or(a.MaxToolRounds, defaultMaxToolRounds)
}

func resultToolName(a Agent) string {
	return a.ResultTool.Function.Name
}

func apiTools(a Agent) []tool.Tool {
	out := make([]tool.Tool, 0, len(a.Tools)+1)
	for _, ct := range a.Tools {
		out = append(out, ct.Tool)
	}
	if resultToolName(a) != "" {
		out = append(out, a.ResultTool)
	}
	return out
}

func lookupTool(a Agent, name string) (tool.CallableTool, bool) {
	for _, ct := range a.Tools {
		if ct.Function.Name == name {
			return ct, true
		}
	}
	return tool.CallableTool{}, false
}

// assembleMessages returns a new request-local buffer: this agent's system
// prompt plus a copy of the portable transcript. The input buffer is never
// modified, so it can be reused with a different agent on the next loop.
func assembleMessages(a Agent, buff *MessagesBuffer) *MessagesBuffer {
	n := 1
	if buff != nil {
		n += len(buff.Messages)
	}
	out := &MessagesBuffer{Messages: make([]model.Message, 0, n)}
	out.Messages = append(out.Messages, model.Message{
		Role:    model.System,
		Content: buildSystemPrompt(a.Role, a.Goals, a.Instructions),
	})
	out.Messages = appendPortable(out.Messages, buff)
	return out
}

// appendPortable copies user/assistant transcript messages, dropping system/tool
// roles and stripping tool_calls so history stays agent-agnostic.
func appendPortable(dst []model.Message, buff *MessagesBuffer) []model.Message {
	if buff == nil {
		return dst
	}
	for _, m := range buff.Messages {
		switch m.Role {
		case model.User, model.Assistant:
			dst = append(dst, model.Message{Role: m.Role, Content: m.Content, Name: m.Name})
		}
	}
	return dst
}

func appendAssistantContent(buff *MessagesBuffer, content string) {
	buff.Messages = append(buff.Messages, model.Message{
		Role:    model.Assistant,
		Content: content,
	})
}

func applyUsage(buff *MessagesBuffer, usage *model.Usage) {
	if usage == nil {
		return
	}
	buff.Tokens = usage.PromptTokens
}

// adoptBuffer copies src into dst when they are distinct buffers.
func adoptBuffer(dst, src *MessagesBuffer) {
	if dst != src {
		*dst = *src
	}
}

// CloneBuffer returns a copy of the portable transcript and Tokens.
// The source buffer is never modified.
func CloneBuffer(buff *MessagesBuffer) *MessagesBuffer {
	if buff == nil {
		return &MessagesBuffer{}
	}
	out := &MessagesBuffer{
		Messages: make([]model.Message, 0, len(buff.Messages)),
		Tokens:   buff.Tokens,
	}
	out.Messages = appendPortable(out.Messages, buff)
	return out
}

// NeedsCompaction reports whether buff.Tokens has reached the model context window.
func NeedsCompaction(buff *MessagesBuffer, contextWindow int) bool {
	if buff == nil || contextWindow <= 0 {
		return false
	}
	return buff.Tokens >= contextWindow
}

func buildParams(a Agent, apiMessages *MessagesBuffer) model.Parameters {
	params := a.ModelParams
	params.Messages = apiMessages.Messages
	// Agent.Stream chooses Complete vs CompleteStream; never forward a stale
	// ModelParams.Stream into non-streaming model.Complete calls.
	params.Stream = false
	params.SessionID = cmp.Or(a.SessionID, params.SessionID)
	params.Tools = apiTools(a)
	if len(params.Tools) > 0 {
		if params.ToolChoice == nil {
			params.ToolChoice = "auto"
		}
	} else {
		params.ToolChoice = nil
	}
	return params
}

func contentString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case *string:
		if t == nil {
			return ""
		}
		return *t
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// AppendUserMessage appends a user message to the shared transcript.
func AppendUserMessage(buff *MessagesBuffer, msg string) error {
	if buff == nil {
		return fmt.Errorf("agent.AppendUserMessage: nil messages buffer")
	}
	buff.Messages = append(buff.Messages, model.Message{
		Role:    model.User,
		Content: msg,
	})
	return nil
}

// Complete runs a plain agent turn (no typed handoff). History is appended to msgs.
func Complete(ctx context.Context, a Agent, msgs *MessagesBuffer) (CompleteResult, error) {
	if msgs == nil {
		return CompleteResult{}, fmt.Errorf("agent.Complete: nil messages buffer")
	}
	working, err := compactIfNeeded(ctx, a, msgs)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("agent.Complete: %w", err)
	}
	apiMessages := assembleMessages(a, working)

	if len(a.Tools) == 0 && resultToolName(a) == "" {
		params := buildParams(a, apiMessages)
		if a.Stream {
			ch, err := model.CompleteStream(ctx, a.Provider, params)
			if err != nil {
				return CompleteResult{}, fmt.Errorf("agent.Complete: %w", err)
			}
			return CompleteResult{Stream: teeStream(ctx, msgs, working, ch)}, nil
		}

		res, err := model.Complete(ctx, a.Provider, params)
		if err != nil {
			return CompleteResult{}, fmt.Errorf("agent.Complete: %w", err)
		}
		if len(res.Choices) == 0 {
			return CompleteResult{}, fmt.Errorf("agent.Complete: no choices in response")
		}
		applyUsage(working, res.Usage)
		appendAssistantContent(working, contentString(res.Choices[0].Message.Content))
		adoptBuffer(msgs, working)
		return CompleteResult{Response: &res}, nil
	}

	if a.Stream {
		out := make(chan model.ChatStreamChunk)
		go func() {
			defer close(out)
			if err := runCompleteStream(ctx, a, working, apiMessages, out); err != nil {
				if !errors.Is(err, errStreamForwarded) {
					sendStreamError(ctx, out, err)
				}
				return
			}
			adoptBuffer(msgs, working)
		}()
		return CompleteResult{Stream: out}, nil
	}

	maxRounds := maxToolRounds(a)
	for round := 0; round < maxRounds; round++ {
		params := buildParams(a, apiMessages)
		roundResult, err := fetchModelRound(ctx, false, a.Provider, params, nil)
		if err != nil {
			return CompleteResult{}, fmt.Errorf("agent.Complete: %w", err)
		}
		applyUsage(working, roundResult.Usage)

		if len(roundResult.ToolCalls) > 0 {
			executeRegularTools(ctx, a, apiMessages, model.Message{
				Role:      model.Assistant,
				Content:   roundResult.Content,
				ToolCalls: roundResult.ToolCalls,
			})
			continue
		}

		appendAssistantContent(working, roundResult.Content)
		adoptBuffer(msgs, working)
		return CompleteResult{Response: roundResult.Response}, nil
	}

	return CompleteResult{}, fmt.Errorf("agent.Complete: tool loop exceeded %d rounds", maxRounds)
}

// runCompleteStream streams each model round, including tool-call deltas, and
// executes regular tools between rounds until the model returns final content.
func runCompleteStream(
	ctx context.Context,
	a Agent,
	msgs *MessagesBuffer,
	apiMessages *MessagesBuffer,
	out chan<- model.ChatStreamChunk,
) error {
	maxRounds := maxToolRounds(a)
	for round := 0; round < maxRounds; round++ {
		params := buildParams(a, apiMessages)
		roundResult, err := fetchModelRound(ctx, true, a.Provider, params, out)
		if err != nil {
			return fmt.Errorf("agent.Complete: %w", err)
		}
		applyUsage(msgs, roundResult.Usage)
		if len(roundResult.ToolCalls) == 0 {
			appendAssistantContent(msgs, roundResult.Content)
			return nil
		}
		executeRegularTools(ctx, a, apiMessages, model.Message{
			Role:      model.Assistant,
			Content:   roundResult.Content,
			ToolCalls: roundResult.ToolCalls,
		})
	}
	return fmt.Errorf("agent.Complete: tool loop exceeded %d rounds", maxRounds)
}

func sendStreamError(ctx context.Context, out chan<- model.ChatStreamChunk, err error) {
	select {
	case <-ctx.Done():
	case out <- model.ChatStreamChunk{Error: &model.StreamError{Message: err.Error()}}:
	}
}

// executeRegularTools appends tool traffic only to the request-local API buffer.
// Unknown tools and Call/marshal errors become tool messages so the model can retry.
func executeRegularTools(ctx context.Context, a Agent, apiMessages *MessagesBuffer, assistantMsg model.Message) {
	apiMessages.Messages = append(apiMessages.Messages, model.Message{
		Role:      model.Assistant,
		Content:   assistantMsg.Content,
		ToolCalls: assistantMsg.ToolCalls,
	})

	timeout := cmp.Or(a.ToolTimeout, defaultToolTimeout)

	for _, tc := range assistantMsg.ToolCalls {
		content := toolErrorJSON(fmt.Sprintf("unknown tool %q", tc.Function.Name))
		if fn, ok := lookupTool(a, tc.Function.Name); ok {
			out, err := callTool(ctx, timeout, fn, tc.Function.Arguments)
			if err != nil {
				content = toolErrorJSON(err.Error())
			} else if payload, err := json.Marshal(out); err != nil {
				content = toolErrorJSON("marshal tool result: " + err.Error())
			} else {
				content = string(payload)
			}
		}
		apiMessages.Messages = append(apiMessages.Messages, model.Message{
			Role:       model.Tool,
			Content:    content,
			ToolCallID: tc.Id,
		})
	}
}

func toolErrorJSON(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

// ponytail: trusts tools to honor ctx; wrap in a goroutine if you need hard kill.
func callTool(ctx context.Context, timeout time.Duration, fn tool.CallableTool, argsJSON string) (any, error) {
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn.Call(toolCtx, argsJSON)
}

func teeStream(ctx context.Context, dst, working *MessagesBuffer, upstream <-chan model.ChatStreamChunk) <-chan model.ChatStreamChunk {
	out := make(chan model.ChatStreamChunk)
	go func() {
		defer close(out)
		var content strings.Builder
		for chunk := range upstream {
			if chunk.Error != nil {
				select {
				case <-ctx.Done():
					return
				case out <- chunk:
				}
				return
			}
			applyUsage(working, chunk.Usage)
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != nil {
					content.WriteString(*choice.Delta.Content)
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- chunk:
			}
		}
		if content.Len() > 0 {
			appendAssistantContent(working, content.String())
		}
		adoptBuffer(dst, working)
	}()
	return out
}

// CompleteTurn runs an agent turn that must end with a call to Agent.ResultTool.
// User-facing text is assistant content (optionally streamed). Handoff fields are
// unmarshaled from the result tool arguments into T.
func CompleteTurn[T any](ctx context.Context, a Agent, msgs *MessagesBuffer) (*TurnResult[T], error) {
	if err := validateCompleteTurn(a); err != nil {
		return nil, fmt.Errorf("agent.CompleteTurn: %w", err)
	}
	if msgs == nil {
		return nil, fmt.Errorf("agent.CompleteTurn: nil messages buffer")
	}
	working, err := compactIfNeeded(ctx, a, msgs)
	if err != nil {
		return nil, fmt.Errorf("agent.CompleteTurn: %w", err)
	}

	apiMessages := assembleMessages(a, working)
	result := &TurnResult[T]{}

	if a.Stream {
		out := make(chan model.ChatStreamChunk)
		done := make(chan struct{})
		result.Stream = out
		result.done = done
		go func() {
			defer close(done)
			defer close(out)
			content, data, err := runCompleteTurn[T](ctx, a, working, apiMessages, out)
			if err == nil {
				adoptBuffer(msgs, working)
			}
			result.Content = content
			result.Data = data
			result.err = err
		}()
		return result, nil
	}

	content, data, err := runCompleteTurn[T](ctx, a, working, apiMessages, nil)
	if err != nil {
		return nil, err
	}
	adoptBuffer(msgs, working)
	result.Content = content
	result.Data = data
	return result, nil
}

func validateCompleteTurn(a Agent) error {
	if resultToolName(a) == "" {
		return fmt.Errorf("ResultTool is required")
	}
	return nil
}

func runCompleteTurn[T any](
	ctx context.Context,
	a Agent,
	msgs *MessagesBuffer,
	apiMessages *MessagesBuffer,
	streamOut chan<- model.ChatStreamChunk,
) (string, T, error) {
	var zero T
	maxRounds := maxToolRounds(a)
	name := resultToolName(a)

	for round := 0; round < maxRounds; round++ {
		params := buildParams(a, apiMessages)

		roundResult, err := fetchModelRound(ctx, a.Stream, a.Provider, params, streamOut)
		if err != nil {
			return "", zero, fmt.Errorf("agent.CompleteTurn: %w", err)
		}
		applyUsage(msgs, roundResult.Usage)

		resultCall, regular, err := splitToolCalls(name, roundResult.ToolCalls)
		if err != nil {
			return "", zero, fmt.Errorf("agent.CompleteTurn: %w", err)
		}
		if resultCall != nil {
			data, err := decodeResultArgs[T](resultCall.Function.Arguments)
			if err != nil {
				return "", zero, fmt.Errorf("agent.CompleteTurn: %w", err)
			}
			appendAssistantContent(msgs, roundResult.Content)
			return roundResult.Content, data, nil
		}
		if len(regular) == 0 {
			return "", zero, fmt.Errorf("agent.CompleteTurn: handoff required: model returned no tool calls")
		}
		executeRegularTools(ctx, a, apiMessages, model.Message{
			Role:      model.Assistant,
			Content:   roundResult.Content,
			ToolCalls: regular,
		})
	}

	return "", zero, fmt.Errorf("agent.CompleteTurn: tool loop exceeded %d rounds without result tool", maxRounds)
}

type modelRound struct {
	Content   string
	ToolCalls []tool.ToolCall
	Usage     *model.Usage
	Response  *model.ChatResponse // non-stream only
}

func fetchModelRound(
	ctx context.Context,
	stream bool,
	provider model.Provider,
	params model.Parameters,
	streamOut chan<- model.ChatStreamChunk,
) (modelRound, error) {
	if stream {
		content, toolCalls, usage, err := streamModelRound(ctx, provider, params, streamOut)
		return modelRound{Content: content, ToolCalls: toolCalls, Usage: usage}, err
	}

	res, err := model.Complete(ctx, provider, params)
	if err != nil {
		return modelRound{}, err
	}
	if len(res.Choices) == 0 {
		return modelRound{}, fmt.Errorf("no choices in response")
	}
	msg := res.Choices[0].Message
	return modelRound{
		Content:   contentString(msg.Content),
		ToolCalls: msg.ToolCalls,
		Usage:     res.Usage,
		Response:  &res,
	}, nil
}

// splitToolCalls applies the v1 rule: result tool must appear alone.
// On success: result set, or regular set, or both empty (no calls).
func splitToolCalls(resultTool string, calls []tool.ToolCall) (*tool.ToolCall, []tool.ToolCall, error) {
	if len(calls) == 0 {
		return nil, nil, nil
	}

	var result *tool.ToolCall
	var regular []tool.ToolCall
	for i := range calls {
		tc := &calls[i]
		if tc.Function.Name == resultTool {
			if result != nil {
				return nil, nil, fmt.Errorf("multiple result tool calls are not allowed")
			}
			result = tc
			continue
		}
		regular = append(regular, *tc)
	}
	if result != nil && len(regular) > 0 {
		return nil, nil, fmt.Errorf("mixed result and regular tool calls are not allowed")
	}
	return result, regular, nil
}

func decodeResultArgs[T any](argsJSON string) (T, error) {
	var data T
	if err := json.Unmarshal([]byte(argsJSON), &data); err != nil {
		return data, fmt.Errorf("unmarshal result tool args: %w", err)
	}
	return data, nil
}

func streamModelRound(
	ctx context.Context,
	provider model.Provider,
	params model.Parameters,
	streamOut chan<- model.ChatStreamChunk,
) (string, []tool.ToolCall, *model.Usage, error) {
	ch, err := model.CompleteStream(ctx, provider, params)
	if err != nil {
		return "", nil, nil, err
	}

	var content strings.Builder
	var toolCalls []tool.ToolCall
	var argBufs []strings.Builder
	var usage *model.Usage

	for chunk := range ch {
		if chunk.Error != nil {
			if streamOut != nil {
				select {
				case <-ctx.Done():
					return "", nil, nil, ctx.Err()
				case streamOut <- chunk:
				}
			}
			return "", nil, nil, fmt.Errorf("%w: %s", errStreamForwarded, cmp.Or(chunk.Error.Message, "stream error"))
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				content.WriteString(*choice.Delta.Content)
			}
			if len(choice.Delta.ToolCalls) > 0 {
				accumulateToolCallDeltas(&toolCalls, &argBufs, choice.Delta.ToolCalls)
			}
		}
		if streamOut != nil {
			select {
			case <-ctx.Done():
				return "", nil, nil, ctx.Err()
			case streamOut <- chunk:
			}
		}
	}

	for i := range toolCalls {
		toolCalls[i].Function.Arguments = argBufs[i].String()
	}
	return content.String(), toolCalls, usage, nil
}

func accumulateToolCallDeltas(dst *[]tool.ToolCall, argBufs *[]strings.Builder, deltas []tool.ToolCall) {
	for _, d := range deltas {
		idx := 0
		if d.Index != nil {
			idx = *d.Index
		}
		for len(*dst) <= idx {
			*dst = append(*dst, tool.ToolCall{})
			*argBufs = append(*argBufs, strings.Builder{})
		}
		tc := &(*dst)[idx]
		tc.Id = cmp.Or(d.Id, tc.Id)
		tc.Type = cmp.Or(d.Type, tc.Type)
		tc.Function.Name = cmp.Or(d.Function.Name, tc.Function.Name)
		if d.Function.Arguments != "" {
			(*argBufs)[idx].WriteString(d.Function.Arguments)
		}
	}
}
