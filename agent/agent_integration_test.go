//go:build integration

package agent_test

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steckerfy/tiny_agents/agent"
	"github.com/steckerfy/tiny_agents/model"
	"github.com/steckerfy/tiny_agents/tool"
)

const integrationModel = "openai/gpt-4o-mini"

type weatherHandoff struct {
	City  string `json:"city" desc:"City name"`
	TempC int    `json:"temp_c" desc:"Temperature in Celsius"`
}

type sumHandoff struct {
	Sum       int  `json:"sum" desc:"Sum of the two numbers"`
	Completed bool `json:"completed" desc:"Whether the turn is done"`
}

type addArgs struct {
	A int `json:"a" desc:"First number"`
	B int `json:"b" desc:"Second number"`
}

func requireAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
}

func integrationCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 60*time.Second)
}

func addNumbers(_ context.Context, args addArgs) (any, error) {
	return map[string]any{"sum": args.A + args.B}, nil
}

var addCalls atomic.Int64

func addNumbersTracked(ctx context.Context, args addArgs) (any, error) {
	addCalls.Add(1)
	return addNumbers(ctx, args)
}

func submitWeatherTool(t *testing.T) tool.Tool {
	t.Helper()
	ct, err := tool.CreateTool(
		"Submit the weather handoff fields for this turn. Ends the turn.",
		func(_ context.Context, _ weatherHandoff) (any, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	return ct.Tool
}

func integrationModelParams() model.Parameters {
	p, err := model.NewParameters(model.OpenRouter)
	if err != nil {
		panic(err)
	}
	p.Model = integrationModel
	return p
}

func TestIntegrationCompleteTurnNonStream(t *testing.T) {
	requireAPIKey(t)
	ctx, cancel := integrationCtx(t)
	defer cancel()

	submit := submitWeatherTool(t)
	a := agent.Agent{
		Role: "Weather clerk",
		Goals: []string{
			"Tell the user a short spoken confirmation in message text",
			"Submit structured city and temperature via the result tool",
		},
		Instructions: strings.Join([]string{
			"Write a brief user-facing confirmation in your assistant message content.",
			"Put machine fields only in the " + submit.Function.Name + " tool arguments.",
			"Use city Paris and temp_c 20 exactly.",
			"You must call the " + submit.Function.Name + " tool to end the turn.",
		}, "\n"),
		Provider:    model.OpenRouter,
		ModelParams: integrationModelParams(),
		SessionID:   "integration-test-session",
		ResultTool:  submit,
		Stream:      false,
	}

	buff := &agent.MessagesBuffer{}
	if err := agent.AppendUserMessage(buff, "Please confirm: city is Paris and temperature is 20 Celsius. Reply briefly to me and submit the handoff."); err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}

	res, err := agent.CompleteTurn[weatherHandoff](ctx, a, buff)
	if err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if res.Stream != nil {
		t.Fatal("expected no stream for non-streaming turn")
	}
	if strings.TrimSpace(res.Content) == "" {
		t.Fatal("expected non-empty user-facing Content")
	}
	if !strings.EqualFold(res.Data.City, "Paris") {
		t.Fatalf("city = %q, want Paris", res.Data.City)
	}
	if res.Data.TempC != 20 {
		t.Fatalf("temp_c = %d, want 20", res.Data.TempC)
	}
}

func TestIntegrationCompleteTurnStream(t *testing.T) {
	requireAPIKey(t)
	ctx, cancel := integrationCtx(t)
	defer cancel()

	submit := submitWeatherTool(t)
	a := agent.Agent{
		Role: "Weather clerk",
		Goals: []string{
			"Speak a short confirmation to the user",
			"Submit structured handoff fields via the result tool",
		},
		Instructions: strings.Join([]string{
			"Write a brief user-facing confirmation in your assistant message content.",
			"Put machine fields only in the " + submit.Function.Name + " tool arguments.",
			"Use city Paris and temp_c 20 exactly.",
			"You must call the " + submit.Function.Name + " tool to end the turn.",
		}, "\n"),
		Provider:    model.OpenRouter,
		ModelParams: integrationModelParams(),
		SessionID:   "integration-test-session",
		ResultTool:  submit,
		Stream:      true,
	}

	buff := &agent.MessagesBuffer{}
	if err := agent.AppendUserMessage(buff, "Please confirm: city is Paris and temperature is 20 Celsius. Reply briefly to me and submit the handoff."); err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}

	res, err := agent.CompleteTurn[weatherHandoff](ctx, a, buff)
	if err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if res.Stream == nil {
		t.Fatal("expected stream channel")
	}

	var sawContentDelta bool
	var streamed strings.Builder
	for chunk := range res.Stream {
		if chunk.Error != nil {
			t.Fatalf("stream error: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				sawContentDelta = true
				streamed.WriteString(*choice.Delta.Content)
			}
		}
	}
	if err := res.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !sawContentDelta && strings.TrimSpace(res.Content) == "" {
		t.Fatal("expected content deltas or accumulated Content")
	}
	if strings.TrimSpace(res.Content) == "" && streamed.Len() == 0 {
		t.Fatal("expected non-empty user-facing content")
	}
	if !strings.EqualFold(res.Data.City, "Paris") {
		t.Fatalf("city = %q, want Paris", res.Data.City)
	}
	if res.Data.TempC != 20 {
		t.Fatalf("temp_c = %d, want 20", res.Data.TempC)
	}
}

func TestIntegrationCompleteTurnToolThenResult(t *testing.T) {
	requireAPIKey(t)
	ctx, cancel := integrationCtx(t)
	defer cancel()

	addCalls.Store(0)
	add, err := tool.CreateTool("Add two integers and return the sum", addNumbersTracked)
	if err != nil {
		t.Fatalf("CreateTool add: %v", err)
	}
	submit, err := tool.CreateTool(
		"Submit the final handoff fields for this turn. Ends the turn.",
		func(_ context.Context, _ sumHandoff) (any, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("CreateTool submit: %v", err)
	}

	a := agent.Agent{
		Role:  "Calculator clerk",
		Goals: []string{"Use the add tool, then submit the handoff"},
		Instructions: strings.Join([]string{
			"You must call the " + add.Function.Name + " tool with a=2 and b=3 before finishing.",
			"After you receive the tool result, write a brief user-facing confirmation in message content.",
			"Then call the " + submit.Function.Name + " tool with sum set to the tool result and completed=true.",
			"Do not call the handoff tool in the same turn as add.",
		}, "\n"),
		Provider:      model.OpenRouter,
		ModelParams:   integrationModelParams(),
		SessionID:     "integration-test-tool-session",
		Tools:         []tool.CallableTool{add},
		ResultTool:    submit.Tool,
		MaxToolRounds: 5,
	}

	buff := &agent.MessagesBuffer{}
	if err := agent.AppendUserMessage(buff, "Add 2 and 3 using the add tool, tell me the result briefly, then submit the handoff."); err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}

	res, err := agent.CompleteTurn[sumHandoff](ctx, a, buff)
	if err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if addCalls.Load() < 1 {
		t.Fatal("expected add tool to be invoked")
	}
	if strings.TrimSpace(res.Content) == "" {
		t.Fatal("expected non-empty user-facing Content")
	}
	if res.Data.Sum != 5 {
		t.Fatalf("sum = %d, want 5", res.Data.Sum)
	}
	if !res.Data.Completed {
		t.Fatal("expected completed=true")
	}
}
