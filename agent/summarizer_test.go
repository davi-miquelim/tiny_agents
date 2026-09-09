package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/steckerfy/tiny_agents/agent"
	"github.com/steckerfy/tiny_agents/model"
	"github.com/steckerfy/tiny_agents/tool"
)

func TestCloneBufferDoesNotAlias(t *testing.T) {
	t.Parallel()
	src := &agent.MessagesBuffer{
		Messages: []model.Message{
			{Role: model.User, Content: "hi"},
			{Role: model.Assistant, Content: "hello"},
		},
		Tokens: 42,
	}
	clone := agent.CloneBuffer(src)
	if clone.Tokens != 42 {
		t.Fatalf("Tokens = %d, want 42", clone.Tokens)
	}
	if len(clone.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(clone.Messages))
	}

	clone.Messages[0].Content = "mutated"
	clone.Tokens = 1
	if src.Messages[0].Content != "hi" {
		t.Fatalf("source message mutated: %#v", src.Messages[0].Content)
	}
	if src.Tokens != 42 {
		t.Fatalf("source Tokens mutated: %d", src.Tokens)
	}
}

func TestCloneBufferNil(t *testing.T) {
	t.Parallel()
	clone := agent.CloneBuffer(nil)
	if clone == nil {
		t.Fatal("expected non-nil clone")
	}
	if len(clone.Messages) != 0 || clone.Tokens != 0 {
		t.Fatalf("clone = %+v, want empty", clone)
	}
}

func TestCompactAtNegativeDisables(t *testing.T) {
	t.Parallel()
	if agent.CompactAt(-1) != agent.NoCompaction() {
		t.Fatal("CompactAt(<0) should equal NoCompaction")
	}
	if agent.CompactAt(0) == agent.NoCompaction() {
		t.Fatal("CompactAt(0) is default-on, not disabled")
	}
}

func TestNeedsCompaction(t *testing.T) {
	t.Parallel()
	buff := &agent.MessagesBuffer{Tokens: 100}
	if !agent.NeedsCompaction(buff, 100) {
		t.Fatal("expected true at exact window")
	}
	if !agent.NeedsCompaction(buff, 50) {
		t.Fatal("expected true when over window")
	}
	if agent.NeedsCompaction(buff, 101) {
		t.Fatal("expected false under window")
	}
	if agent.NeedsCompaction(buff, 0) {
		t.Fatal("expected false when contextWindow is 0")
	}
	if agent.NeedsCompaction(nil, 100) {
		t.Fatal("expected false for nil buffer")
	}
}

func TestFormatSummary(t *testing.T) {
	t.Parallel()
	got := agent.FormatSummary(agent.SummaryResult{
		Summary:       "We agreed on Paris.",
		KeyPoints:     []string{"City is Paris", "Temp 20C"},
		OpenQuestions: []string{"Confirm booking?"},
	})
	for _, want := range []string{
		"Previous conversation summary:",
		"We agreed on Paris.",
		"Key points:",
		"- City is Paris",
		"- Temp 20C",
		"Open questions:",
		"- Confirm booking?",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("FormatSummary missing %q in:\n%s", want, got)
		}
	}
}

func TestNewSummarizerAgent(t *testing.T) {
	t.Parallel()
	a, err := agent.NewSummarizerAgent(model.OpenRouter, testModelParams("m"))
	if err != nil {
		t.Fatalf("NewSummarizerAgent: %v", err)
	}
	if a.Provider != model.OpenRouter {
		t.Fatalf("Provider = %q, want openrouter", a.Provider)
	}
	if a.ResultTool.Function.Name == "" {
		t.Fatal("expected ResultTool")
	}
	if a.MaxToolRounds != 1 {
		t.Fatalf("MaxToolRounds = %d, want 1", a.MaxToolRounds)
	}
	if a.Stream {
		t.Fatal("summarizer should default to non-streaming")
	}
	if a.Compaction != agent.NoCompaction() {
		t.Fatal("summarizer must disable compaction")
	}
	if a.ResultTool.Function.Name != "submit_summary" {
		t.Fatalf("ResultTool name = %q, want submit_summary", a.ResultTool.Function.Name)
	}
	if len(a.Tools) != 0 {
		t.Fatal("result tool must not be in executable Tools")
	}
}

