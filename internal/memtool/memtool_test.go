// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package memtool_test

import (
	"context"
	"testing"

	"github.com/othershore-studio/llamingo/internal/memtool"
	"github.com/othershore-studio/llamingo/internal/permission"
)

type fakeWriter struct {
	calls   int
	content string
}

func (f *fakeWriter) WriteMemory(content string) error {
	f.calls++
	f.content = content
	return nil
}

type fixedPrompter struct{ allow bool }

func (p fixedPrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	return permission.Decision{Allow: p.allow, Scope: permission.ScopeSession}, nil
}

func gate(allow bool) *permission.Gate {
	return permission.New(fixedPrompter{allow}, permission.NewMemoryGrantStore())
}

func TestWriteMemoryGated(t *testing.T) {
	w := &fakeWriter{}
	tool := memtool.NewTool(w, gate(true))

	out, err := tool.Call(context.Background(), memtool.ToolName, `{"content":"Daniel prefers tea."}`)
	if err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 || w.content != "Daniel prefers tea." {
		t.Errorf("writer got calls=%d content=%q, want one write of the content", w.calls, w.content)
	}
	if out == "" {
		t.Error("want a non-empty result for the model to read back")
	}
}

func TestGateDeniedBlocksWrite(t *testing.T) {
	w := &fakeWriter{}
	tool := memtool.NewTool(w, gate(false))

	if _, err := tool.Call(context.Background(), memtool.ToolName, `{"content":"x"}`); err == nil {
		t.Fatal("want a refusal when the gate denies")
	}
	if w.calls != 0 {
		t.Errorf("memory written despite denial (calls=%d)", w.calls)
	}
}

func TestEmptyContentRejected(t *testing.T) {
	w := &fakeWriter{}
	tool := memtool.NewTool(w, gate(true))

	if _, err := tool.Call(context.Background(), memtool.ToolName, `{"content":"   "}`); err == nil {
		t.Error("want an error for empty content")
	}
	if w.calls != 0 {
		t.Errorf("wrote empty content (calls=%d)", w.calls)
	}
}

func TestToolAdvertised(t *testing.T) {
	defs := memtool.NewTool(&fakeWriter{}, gate(true)).Tools()
	if len(defs) != 1 || defs[0].Name != memtool.ToolName {
		t.Fatalf("tool defs = %+v, want one %q", defs, memtool.ToolName)
	}
}
