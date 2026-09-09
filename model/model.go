// Package model is an OpenAI-compatible chat-completions client: messages,
// named providers, and streaming. Default host is OpenRouter.
package model

import (
	"context"
	"fmt"

	aihttp "github.com/steckerfy/tiny_agents/internal/http"
	"github.com/steckerfy/tiny_agents/tool"
)

type ReasoningEffort string

const (
	EffortXHigh   ReasoningEffort = "xhigh"
	EffortHigh    ReasoningEffort = "high"
	EffortMedium  ReasoningEffort = "medium"
	EffortLow     ReasoningEffort = "low"
	EffortMinimal ReasoningEffort = "minimal"
	EffortNone    ReasoningEffort = "none"
)

type Verbosity string

const (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
	VerbosityXHigh  Verbosity = "xhigh"
	VerbosityMax    Verbosity = "max"
)

type MessageRole string

const (
	System    MessageRole = "system"
	User      MessageRole = "user"
	Assistant MessageRole = "assistant"
	Tool      MessageRole = "tool"
)

type Message struct {
	Role       MessageRole     `json:"role"`
	Content    any             `json:"content"` // string | []ContentPart | null
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []tool.ToolCall `json:"tool_calls,omitempty"`
}

type JSONSchema struct {
	Name   string         `json:"name"`
	Strict *bool          `json:"strict,omitempty"`
	Schema map[string]any `json:"schema"`
}

type ResponseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

type Parameters struct {
	// URL overrides the catalog provider URL for this request only. Not sent in the JSON body.
	URL string `json:"-"`
	// APIKey overrides the catalog/env key for this request only. Not sent in the JSON body.
	APIKey              string             `json:"-"`
	Messages            []Message          `json:"messages,omitempty"`
	Model               string             `json:"model,omitempty"`
	SessionID           string             `json:"session_id,omitempty"`
	PromptCacheKey      string             `json:"prompt_cache_key,omitempty"`
	CacheControl        map[string]any     `json:"cache_control,omitempty"`
	Provider            map[string]any     `json:"provider,omitempty"` // OpenRouter routing; unrelated to Agent.Provider
	ResponseFormat      *ResponseFormat    `json:"response_format,omitempty"`
	Stop                any                `json:"stop,omitempty"` // string | []string
	Stream              bool               `json:"stream,omitempty"`
	StreamOptions       *StreamOptions     `json:"stream_options,omitempty"`
	MaxTokens           *int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	TopK                *int               `json:"top_k,omitempty"`
	FrequencyPenalty    *float64           `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64           `json:"presence_penalty,omitempty"`
	RepetitionPenalty   *float64           `json:"repetition_penalty,omitempty"`
	MinP                *float64           `json:"min_p,omitempty"`
	TopA                *float64           `json:"top_a,omitempty"`
	Seed                *int               `json:"seed,omitempty"`
	LogitBias           map[string]float64 `json:"logit_bias,omitempty"`
	Logprobs            *bool              `json:"logprobs,omitempty"`
	TopLogprobs         *int               `json:"top_logprobs,omitempty"`
	Tools               []tool.Tool        `json:"tools,omitempty"`
	ToolChoice          any                `json:"tool_choice,omitempty"` // "none"|"auto"|forced object
	ParallelToolCalls   *bool              `json:"parallel_tool_calls,omitempty"`
	StructuredOutputs   *bool              `json:"structured_outputs,omitempty"`
	IncludeReasoning    *bool              `json:"include_reasoning,omitempty"` // deprecated alias
	Reasoning           map[string]any     `json:"reasoning,omitempty"`
	ReasoningEffort     ReasoningEffort    `json:"reasoning_effort,omitempty"`
	WebSearchOptions    map[string]any     `json:"web_search_options,omitempty"`
	Verbosity           Verbosity          `json:"verbosity,omitempty"`
}

// StreamOptions configures OpenAI-compatible streaming extras.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type Choice struct {
	FinishReason       *string `json:"finish_reason"`
	NativeFinishReason *string `json:"native_finish_reason"`
	Message            Message `json:"message"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Object  string   `json:"object"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type MessageDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   *string         `json:"content"`
	ToolCalls []tool.ToolCall `json:"tool_calls,omitempty"`
}

type StreamChoice struct {
	FinishReason       *string      `json:"finish_reason"`
	NativeFinishReason *string      `json:"native_finish_reason"`
	Delta              MessageDelta `json:"delta"`
}

type StreamError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ChatStreamChunk struct {
	ID      string         `json:"id"`
	Choices []StreamChoice `json:"choices"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Object  string         `json:"object"`
	Usage   *Usage         `json:"usage,omitempty"`
	Error   *StreamError   `json:"error,omitempty"`
}

// Complete runs a non-streaming chat completion against a catalog provider.
// Parameters.URL / Parameters.APIKey overlay the catalog row for this request only.
func Complete(ctx context.Context, provider Provider, params Parameters) (ChatResponse, error) {
	if params.Stream {
		return ChatResponse{}, fmt.Errorf("Complete: use CompleteStream for streaming requests")
	}
	if len(params.Messages) == 0 {
		return ChatResponse{}, fmt.Errorf("messages required")
	}
	url, key, err := resolve(provider, params)
	if err != nil {
		return ChatResponse{}, err
	}
	headers := []aihttp.Header{{Name: "Authorization", Value: "Bearer " + key}}

	res, err := aihttp.Mutate[Parameters, ChatResponse](ctx, aihttp.MutationParams[Parameters]{
		Method:  aihttp.POST,
		Url:     url,
		Body:    params,
		Headers: headers,
	})
	if err != nil {
		return ChatResponse{}, err
	}
	return res.Value, nil
}

// CompleteStream starts a streaming chat completion. The returned error is only
// for validation failures. HTTP/stream errors after start are delivered as a
// final ChatStreamChunk with Error set, then the channel is closed.
func CompleteStream(ctx context.Context, provider Provider, params Parameters) (<-chan ChatStreamChunk, error) {
	if len(params.Messages) == 0 {
		return nil, fmt.Errorf("messages required")
	}
	url, key, err := resolve(provider, params)
	if err != nil {
		return nil, err
	}
	headers := []aihttp.Header{{Name: "Authorization", Value: "Bearer " + key}}
	params.Stream = true
	params.StreamOptions = &StreamOptions{IncludeUsage: true}

	ch := make(chan ChatStreamChunk)
	go func() {
		defer close(ch)
		err := aihttp.MutateStream(ctx, aihttp.MutationParams[Parameters]{
			Method:  aihttp.POST,
			Url:     url,
			Body:    params,
			Headers: headers,
		}, func(chunk ChatStreamChunk) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- chunk:
				return nil
			}
		})
		if err != nil {
			select {
			case <-ctx.Done():
			case ch <- ChatStreamChunk{Error: &StreamError{Code: -1, Message: err.Error()}}:
			}
		}
	}()
	return ch, nil
}
