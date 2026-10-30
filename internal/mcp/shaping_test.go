// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import (
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
)

func TestWithShapingGuidance(t *testing.T) {
	hasGuidance := func(msgs []backend.Message) bool {
		for _, m := range msgs {
			if m.Role == backend.RoleSystem && strings.Contains(m.Content, "caption") {
				return true
			}
		}
		return false
	}

	// After a tool result that carries an image, the guidance is appended as the
	// final (system) message — steering the shaping round toward a clean caption.
	withImage := []backend.Message{
		{Role: backend.RoleUser, Content: "draw a cat"},
		{Role: backend.RoleAssistant, ToolCalls: []backend.ToolCall{{ID: "1"}}},
		{Role: backend.RoleTool, Content: "Image generated with DreamShaperXL_Turbo_SFW (seed 42, 5s): ![generated image](attachments/x.png)"},
	}
	got := withShapingGuidance(withImage)
	if len(got) != len(withImage)+1 {
		t.Fatalf("expected one appended message, got %d (input had %d)", len(got), len(withImage))
	}
	if got[len(got)-1].Role != backend.RoleSystem || !hasGuidance(got) {
		t.Errorf("last message is not the guidance system message: %+v", got[len(got)-1])
	}
	if len(withImage) != 3 {
		t.Errorf("input was mutated: now %d messages", len(withImage))
	}

	// An intermediate tool whose result has no image (e.g. list_image_models) must
	// NOT get the guidance — a follow-up tool call should still be allowed.
	noImage := []backend.Message{
		{Role: backend.RoleTool, Content: "Configured image models (default: DreamShaperXL_Turbo_SFW): ..."},
	}
	if got := withShapingGuidance(noImage); len(got) != 1 || hasGuidance(got) {
		t.Errorf("guidance wrongly injected after a non-image tool result: %+v", got)
	}

	// Not after a plain user/assistant turn.
	plain := []backend.Message{{Role: backend.RoleUser, Content: "hi"}}
	if got := withShapingGuidance(plain); len(got) != 1 || hasGuidance(got) {
		t.Errorf("guidance injected when the last turn was not a tool: %+v", got)
	}

	// Empty input is safe.
	if got := withShapingGuidance(nil); got != nil {
		t.Errorf("nil input should return nil, got %+v", got)
	}
}
