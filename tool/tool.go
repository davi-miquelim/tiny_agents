// Package tool turns Go functions into OpenAI-compatible function tools.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

type Items struct {
	Type string `json:"type"`
}

type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"default,omitempty"`
	Items       *Items   `json:"items,omitempty"`
}

type Params struct {
	Type       string              `json:"type"`
	Required   []string            `json:"required,omitempty"`
	Properties map[string]Property `json:"properties,omitempty"`
}

type Function struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  Params `json:"parameters"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type CallableTool struct {
	Tool
	call func(context.Context, string) (any, error)
}

// Call invokes the tool with ctx. Long-running tools should honor ctx.Done()
// so callers can cancel work such as timed-out tool executions.
func (c CallableTool) Call(ctx context.Context, argsJSON string) (any, error) {
	return c.call(ctx, argsJSON)
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	Id       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type ToolCallResult struct {
	Role       string `json:"role"`
	ToolCallId string `json:"tool_call_id"`
	Content    string `json:"content"`
}

func MapToJSONString(m map[string]any) (string, error) {
	bytes, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal map: %w", err)
	}
	return string(bytes), nil
}
