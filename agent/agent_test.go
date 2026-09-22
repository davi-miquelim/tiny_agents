package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/davi-miquelim/tiny_agents/agent"
	"github.com/davi-miquelim/tiny_agents/model"
	"github.com/davi-miquelim/tiny_agents/tool"
)

type handoffArgs struct {
	City  string `json:"city" desc:"City name"`
	TempC int    `json:"temp_c" desc:"Temperature in Celsius"`
}

func testModelParams(modelName string) model.Parameters {
	p, err := model.NewParameters(model.OpenRouter)
	if err != nil {
		panic(err)
	}
	p.Model = modelName
	return p
}

func TestCompleteTurnMissingResultTool(t *testing.T) {
	t.Parallel()
	a := agent.Agent{
		Role:        "Bot",
		ModelParams: testModelParams("m"),
	}
	buff := &agent.MessagesBuffer{}
	_ = agent.AppendUserMessage(buff, "hi")
	_, err := agent.CompleteTurn[handoffArgs](context.Background(), a, buff)
	if err == nil || !strings.Contains(err.Error(), "ResultTool is required") {
		t.Fatalf("error = %v, want ResultTool is required", err)
	}
}

func TestCompleteTurnNilMessages(t *testing.T) {
	t.Parallel()
	submit, err := tool.CreateTool("Submit handoff", func(_ context.Context, h handoffArgs) (any, error) { return nil, nil })
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	a := agent.Agent{
		Role:        "Bot",
		ModelParams: testModelParams("m"),
		ResultTool:  submit.Tool,
	}
	_, err = agent.CompleteTurn[handoffArgs](context.Background(), a, nil)
	if err == nil || !strings.Contains(err.Error(), "nil messages buffer") {
		t.Fatalf("error = %v, want nil messages buffer", err)
	}
}

func TestAppendUserMessageNilBuffer(t *testing.T) {
	t.Parallel()
	err := agent.AppendUserMessage(nil, "hi")
	if err == nil || !strings.Contains(err.Error(), "nil messages buffer") {
		t.Fatalf("error = %v, want nil messages buffer", err)
	}
}

func TestAppendUserMessage(t *testing.T) {
	t.Parallel()
	buff := &agent.MessagesBuffer{}
	if err := agent.AppendUserMessage(buff, "first"); err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}
	if err := agent.AppendUserMessage(buff, "second"); err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}
	if len(buff.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(buff.Messages))
	}
	if buff.Messages[1].Content != "second" {
		t.Fatalf("second content = %#v", buff.Messages[1].Content)
	}
}

func TestDrainCompleteNoStream(t *testing.T) {
	t.Parallel()
	if err := agent.DrainComplete(agent.CompleteResult{}); err != nil {
		t.Fatalf("DrainComplete: %v", err)
	}
}

func TestDrainCompleteStreamError(t *testing.T) {
	t.Parallel()
	ch := make(chan model.ChatStreamChunk, 1)
	ch <- model.ChatStreamChunk{Error: &model.StreamError{Message: "boom"}}
	close(ch)
	err := agent.DrainComplete(agent.CompleteResult{Stream: ch})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want boom", err)
	}
}
