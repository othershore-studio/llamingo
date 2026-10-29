// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package backend is the single boundary to the inference engine. llama.cpp's
// LlamaCppNative is the only implementation; a future engine swap is a
// contained change here, not a codebase-wide rewrite. See docs/architecture.md.
package backend

import (
	"context"
	"encoding/json"
)

// Role identifies who authored a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one chat message. An assistant message may carry ToolCalls; a tool
// message carries ToolCallID (which call it answers). The JSON tags match the
// OpenAI shape the server's chat template expects.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Props is the slice of llama-server /props state the client needs.
type Props struct {
	NCtx         int
	ModelID      string
	ModelAlias   string // llama.cpp --alias, when set; a friendly display name
	ChatTemplate string
}

// Usage is the authoritative token accounting a completion reports.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Timings is the server's performance report for a completion (llama.cpp
// `timings`): tokens processed and the per-second rates for prompt processing
// and token generation.
type Timings struct {
	PromptN          int
	PromptPerSecond  float64
	PredictN         int
	PredictPerSecond float64
}

// ToolDef is a tool offered to the model. Parameters is the raw JSON Schema for
// the arguments, passed through unchanged.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// FunctionCall is the function portion of a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string
}

// ToolCall is a tool invocation requested by the model (OpenAI shape).
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// Delta is one streamed increment of a chat completion.
type Delta struct {
	Content   string     // assistant text in this chunk, if any
	Reasoning string     // reasoning_content ("thinking") in this chunk, if any
	ToolCalls []ToolCall // assembled tool calls, set on the terminal (Done) delta
	Done      bool       // true on the terminal chunk
	Usage     *Usage     // set on the terminal chunk when the server reports it
	Timings   *Timings   // set on the terminal chunk when the server reports timings
}

// ChatRequest is a chat-completion request: the conversation plus any tools the
// client offers the model for this turn.
type ChatRequest struct {
	Messages []Message
	Tools    []ToolDef
	// TemplateKwargs are extra variables passed to the model's chat template
	// (llama.cpp's `chat_template_kwargs`), e.g. {"enable_thinking": false} to
	// turn a model's thinking on or off per request. Omitted from the wire when
	// empty, so the server's own default applies.
	TemplateKwargs map[string]any
}

// Backend is the one boundary to the engine. LlamaCppNative is the only
// implementation.
type Backend interface {
	// Props reports context size, model, and chat template.
	Props(ctx context.Context) (Props, error)
	// Tokenize returns the exact token count for a rendered string.
	Tokenize(ctx context.Context, text string) (int, error)
	// ApplyTemplate renders messages through the model's chat template.
	ApplyTemplate(ctx context.Context, msgs []Message) (string, error)
	// ChatStream streams a completion for req, invoking fn for each Delta in
	// order. Content streams as it arrives; assembled tool calls arrive on the
	// terminal (Done) delta.
	ChatStream(ctx context.Context, req ChatRequest, fn func(Delta) error) error
}
