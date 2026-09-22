package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davi-miquelim/tiny_agents/model"
	"github.com/davi-miquelim/tiny_agents/tool"
)

func TestAssembleMessagesLeavesTranscriptUntouched(t *testing.T) {
	t.Parallel()
	buff := &MessagesBuffer{Messages: []model.Message{
		{Role: model.User, Content: "hi"},
		{Role: model.Assistant, Content: "hello", ToolCalls: []tool.ToolCall{{Id: "x"}}},
		{Role: model.Tool, Content: `{"ok":true}`, ToolCallID: "x"},
		{Role: model.System, Content: "stale system"},
	}}
	before := len(buff.Messages)

	a := Agent{Role: "Clerk", Goals: []string{"help"}, Instructions: "Be brief."}
	api := assembleMessages(a, buff)

	if len(buff.Messages) != before {
		t.Fatalf("transcript mutated: len=%d want %d", len(buff.Messages), before)
	}
	if len(api.Messages) != 3 { // system + user + assistant (tool/system dropped)
		t.Fatalf("api messages len = %d, want 3: %+v", len(api.Messages), api.Messages)
	}
	if api.Messages[0].Role != model.System {
		t.Fatalf("first role = %q, want system", api.Messages[0].Role)
	}
	if api.Messages[1].Role != model.User || api.Messages[1].Content != "hi" {
		t.Fatalf("user = %+v", api.Messages[1])
	}
	if api.Messages[2].Role != model.Assistant || api.Messages[2].Content != "hello" {
		t.Fatalf("assistant = %+v", api.Messages[2])
	}
	if len(api.Messages[2].ToolCalls) != 0 {
		t.Fatalf("tool_calls leaked into portable history: %+v", api.Messages[2].ToolCalls)
	}
}

type pingArgs struct {
	N int `json:"n"`
}

func pingTool(_ context.Context, a pingArgs) (any, error) { return map[string]any{"n": a.N}, nil }

func TestExecuteRegularToolsDoesNotTouchTranscript(t *testing.T) {
	t.Parallel()
	ping, err := tool.CreateTool("test ping tool", pingTool)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	a := Agent{
		Tools: []tool.CallableTool{ping},
	}
	api := &MessagesBuffer{Messages: []model.Message{{Role: model.System, Content: "sys"}}}
	transcript := &MessagesBuffer{Messages: []model.Message{{Role: model.User, Content: "go"}}}

	executeRegularTools(context.Background(), a, api, model.Message{
		Role: model.Assistant,
		ToolCalls: []tool.ToolCall{{
			Id:       "1",
			Type:     "function",
			Function: tool.FunctionCall{Name: ping.Function.Name, Arguments: `{"n":1}`},
		}},
	})
	if len(transcript.Messages) != 1 {
		t.Fatalf("transcript len = %d, want 1 (unchanged)", len(transcript.Messages))
	}
	if len(api.Messages) != 3 { // system + assistant tool_calls + tool result
		t.Fatalf("api len = %d, want 3", len(api.Messages))
	}
}

