// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"context"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/mcp"
)

type fakeCurateReg struct{ called string }

func (r *fakeCurateReg) Tools() []backend.ToolDef {
	return []backend.ToolDef{{Name: "keep"}, {Name: "drop"}}
}
func (r *fakeCurateReg) Call(ctx context.Context, name, arguments string) (string, error) {
	r.called = name
	return "ran " + name, nil
}

// A disabled tool is dropped from the advertised set and refused by Call —
// without ever reaching the wrapped registry.
func TestCurateFiltersAndGuards(t *testing.T) {
	base := &fakeCurateReg{}
	reg := mcp.Curate(base, map[string]bool{"drop": true})

	tools := reg.Tools()
	if len(tools) != 1 || tools[0].Name != "keep" {
		t.Fatalf("tools = %v, want only 'keep'", tools)
	}
	if _, err := reg.Call(context.Background(), "drop", "{}"); err == nil {
		t.Error("calling a disabled tool should error")
	}
	if base.called != "" {
		t.Errorf("a disabled Call should not reach the base registry (it called %q)", base.called)
	}
	if out, err := reg.Call(context.Background(), "keep", "{}"); err != nil || out != "ran keep" {
		t.Errorf("enabled Call = %q, %v; want it to pass through", out, err)
	}
}

// An empty disabled set is a no-op: the registry is returned unwrapped.
func TestCurateEmptyIsNoop(t *testing.T) {
	base := &fakeCurateReg{}
	if mcp.Curate(base, nil) != mcp.Registry(base) {
		t.Error("an empty disabled set should return the registry unwrapped")
	}
}