func TestCloneBufferUsesPortableHistory(t *testing.T) {
	t.Parallel()
	src := &agent.MessagesBuffer{
		Messages: []model.Message{
			{Role: model.System, Content: "stale"},
			{Role: model.User, Content: "hi"},
			{Role: model.Assistant, Content: "hello"},
			{Role: model.Tool, Content: `{"ok":true}`, ToolCallID: "x"},
		},
		Tokens: 9,
	}
	clone := agent.CloneBuffer(src)
	if clone.Tokens != 9 {
		t.Fatalf("Tokens = %d, want 9", clone.Tokens)
	}
	if len(clone.Messages) != 2 {
		t.Fatalf("len = %d, want 2 portable messages: %+v", len(clone.Messages), clone.Messages)
	}
	if clone.Messages[0].Role != model.User || clone.Messages[1].Role != model.Assistant {
		t.Fatalf("roles = %q,%q", clone.Messages[0].Role, clone.Messages[1].Role)
	}
}

func writeChat(w http.ResponseWriter, msg model.Message) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(model.ChatResponse{
		Choices: []model.Choice{{Message: msg}},
		Usage:   &model.Usage{PromptTokens: 3},
	})
}

func decodeParams(t *testing.T, r *http.Request) model.Parameters {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var params model.Parameters
	if err := json.Unmarshal(body, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	return params
}

func hasTool(params model.Parameters, name string) bool {
	for _, tl := range params.Tools {
		if tl.Function.Name == name {
			return true
		}
	}
	return false
}

func summaryToolCall() model.Message {
	return model.Message{
		Role:    model.Assistant,
		Content: "recap",
		ToolCalls: []tool.ToolCall{{
			Id:   "sum-1",
			Type: "function",
			Function: tool.FunctionCall{
				Name:      "submit_summary",
				Arguments: `{"summary":"prior chat","key_points":["decided Paris"],"open_questions":[]}`,
			},
		}},
	}
}

func TestCompleteCompactsOverWindow(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := decodeParams(t, r)
		switch requests.Add(1) {
		case 1:
			if !hasTool(params, "submit_summary") {
				t.Error("first request should be the summarizer")
			}
			for _, m := range params.Messages {
				if m.Role == model.User && m.Content == "new question" {
					t.Error("current user message should not be in the summarizer prompt")
				}
			}
			writeChat(w, summaryToolCall())
		case 2:
			if hasTool(params, "submit_summary") {
				t.Error("second request should be the agent turn, not another summarizer")
			}
			writeChat(w, model.Message{Role: model.Assistant, Content: "ok"})
		default:
			t.Errorf("unexpected model request %d", requests.Load())
		}
	}))
	t.Cleanup(srv.Close)

	a := agent.Agent{
		Role:         "Bot",
		Goals:        []string{"help"},
		Instructions: "Be brief.",
		Provider:     model.OpenRouter,
		ModelParams:  model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"},
		Compaction:   agent.CompactAt(100),
	}
	buff := &agent.MessagesBuffer{
		Tokens: 100,
		Messages: []model.Message{
			{Role: model.User, Content: "old question"},
			{Role: model.Assistant, Content: "old answer"},
			{Role: model.User, Content: "new question"},
		},
	}

	res, err := agent.Complete(context.Background(), a, buff)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Response == nil {
		t.Fatal("expected non-stream response")
	}
	if requests.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", requests.Load())
	}
	if len(buff.Messages) != 3 {
		t.Fatalf("transcript len = %d, want 3: %+v", len(buff.Messages), buff.Messages)
	}
	if got, ok := buff.Messages[0].Content.(string); !ok || !strings.Contains(got, "prior chat") {
		t.Fatalf("first message = %#v, want formatted summary", buff.Messages[0].Content)
	}
	if buff.Messages[1].Content != "new question" {
		t.Fatalf("kept user = %#v, want new question", buff.Messages[1].Content)
	}
	if buff.Messages[2].Content != "ok" {
		t.Fatalf("assistant = %#v, want ok", buff.Messages[2].Content)
	}
}