func TestExecuteRegularToolsFeedsErrorsBack(t *testing.T) {
	t.Parallel()
	failing, err := tool.CreateTool("failing test tool", func(_ context.Context, _ pingArgs) (any, error) {
		return nil, fmt.Errorf("boom")
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	a := Agent{Tools: []tool.CallableTool{failing}}
	api := &MessagesBuffer{Messages: []model.Message{{Role: model.System, Content: "sys"}}}

	executeRegularTools(context.Background(), a, api, model.Message{
		Role: model.Assistant,
		ToolCalls: []tool.ToolCall{
			{Id: "1", Type: "function", Function: tool.FunctionCall{Name: failing.Function.Name, Arguments: `{"n":1}`}},
			{Id: "2", Type: "function", Function: tool.FunctionCall{Name: "nope", Arguments: `{}`}},
		},
	})
	if len(api.Messages) != 4 {
		t.Fatalf("api len = %d, want 4", len(api.Messages))
	}
	if got := fmt.Sprint(api.Messages[2].Content); !strings.Contains(got, "boom") {
		t.Fatalf("call error content = %s, want boom", got)
	}
	if got := fmt.Sprint(api.Messages[3].Content); !strings.Contains(got, "unknown tool") {
		t.Fatalf("unknown tool content = %s", got)
	}
}

func TestCallToolCancelsCooperativeTool(t *testing.T) {
	blocking, err := tool.CreateTool("blocking test tool", func(ctx context.Context, _ pingArgs) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	_, err = callTool(context.Background(), 10*time.Millisecond, blocking, `{"n":1}`)
	if err != context.DeadlineExceeded {
		t.Fatalf("callTool error = %v, want DeadlineExceeded", err)
	}
}

func TestCompleteStreamsToolRounds(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"ping_tool\",\"arguments\":\"{\\\"n\\\":1}\"}}]}}]}\n\ndata: [DONE]\n\n")
		case 2:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"final \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected model request %d", requests.Load())
		}
	}))
	t.Cleanup(srv.Close)

	ping, err := tool.CreateTool("test ping tool", pingTool)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	a := Agent{
		Provider:    model.OpenRouter,
		ModelParams: model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"},
		Stream:      true,
		Tools:       []tool.CallableTool{ping},
	}
	msgs := &MessagesBuffer{Messages: []model.Message{{Role: model.User, Content: "go"}}}

	res, err := Complete(context.Background(), a, msgs)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var content strings.Builder
	for chunk := range res.Stream {
		if chunk.Error != nil {
			t.Fatalf("stream error: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				content.WriteString(*choice.Delta.Content)
			}
		}
	}
	if got := content.String(); got != "final answer" {
		t.Fatalf("streamed content = %q, want final answer", got)
	}
	if requests.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", requests.Load())
	}
	if len(msgs.Messages) != 2 || msgs.Messages[1].Content != "final answer" {
		t.Fatalf("transcript = %+v, want final assistant content", msgs.Messages)
	}
}

func TestSplitToolCallsMixed(t *testing.T) {
	t.Parallel()
	_, _, err := splitToolCalls("submit_handoff", []tool.ToolCall{
		{Function: tool.FunctionCall{Name: "add"}},
		{Function: tool.FunctionCall{Name: "submit_handoff"}},
	})
	if err == nil || !strings.Contains(err.Error(), "mixed") {
		t.Fatalf("error = %v, want mixed error", err)
	}
}

func TestSplitToolCallsResultAlone(t *testing.T) {
	t.Parallel()
	result, regular, err := splitToolCalls("submit_handoff", []tool.ToolCall{
		{Id: "1", Function: tool.FunctionCall{Name: "submit_handoff", Arguments: `{"city":"Paris"}`}},
	})
	if err != nil {
		t.Fatalf("splitToolCalls: %v", err)
	}
	if result == nil {
		t.Fatal("expected result call")
	}
	if len(regular) != 0 {
		t.Fatalf("regular = %+v, want empty", regular)
	}
	if result.Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("args = %q", result.Function.Arguments)
	}
}

func TestApplyUsage(t *testing.T) {
	t.Parallel()
	buff := &MessagesBuffer{Tokens: 1}
	applyUsage(buff, nil)
	if buff.Tokens != 1 {
		t.Fatalf("nil usage mutated Tokens: %d", buff.Tokens)
	}
	applyUsage(buff, &model.Usage{PromptTokens: 1234})
	if buff.Tokens != 1234 {
		t.Fatalf("Tokens = %d, want 1234", buff.Tokens)
	}
}

