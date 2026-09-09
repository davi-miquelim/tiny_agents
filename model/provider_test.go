package model

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	cfg, err := lookup(OpenRouter)
	if err != nil {
		t.Fatalf("lookup(OpenRouter): %v", err)
	}
	if cfg.DefaultModel != "openrouter/free" {
		t.Fatalf("DefaultModel = %q", cfg.DefaultModel)
	}

	cfg, err = lookup(OpenAI)
	if err != nil {
		t.Fatalf("lookup(OpenAI): %v", err)
	}
	if cfg.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("APIKeyEnv = %q", cfg.APIKeyEnv)
	}

	_, err = lookup("")
	if err == nil {
		t.Fatal("expected error for empty provider")
	}
	_, err = lookup(Provider("nope"))
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestResolveOverlays(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")

	url, key, err := resolve(OpenRouter, Parameters{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg, _ := lookup(OpenRouter)
	if url != cfg.URL {
		t.Fatalf("url = %q, want catalog", url)
	}
	if key != "env-key" {
		t.Fatalf("key = %q, want env-key", key)
	}

	url, key, err = resolve(OpenRouter, Parameters{URL: "http://test", APIKey: "per-call"})
	if err != nil {
		t.Fatalf("resolve overlays: %v", err)
	}
	if url != "http://test" || key != "per-call" {
		t.Fatalf("got url=%q key=%q", url, key)
	}
}

func TestResolveMissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, _, err := resolve(OpenRouter, Parameters{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("error = %q, want mention of env var", err)
	}
}

func TestResolveUnknownProvider(t *testing.T) {
	_, _, err := resolve(Provider(""), Parameters{APIKey: "k"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestParametersAPIKeyNotInJSON(t *testing.T) {
	_, overlay := withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth := r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		rawBody := string(body)
		if gotAuth != "Bearer secret-should-not-serialize" {
			t.Errorf("Authorization = %q", gotAuth)
		}
		if strings.Contains(rawBody, "secret-should-not-serialize") || strings.Contains(rawBody, "APIKey") {
			t.Errorf("request body leaked API key: %s", rawBody)
		}
		if strings.Contains(rawBody, `"URL"`) {
			t.Errorf("request body leaked URL: %s", rawBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","choices":[{"message":{"role":"assistant","content":"ok"}}],"created":1,"model":"m","object":"chat.completion"}`))
	})

	params := mustParams(t, OpenRouter)
	params.URL = overlay.URL
	params.APIKey = "secret-should-not-serialize"
	params.Messages = []Message{{Role: User, Content: "hi"}}

	if _, err := Complete(context.Background(), OpenRouter, params); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}
