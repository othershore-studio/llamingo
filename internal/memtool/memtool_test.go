// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package memtool_test

import (
	"context"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/memtool"
	"github.com/othershore-studio/llamingo/internal/permission"
)

// fakeMem is a store stand-in: it holds the current memory and records writes.
type fakeMem struct {
	content string
	writes  int
}

func (f *fakeMem) Memory() (string, bool, error) { return f.content, false, nil }

func (f *fakeMem) WriteMemory(content string) error {
	f.content = content
	f.writes++
	return nil
}

// countingPrompter records how often the gate asked, so a no-op can assert it
// never prompted.
type countingPrompter struct {
	allow bool
	asked int
}

func (p *countingPrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	p.asked++
	return permission.Decision{Allow: p.allow, Scope: permission.ScopeSession}, nil
}

func newTool(mem *fakeMem, allow bool) (*memtool.Tool, *countingPrompter) {
	p := &countingPrompter{allow: allow}
	return memtool.NewTool(mem, permission.New(p, permission.NewMemoryGrantStore())), p
}

func TestWriteMemoryGated(t *testing.T) {
	mem := &fakeMem{}
	tool, prompter := newTool(mem, true)

	out, err := tool.Call(context.Background(), memtool.ToolName, `{"content":"Daniel prefers tea."}`)
	if err != nil {
		t.Fatal(err)
	}
	if mem.writes != 1 || mem.content != "Daniel prefers tea." {
		t.Errorf("writer got writes=%d content=%q", mem.writes, mem.content)
	}
	if prompter.asked != 1 {
		t.Errorf("gate asked %d times, want 1", prompter.asked)
	}
	if out == "" {
		t.Error("want a non-empty result for the model to read back")
	}
}

// A denial is surfaced as a result (not an error), so the model is told plainly
// it was not saved; nothing is written.
func TestDenialIsResultNotError(t *testing.T) {
	mem := &fakeMem{}
	tool, _ := newTool(mem, false)

	out, err := tool.Call(context.Background(), memtool.ToolName, `{"content":"x"}`)
	if err != nil {
		t.Fatalf("a refusal must not be an error: %v", err)
	}
	if mem.writes != 0 {
		t.Errorf("memory written despite denial (writes=%d)", mem.writes)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "not saved") && !strings.Contains(low, "declin") {
		t.Errorf("denial result = %q, want a clear 'not saved' message", out)
	}
}

// Writing content identical to what is already stored is a no-op: no gate
// prompt, no write, and a clear "no change" result.
func TestUnchangedIsNoop(t *testing.T) {
	mem := &fakeMem{content: "Daniel prefers tea."}
	tool, prompter := newTool(mem, true)

	out, err := tool.Call(context.Background(), memtool.ToolName, `{"content":"Daniel prefers tea."}`)
	if err != nil {
		t.Fatal(err)
	}
	if mem.writes != 0 {
		t.Errorf("rewrote identical content (writes=%d)", mem.writes)
	}
	if prompter.asked != 0 {
		t.Errorf("prompted for a no-op write (asked=%d)", prompter.asked)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "no change") && !strings.Contains(low, "already") {
		t.Errorf("no-op result = %q, want a 'no change' message", out)
	}
}

func TestEmptyContentRejected(t *testing.T) {
	mem := &fakeMem{}
	tool, _ := newTool(mem, true)
	if _, err := tool.Call(context.Background(), memtool.ToolName, `{"content":"   "}`); err == nil {
		t.Error("want an error for empty content")
	}
	if mem.writes != 0 {
		t.Errorf("wrote empty content (writes=%d)", mem.writes)
	}
}

func TestToolAdvertised(t *testing.T) {
	mem := &fakeMem{}
	tool, _ := newTool(mem, true)
	defs := tool.Tools()
	if len(defs) != 1 || defs[0].Name != memtool.ToolName {
		t.Fatalf("tool defs = %+v, want one %q", defs, memtool.ToolName)
	}
}
