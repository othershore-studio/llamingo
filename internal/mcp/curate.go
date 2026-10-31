// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import (
	"context"
	"fmt"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// Curate wraps a registry so only enabled tools are offered to the model and
// callable. A small local model drowns in too many tools — native tool calls
// degrade into confabulated ReAct text once the set grows past a handful — so the
// user can trim it. Disabled names are dropped from Tools() and refused by Call().
//
// The disabled set is supplied per turn (the caller reads it fresh from config),
// so toggling a tool takes effect on the next turn without reconnecting any MCP
// server. An empty set is a no-op: the registry is returned unwrapped.
func Curate(reg Registry, disabled map[string]bool) Registry {
	if len(disabled) == 0 {
		return reg
	}
	return curated{reg: reg, disabled: disabled}
}

type curated struct {
	reg      Registry
	disabled map[string]bool
}

func (c curated) Tools() []backend.ToolDef {
	all := c.reg.Tools()
	out := make([]backend.ToolDef, 0, len(all))
	for _, t := range all {
		if !c.disabled[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

func (c curated) Call(ctx context.Context, name, arguments string) (string, error) {
	if c.disabled[name] {
		return "", fmt.Errorf("mcp: tool %q is disabled", name)
	}
	return c.reg.Call(ctx, name, arguments)
}

var _ Registry = curated{}
