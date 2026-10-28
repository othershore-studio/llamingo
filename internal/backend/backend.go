// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package backend is the single boundary to the inference engine. llama.cpp's
// LlamaCppNative is the only implementation; a future engine swap is a
// contained change here, not a codebase-wide rewrite. See docs/architecture.md.
package backend

import "context"

// Role identifies who authored a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one chat message in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Props is the slice of llama-server /props state the client needs: the real
// context window, the model identifier, and the model's chat template.
type Props struct {
	NCtx         int
	ModelID      string
	ChatTemplate string
}

// Usage is the authoritative token accounting a completion reports.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Delta is one streamed increment of a chat completion.
type Delta struct {
	// Content is assistant text produced in this chunk, if any.
	Content string
	// Reasoning is "thinking" text (reasoning_content) in this chunk, if any.
	Reasoning string
	// HasToolCall reports whether this chunk carried a tool call. Increment #1
	// detects but does not act on tool calls.
	HasToolCall bool
	// Done is true on the terminal chunk.
	Done bool
	// Usage is set on the terminal chunk when the server reports token usage.
	Usage *Usage
}

// Backend is the one boundary to the engine. LlamaCppNative is the only
// implementation.
type Backend interface {
	// Props reports context size, model, and chat template.
	Props(ctx context.Context) (Props, error)
	// Tokenize returns the exact token count for a rendered string.
	Tokenize(ctx context.Context, text string) (int, error)
	// ApplyTemplate renders messages through the model's chat template and
	// returns the resulting prompt string.
	ApplyTemplate(ctx context.Context, msgs []Message) (string, error)
	// ChatStream streams a completion for msgs, invoking fn for each Delta in
	// order. It returns when the stream ends or when fn returns an error.
	ChatStream(ctx context.Context, msgs []Message, fn func(Delta) error) error
}
