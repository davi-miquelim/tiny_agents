package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func withTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, Parameters) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, Parameters{URL: srv.URL, APIKey: "test-key"}
}

func mustParams(t *testing.T, p Provider) Parameters {
	t.Helper()
	params, err := NewParameters(p)
	if err != nil {
		t.Fatalf("NewParameters(%q): %v", p, err)
	}
	return params
}

func TestComplete(t *testing.T) {
	var gotBody Parameters
	_, overlay := withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ChatResponse{
			ID:      "resp-1",
			Object:  "chat.completion",
			Created: 1,
			Model:   "m",
			Choices: []Choice{{
				Message: Message{Role: Assistant, Content: "hi there"},
			}},
		})
	})

	params := mustParams(t, OpenRouter)
	params.URL = overlay.URL
	params.APIKey = overlay.APIKey
	params.Model = "m"
	params.Messages = []Message{
		{Role: User, Content: "hello"},
	}
	res, err := Complete(context.Background(), OpenRouter, params)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(res.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	content, ok := res.Choices[0].Message.Content.(string)
	if !ok || content != "hi there" {
		t.Fatalf("content = %#v, want %q", res.Choices[0].Message.Content, "hi there")
	}
	if gotBody.Model != "m" {
		t.Fatalf("request model = %q, want m", gotBody.Model)
	}
	if len(gotBody.Messages) != 1 {
		t.Fatalf("request messages len = %d, want 1", len(gotBody.Messages))
	}
	if gotBody.Stream {
		t.Fatal("request stream should be false/absent")
	}
}

func TestCompleteRejectsStream(t *testing.T) {
	_, err := Complete(context.Background(), OpenRouter, Parameters{
		Stream: true,
		Messages: []Message{
			{Role: User, Content: "hello"},
		},
	})
	if err == nil {
		t.Fatal("expected error when Stream is true")
	}
	if !strings.Contains(err.Error(), "CompleteStream") {
		t.Fatalf("error %q should mention CompleteStream", err)
	}
}

func TestCompleteRequiresMessages(t *testing.T) {
	_, err := Complete(context.Background(), OpenRouter, Parameters{Model: "m", APIKey: "k"})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
	if !strings.Contains(err.Error(), "messages required") {
		t.Fatalf("error %q should mention messages required", err)
	}
}

func TestCompleteUnknownProvider(t *testing.T) {
	_, err := Complete(context.Background(), Provider(""), Parameters{
		APIKey:   "k",
		Messages: []Message{{Role: User, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("error %q should mention unknown provider", err)
	}
}

func TestCompleteStream(t *testing.T) {
	_, overlay := withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var params Parameters
		if err := json.Unmarshal(body, &params); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if !params.Stream {
			t.Error("expected stream=true in request body")
		}
		if params.StreamOptions == nil || !params.StreamOptions.IncludeUsage {
			t.Error("expected stream_options.include_usage=true in request body")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter is not a Flusher")
		}
		chunks := []string{
			": OPENROUTER PROCESSING\n\n",
			`data: {"id":"gen-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"delta":{"content":"Hel"}}]}` + "\n\n",
			`data: {"id":"gen-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"delta":{"content":"lo"}}]}` + "\n\n",
			`data: {"id":"gen-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}` + "\n\n",
			"data: [DONE]\n\n",
		}
		for _, c := range chunks {
			if _, err := io.WriteString(w, c); err != nil {
				return
			}
			flusher.Flush()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := CompleteStream(ctx, OpenRouter, Parameters{
		URL:    overlay.URL,
		APIKey: overlay.APIKey,
		Model:  "m",
		Messages: []Message{
			{Role: User, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}

	var (
		content strings.Builder
		chunks  []ChatStreamChunk
	)
	for chunk := range ch {
		chunks = append(chunks, chunk)
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				content.WriteString(*choice.Delta.Content)
			}
		}
	}

	if got := content.String(); got != "Hello" {
		t.Fatalf("concatenated content = %q, want Hello", got)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	last := chunks[len(chunks)-1]
	if last.Usage == nil {
		t.Fatal("expected last chunk to include usage")
	}
	if last.Usage.PromptTokens != 1 || last.Usage.CompletionTokens != 2 || last.Usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v, want prompt=1 completion=2 total=3", last.Usage)
	}
}

func TestCompleteStreamRequiresMessages(t *testing.T) {
	_, err := CompleteStream(context.Background(), OpenRouter, Parameters{Model: "m", APIKey: "k"})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
	if !strings.Contains(err.Error(), "messages required") {
		t.Fatalf("error %q should mention messages required", err)
	}
}

func TestNewParameters(t *testing.T) {
	p := mustParams(t, OpenRouter)
	cfg, err := lookup(OpenRouter)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if p.Model != cfg.DefaultModel {
		t.Fatalf("Model = %q, want %q", p.Model, cfg.DefaultModel)
	}
	if p.Temperature == nil || *p.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", p.Temperature)
	}
	if p.MaxCompletionTokens == nil || *p.MaxCompletionTokens != 4096 {
		t.Fatalf("MaxCompletionTokens = %v, want 4096", p.MaxCompletionTokens)
	}
	if p.Provider == nil {
		t.Fatal("Provider should be set")
	}
	if p.Provider["allow_fallbacks"] != true {
		t.Fatalf("provider.allow_fallbacks = %v, want true", p.Provider["allow_fallbacks"])
	}

	p.Model = "custom/model"
	v := 0.2
	p.Temperature = &v
	if p.Model != "custom/model" {
		t.Fatalf("Model = %q, want custom/model", p.Model)
	}
	if p.Temperature == nil || *p.Temperature != 0.2 {
		t.Fatalf("Temperature = %v, want 0.2", p.Temperature)
	}
	if p.MaxCompletionTokens == nil || *p.MaxCompletionTokens != 4096 {
		t.Fatalf("MaxCompletionTokens = %v, want default 4096", p.MaxCompletionTokens)
	}
}

func TestNewParametersRequiresName(t *testing.T) {
	_, err := NewParameters("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestNewParametersProvider(t *testing.T) {
	p := mustParams(t, OpenRouter)
	p.Provider["sort"] = "latency"
	if p.Provider["allow_fallbacks"] != true {
		t.Fatalf("provider.allow_fallbacks = %v, want true", p.Provider["allow_fallbacks"])
	}
	if p.Provider["sort"] != "latency" {
		t.Fatalf("provider.sort = %v, want latency", p.Provider["sort"])
	}
}

func TestNewParametersOpenAI(t *testing.T) {
	p := mustParams(t, OpenAI)
	if p.Model != "" {
		t.Fatalf("Model = %q, want empty (openai has no DefaultModel)", p.Model)
	}
	if p.Provider != nil {
		t.Fatalf("Provider = %#v, want nil for non-openrouter", p.Provider)
	}
}
