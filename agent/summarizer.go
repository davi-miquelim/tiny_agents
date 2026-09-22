package agent

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/davi-miquelim/tiny_agents/model"
	"github.com/davi-miquelim/tiny_agents/tool"
)

// SummaryResult is the structured handoff payload from the summarizer agent.
type SummaryResult struct {
	Summary       string   `json:"summary" desc:"Concise narrative of the conversation so far"`
	KeyPoints     []string `json:"key_points" desc:"Important facts, decisions, and outcomes"`
	OpenQuestions []string `json:"open_questions" desc:"Unresolved questions or pending actions"`
}

func submitSummary(_ context.Context, _ SummaryResult) (any, error) { return nil, nil }

// NewSummarizerAgent returns an Agent configured to end each turn with a
// SummaryResult handoff via CompleteTurn. Compaction is disabled so a
// compacting loop cannot recurse into another summarizer turn.
func NewSummarizerAgent(provider model.Provider, modelParams model.Parameters) (Agent, error) {
	submit, err := tool.CreateTool(
		"Submit the conversation summary fields for this turn. Ends the turn.",
		submitSummary,
	)
	if err != nil {
		return Agent{}, err
	}
	return Agent{
		Role: "Conversation summarizer",
		Goals: []string{
			"Compress prior turns into a faithful handoff for another agent",
			"Preserve decisions, facts, and open questions; drop filler",
		},
		Instructions: strings.Join([]string{
			"Write a brief user-facing recap in your assistant message content.",
			"Put machine fields only in the " + submit.Function.Name + " tool arguments.",
			"You must call the " + submit.Function.Name + " tool to end the turn.",
		}, "\n"),
		Provider:      provider,
		ModelParams:   modelParams,
		ResultTool:    submit.Tool,
		MaxToolRounds: 1,
		Compaction:    NoCompaction(),
	}, nil
}

// FormatSummary formats a SummaryResult as a single user message for a new buffer.
func FormatSummary(s SummaryResult) string {
	var b strings.Builder
	b.WriteString("Previous conversation summary:\n")
	b.WriteString(s.Summary)

	if len(s.KeyPoints) > 0 {
		b.WriteString("\n\nKey points:")
		for _, p := range s.KeyPoints {
			b.WriteString("\n- ")
			b.WriteString(p)
		}
	}
	if len(s.OpenQuestions) > 0 {
		b.WriteString("\n\nOpen questions:")
		for _, q := range s.OpenQuestions {
			b.WriteString("\n- ")
			b.WriteString(q)
		}
	}
	return b.String()
}

// Summarize clones src, asks the summarizer agent to produce a SummaryResult,
// and returns a new buffer containing only the formatted summary as a user message.
// src is never modified. The returned buffer has Tokens == 0 until a later API turn.
// Streaming and compaction are disabled so this call cannot recurse into another Summarize.
func Summarize(ctx context.Context, summarizer Agent, src *MessagesBuffer) (*MessagesBuffer, SummaryResult, error) {
	var zero SummaryResult
	if src == nil {
		return nil, zero, fmt.Errorf("agent.Summarize: nil messages buffer")
	}

	summarizer.Stream = false
	summarizer.Compaction = NoCompaction()
	snapshot := CloneBuffer(src)
	snapshot.Messages = append(snapshot.Messages, model.Message{
		Role:    model.User,
		Content: "Summarize the conversation above for handoff to another agent.",
	})

	res, err := CompleteTurn[SummaryResult](ctx, summarizer, snapshot)
	if err != nil {
		return nil, zero, fmt.Errorf("agent.Summarize: %w", err)
	}

	return &MessagesBuffer{
		Messages: []model.Message{{
			Role:    model.User,
			Content: FormatSummary(res.Data),
		}},
	}, res.Data, nil
}

// compactIfNeeded returns a compacted copy of msgs (summary plus the latest
// user message) when Tokens has reached the agent's compaction window.
// msgs is never modified. Returns msgs itself when under the threshold,
// disabled, or there is no prior history to summarize.
func compactIfNeeded(ctx context.Context, a Agent, msgs *MessagesBuffer) (*MessagesBuffer, error) {
	if a.Compaction.disable {
		return msgs, nil
	}
	if !NeedsCompaction(msgs, cmp.Or(a.Compaction.window, defaultContextWindow)) {
		return msgs, nil
	}

	history, tail := msgs, []model.Message(nil)
	if n := len(msgs.Messages); n > 0 && msgs.Messages[n-1].Role == model.User {
		if n == 1 {
			return msgs, nil
		}
		clipped := *msgs
		clipped.Messages = msgs.Messages[:n-1]
		history, tail = &clipped, msgs.Messages[n-1:]
	}

	summarizer, err := NewSummarizerAgent(a.Provider, a.ModelParams)
	if err != nil {
		return nil, fmt.Errorf("compact: %w", err)
	}
	compacted, _, err := Summarize(ctx, summarizer, history)
	if err != nil {
		return nil, fmt.Errorf("compact: %w", err)
	}
	compacted.Messages = append(compacted.Messages, tail...)
	return compacted, nil
}
