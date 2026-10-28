// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package memtool is the native write_memory tool: it lets the model update the
// user's long-term personal memory, through the permission gate. The model
// already sees the current memory in context (the "memory" segment the context
// builder injects), so it composes the complete new version; the tool writes it
// verbatim. A sibling of imagegen as a native, gate-mediated Registry tool. See
// docs/architecture.md → "Memory" and "The permission gate".
package memtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/permission"
)

// Memory is the slice of the store the tool needs: read the current memory (so
// an unchanged write is a no-op) and replace it. *store.Store satisfies it;
// tests fake it.
type Memory interface {
	Memory() (content string, tampered bool, err error)
	WriteMemory(content string) error
}

// ToolName is the tool the model calls.
const ToolName = "write_memory"

// Tool is the built-in memory-write tool. Its Tools/Call methods satisfy the MCP
// loop's Registry, so it is offered to the model alongside MCP tools.
type Tool struct {
	mem  Memory
	gate *permission.Gate
}

// NewTool returns the write-memory tool.
func NewTool(mem Memory, gate *permission.Gate) *Tool {
	return &Tool{mem: mem, gate: gate}
}

// Tools advertises the write-memory tool to the model.
func (t *Tool) Tools() []backend.ToolDef {
	return []backend.ToolDef{{
		Name: ToolName,
		Description: "Replace your long-term personal memory with new content. " +
			"The current memory is shown to you above — only call this if your new version " +
			"actually differs from it, and write the complete new version, not a fragment. " +
			"Use it for durable facts and preferences worth remembering across chats.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"content":{"type":"string","description":"the complete new memory text"}},` +
			`"required":["content"]}`),
	}}
}

// Call updates the personal memory: gate, then write. The gate request's Detail
// carries the proposed content, so the user approves exactly what will be
// written. Risk is Medium — a write/side effect, so the gate offers only once or
// this-session grants (never a blanket "always"): the model cannot silently
// rewrite memory on a standing permission.
func (t *Tool) Call(ctx context.Context, name, arguments string) (string, error) {
	if name != ToolName {
		return "", fmt.Errorf("memtool: unknown tool %q", name)
	}
	var a struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return "", fmt.Errorf("memtool: arguments: %w", err)
	}
	if strings.TrimSpace(a.Content) == "" {
		return "", fmt.Errorf("memtool: content is required")
	}

	// Read-before-write: if the proposed content matches the current memory, it is
	// a no-op — skip the gate and the write, and say so, so the model does not
	// prompt the user or claim a save it did not make.
	if cur, _, err := t.mem.Memory(); err == nil && strings.TrimSpace(cur) == strings.TrimSpace(a.Content) {
		return "No change: your personal memory already contains exactly this; nothing was written.", nil
	}

	allow, err := t.gate.Allow(ctx, permission.Request{
		ToolID: "native:" + ToolName,
		Label:  "update your personal memory",
		Detail: a.Content,
		Risk:   permission.RiskMedium,
	})
	if err != nil {
		return "", err
	}
	if !allow {
		// A refusal is a decision, not an error: return it as a result (not an
		// exception the loop labels "tool error") so the model is told plainly the
		// write did NOT happen and does not report success.
		return "Not saved: you declined to update your personal memory. Do not tell the user it was saved.", nil
	}

	if err := t.mem.WriteMemory(a.Content); err != nil {
		return "", err
	}
	return fmt.Sprintf("memory updated (%d characters)", len(a.Content)), nil
}