func TestCompleteSkipsCompactionWhenDisabled(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := decodeParams(t, r)
		if hasTool(params, "submit_summary") {
			t.Error("compaction should be disabled")
		}
		requests.Add(1)
		writeChat(w, model.Message{Role: model.Assistant, Content: "ok"})
	}))
	t.Cleanup(srv.Close)

	a := agent.Agent{
		Role:         "Bot",
		Goals:        []string{"help"},
		Instructions: "Be brief.",
		Provider:     model.OpenRouter,
		ModelParams:  model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"},
		Compaction:   agent.NoCompaction(),
	}
	buff := &agent.MessagesBuffer{
		Tokens: 100_000,
		Messages: []model.Message{
			{Role: model.User, Content: "old"},
			{Role: model.Assistant, Content: "ans"},
			{Role: model.User, Content: "new"},
		},
	}
	if _, err := agent.Complete(context.Background(), a, buff); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("model requests = %d, want 1", requests.Load())
	}
	if len(buff.Messages) != 4 {
		t.Fatalf("transcript len = %d, want 4 (no compact)", len(buff.Messages))
	}
}

func TestCompleteSkipsCompactionWithoutPriorHistory(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := decodeParams(t, r)
		if hasTool(params, "submit_summary") {
			t.Error("single user message should not be summarized")
		}
		requests.Add(1)
		writeChat(w, model.Message{Role: model.Assistant, Content: "ok"})
	}))
	t.Cleanup(srv.Close)

	a := agent.Agent{
		Role:         "Bot",
		Goals:        []string{"help"},
		Instructions: "Be brief.",
		Provider:     model.OpenRouter,
		ModelParams:  model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"},
		Compaction:   agent.CompactAt(10),
	}
	buff := &agent.MessagesBuffer{
		Tokens:   10,
		Messages: []model.Message{{Role: model.User, Content: "only"}},
	}
	if _, err := agent.Complete(context.Background(), a, buff); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("model requests = %d, want 1", requests.Load())
	}
}

func TestCompleteTurnCompactsOverWindow(t *testing.T) {
	submit, err := tool.CreateTool("Submit handoff", func(_ context.Context, _ handoffArgs) (any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := decodeParams(t, r)
		switch requests.Add(1) {
		case 1:
			if !hasTool(params, "submit_summary") {
				t.Error("first request should be the summarizer")
			}
			writeChat(w, summaryToolCall())
		case 2:
			if hasTool(params, "submit_summary") {
				t.Error("second request should be the handoff turn")
			}
			writeChat(w, model.Message{
				Role:    model.Assistant,
				Content: "done",
				ToolCalls: []tool.ToolCall{{
					Id:   "h-1",
					Type: "function",
					Function: tool.FunctionCall{
						Name:      submit.Function.Name,
						Arguments: `{"city":"Paris","temp_c":20}`,
					},
				}},
			})
		default:
			t.Errorf("unexpected model request %d", requests.Load())
		}
	}))
	t.Cleanup(srv.Close)

	a := agent.Agent{
		Role:         "Clerk",
		Goals:        []string{"handoff"},
		Instructions: "Call the result tool.",
		Provider:     model.OpenRouter,
		ModelParams:  model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"},
		ResultTool:   submit.Tool,
		Compaction:   agent.CompactAt(50),
	}
	buff := &agent.MessagesBuffer{
		Tokens: 50,
		Messages: []model.Message{
			{Role: model.User, Content: "old"},
			{Role: model.Assistant, Content: "ans"},
			{Role: model.User, Content: "now"},
		},
	}
	res, err := agent.CompleteTurn[handoffArgs](context.Background(), a, buff)
	if err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", requests.Load())
	}
	if res.Data.City != "Paris" || res.Data.TempC != 20 {
		t.Fatalf("data = %+v", res.Data)
	}
	if len(buff.Messages) != 3 {
		t.Fatalf("transcript len = %d, want 3: %+v", len(buff.Messages), buff.Messages)
	}
	if buff.Messages[1].Content != "now" {
		t.Fatalf("kept user = %#v, want now", buff.Messages[1].Content)
	}
}

