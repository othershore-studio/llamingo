// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package permission is the safety surface: a gate that decides whether a tool
// action may run, how long an allow lasts, and how much friction a risk tier
// carries. The user holds the power — the gate never hard-refuses, it asks —
// but a dangerous action costs a deliberate, per-invocation yes. See
// docs/architecture.md → "The permission gate".
package permission

import (
	"context"
	"fmt"
	"sync"
)

// Risk is a tool's declared risk tier; it sets the default friction by deciding
// which grant scopes the gate offers.
type Risk int

const (
	RiskLow    Risk = iota // read-only or additive; cheap to grant broadly
	RiskMedium             // writes / side effects
	RiskHigh               // destructive or arbitrary execution
)

// Scope is how long an allow lasts.
type Scope int

const (
	ScopeOnce       Scope = iota // this invocation only
	ScopeSession                 // this chat
	ScopePersistent              // until revoked
)

// Request is a pending tool action awaiting a decision. ToolID is the stable
// identity a grant binds to — callers fold a hash of the tool definition into
// it, so a reshaped tool re-prompts (the confused-deputy guard).
type Request struct {
	ToolID string
	Label  string // human description, e.g. "generate an image"
	Detail string // specifics for an informed decision, e.g. the code to run
	Risk   Risk
}

// Decision is the user's answer; Scope matters only when Allow is true.
type Decision struct {
	Allow bool
	Scope Scope
}

// Prompter asks the user to decide, offering only the scopes allowed for the
// request's risk. The UI implements it; tests fake it.
type Prompter interface {
	Ask(ctx context.Context, req Request, allowed []Scope) (Decision, error)
}

// OfferedScopes returns the grant scopes the gate offers for a risk tier.
// Higher risk means less blanket trust: a high-risk action can only be allowed
// per-invocation, so the user reaffirms each time. It is informational friction
// — a chance to second-guess — not a refusal, since the user holds the power.
func OfferedScopes(r Risk) []Scope {
	switch r {
	case RiskLow:
		return []Scope{ScopeOnce, ScopeSession, ScopePersistent}
	case RiskMedium:
		return []Scope{ScopeOnce, ScopeSession}
	default: // RiskHigh
		return []Scope{ScopeOnce}
	}
}

// Gate decides whether a tool action may run. Persistent grants live in a
// GrantStore; session grants live in memory and die with the Gate.
type Gate struct {
	prompter Prompter
	store    GrantStore
	mu       sync.Mutex
	session  map[string]bool
}

// New returns a Gate backed by a prompter and a persistent grant store.
func New(p Prompter, store GrantStore) *Gate {
	return &Gate{prompter: p, store: store, session: map[string]bool{}}
}

// Allow reports whether req may proceed. A persistent or session grant short-
// circuits; otherwise it prompts, records the granted scope, and returns the
// decision. A scope the risk tier does not offer is rejected as an error.
func (g *Gate) Allow(ctx context.Context, req Request) (bool, error) {
	granted, err := g.store.Granted(req.ToolID)
	if err != nil {
		return false, err
	}
	if granted {
		return true, nil
	}

	g.mu.Lock()
	sess := g.session[req.ToolID]
	g.mu.Unlock()
	if sess {
		return true, nil
	}

	allowed := OfferedScopes(req.Risk)
	dec, err := g.prompter.Ask(ctx, req, allowed)
	if err != nil {
		return false, err
	}
	if !dec.Allow {
		return false, nil
	}
	if !contains(allowed, dec.Scope) {
		return false, fmt.Errorf("permission: scope not offered for this risk tier")
	}
	switch dec.Scope {
	case ScopeSession:
		g.mu.Lock()
		g.session[req.ToolID] = true
		g.mu.Unlock()
	case ScopePersistent:
		if err := g.store.Grant(req.ToolID); err != nil {
			return false, err
		}
	}
	return true, nil
}

func contains(ss []Scope, s Scope) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