func TestAccumulateToolCallDeltas(t *testing.T) {
	t.Parallel()
	idx0 := 0
	var calls []tool.ToolCall
	var argBufs []strings.Builder
	accumulateToolCallDeltas(&calls, &argBufs, []tool.ToolCall{{
		Index: &idx0,
		Id:    "call-1",
		Type:  "function",
		Function: tool.FunctionCall{
			Name:      "submit_handoff",
			Arguments: `{"city":`,
		},
	}})
	accumulateToolCallDeltas(&calls, &argBufs, []tool.ToolCall{{
		Index: &idx0,
		Function: tool.FunctionCall{
			Arguments: `"Paris","temp_c":20}`,
		},
	}})
	if len(calls) != 1 {
		t.Fatalf("len = %d, want 1", len(calls))
	}
	if calls[0].Id != "call-1" || calls[0].Function.Name != "submit_handoff" {
		t.Fatalf("call = %+v", calls[0])
	}
	if got := argBufs[0].String(); got != `{"city":"Paris","temp_c":20}` {
		t.Fatalf("arguments = %q", got)
	}
}

func TestBuildParamsDerivesToolsFromExecutablesAndResult(t *testing.T) {
	t.Parallel()
	leaked := tool.Tool{Type: "function", Function: tool.Function{Name: "leaked"}}
	a := Agent{
		ModelParams: model.Parameters{
			Model:      "m",
			Stream:     true,
			Tools:      []tool.Tool{leaked},
			ToolChoice: "auto",
		},
	}
	api := &MessagesBuffer{Messages: []model.Message{{Role: model.User, Content: "hi"}}}
	params := buildParams(a, api)
	if params.Stream {
		t.Fatal("Stream should be false so model.Complete can be used")
	}
	if len(params.Tools) != 0 {
		t.Fatalf("Tools = %+v, want cleared when Agent has no tools", params.Tools)
	}
	if params.ToolChoice != nil {
		t.Fatalf("ToolChoice = %#v, want nil when Agent has no tools", params.ToolChoice)
	}

	owned, err := tool.CreateTool("owned tool", pingTool)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	result := tool.Tool{Type: "function", Function: tool.Function{Name: "submit_handoff"}}
	a.Tools = []tool.CallableTool{owned}
	a.ResultTool = result
	params = buildParams(a, api)
	if len(params.Tools) != 2 {
		t.Fatalf("Tools len = %d, want 2", len(params.Tools))
	}
	if params.Tools[0].Function.Name != owned.Function.Name {
		t.Fatalf("Tools[0] = %q, want %q", params.Tools[0].Function.Name, owned.Function.Name)
	}
	if params.Tools[1].Function.Name != "submit_handoff" {
		t.Fatalf("Tools[1] = %q, want submit_handoff", params.Tools[1].Function.Name)
	}
	if params.ToolChoice != "auto" {
		t.Fatalf("ToolChoice = %#v, want auto", params.ToolChoice)
	}
}

func TestValidateCompleteTurnRequiresResultTool(t *testing.T) {
	t.Parallel()
	err := validateCompleteTurn(Agent{})
	if err == nil || !strings.Contains(err.Error(), "ResultTool is required") {
		t.Fatalf("error = %v, want ResultTool is required", err)
	}

	err = validateCompleteTurn(Agent{
		ResultTool: tool.Tool{Type: "function", Function: tool.Function{Name: "submit_handoff"}},
	})
	if err != nil {
		t.Fatalf("validateCompleteTurn: %v", err)
	}
}

func TestStreamModelRoundForwardsErrorChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var params model.Parameters
		_ = json.Unmarshal(body, &params)
		if !params.Stream {
			t.Error("expected stream=true")
		}
		http.Error(w, "upstream failed", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	// 502 is retried; allow time for backoff.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out := make(chan model.ChatStreamChunk, 4)
	_, _, _, err := streamModelRound(ctx, model.OpenRouter, model.Parameters{
		URL:      srv.URL,
		APIKey:   "test-key",
		Model:    "m",
		Messages: []model.Message{{Role: model.User, Content: "hi"}},
	}, out)
	if err == nil {
		t.Fatal("expected stream error")
	}

	select {
	case chunk := <-out:
		if chunk.Error == nil {
			t.Fatalf("expected error chunk, got %+v", chunk)
		}
	default:
		t.Fatal("expected error chunk forwarded to streamOut")
	}
}