func TestCompleteKeepsHistoryWhenTurnFailsAfterCompact(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			writeChat(w, summaryToolCall())
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}
	}))
	t.Cleanup(srv.Close)

	a := agent.Agent{
		Role:         "Bot",
		Goals:        []string{"help"},
		Instructions: "Be brief.",
		Provider:     model.OpenRouter,
		ModelParams:  model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"},
		Compaction:   agent.CompactAt(100),
	}
	buff := &agent.MessagesBuffer{
		Tokens: 100,
		Messages: []model.Message{
			{Role: model.User, Content: "old question"},
			{Role: model.Assistant, Content: "old answer"},
			{Role: model.User, Content: "new question"},
		},
	}

	if _, err := agent.Complete(context.Background(), a, buff); err == nil {
		t.Fatal("expected error from failed agent turn")
	}
	if len(buff.Messages) != 3 {
		t.Fatalf("transcript len = %d, want 3 original: %+v", len(buff.Messages), buff.Messages)
	}
	if buff.Messages[0].Content != "old question" || buff.Messages[2].Content != "new question" {
		t.Fatalf("history mutated after failed turn: %+v", buff.Messages)
	}
	if buff.Tokens != 100 {
		t.Fatalf("Tokens = %d, want 100", buff.Tokens)
	}
}

func TestCompleteTurnKeepsHistoryWhenTurnFailsAfterCompact(t *testing.T) {
	submit, err := tool.CreateTool("Submit handoff", func(_ context.Context, _ handoffArgs) (any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			writeChat(w, summaryToolCall())
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}
	}))
	t.Cleanup(srv.Close)

	a := agent.Agent{
		Role:         "Clerk",
		Goals:        []string{"handoff"},
		Instructions: "Call the result tool.",
		Provider:     model.OpenRouter,
		ModelParams:  model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"},
		ResultTool:   submit.Tool,
		Compaction:   agent.CompactAt(50),
	}
	buff := &agent.MessagesBuffer{
		Tokens: 50,
		Messages: []model.Message{
			{Role: model.User, Content: "old"},
			{Role: model.Assistant, Content: "ans"},
			{Role: model.User, Content: "now"},
		},
	}

	if _, err := agent.CompleteTurn[handoffArgs](context.Background(), a, buff); err == nil {
		t.Fatal("expected error from failed agent turn")
	}
	if len(buff.Messages) != 3 || buff.Messages[0].Content != "old" {
		t.Fatalf("history mutated after failed turn: %+v", buff.Messages)
	}
	if buff.Tokens != 50 {
		t.Fatalf("Tokens = %d, want 50", buff.Tokens)
	}
}

func TestSummarizeDisablesCompaction(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeChat(w, summaryToolCall())
	}))
	t.Cleanup(srv.Close)

	sum, err := agent.NewSummarizerAgent(model.OpenRouter, model.Parameters{Model: "m", URL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewSummarizerAgent: %v", err)
	}
	sum.Compaction = agent.CompactAt(10)

	src := &agent.MessagesBuffer{
		Tokens: 10,
		Messages: []model.Message{
			{Role: model.User, Content: "old"},
			{Role: model.Assistant, Content: "ans"},
		},
	}
	if _, _, err := agent.Summarize(context.Background(), sum, src); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("model requests = %d, want 1 (no nested compact)", requests.Load())
	}
}
