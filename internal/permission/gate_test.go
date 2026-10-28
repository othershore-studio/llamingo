// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package permission_test

import (
	"context"
	"testing"

	"github.com/othershore-studio/llamingo/internal/permission"
)

type fakePrompter struct {
	calls       int
	lastAllowed []permission.Scope
	decision    permission.Decision
}

func (p *fakePrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	p.calls++
	p.lastAllowed = allowed
	return p.decision, nil
}

func TestOnceRepromptsEachTime(t *testing.T) {
	p := &fakePrompter{decision: permission.Decision{Allow: true, Scope: permission.ScopeOnce}}
	g := permission.New(p, permission.NewMemoryGrantStore())
	req := permission.Request{ToolID: "t", Risk: permission.RiskLow}
	for i := 0; i < 2; i++ {
		if ok, err := g.Allow(context.Background(), req); err != nil || !ok {
			t.Fatalf("Allow #%d = %v, %v", i, ok, err)
		}
	}
	if p.calls != 2 {
		t.Errorf("prompts = %d, want 2 (once-scope reprompts each time)", p.calls)
	}
}

func TestSessionGrantStopsReprompting(t *testing.T) {
	p := &fakePrompter{decision: permission.Decision{Allow: true, Scope: permission.ScopeSession}}
	g := permission.New(p, permission.NewMemoryGrantStore())
	req := permission.Request{ToolID: "t", Risk: permission.RiskLow}
	for i := 0; i < 3; i++ {
		if ok, err := g.Allow(context.Background(), req); err != nil || !ok {
			t.Fatalf("Allow #%d = %v, %v", i, ok, err)
		}
	}
	if p.calls != 1 {
		t.Errorf("prompts = %d, want 1 (session grant)", p.calls)
	}
}

func TestPersistentSurvivesANewGate(t *testing.T) {
	store := permission.NewMemoryGrantStore()
	req := permission.Request{ToolID: "t", Risk: permission.RiskLow}

	p1 := &fakePrompter{decision: permission.Decision{Allow: true, Scope: permission.ScopePersistent}}
	if ok, err := permission.New(p1, store).Allow(context.Background(), req); err != nil || !ok {
		t.Fatal(err)
	}
	if p1.calls != 1 {
		t.Errorf("first gate prompts = %d, want 1", p1.calls)
	}

	// A fresh gate sharing the store must not prompt.
	p2 := &fakePrompter{decision: permission.Decision{Allow: false}}
	if ok, err := permission.New(p2, store).Allow(context.Background(), req); err != nil || !ok {
		t.Fatalf("persisted grant: %v, %v", ok, err)
	}
	if p2.calls != 0 {
		t.Errorf("second gate prompts = %d, want 0 (grant was persisted)", p2.calls)
	}
}

func TestDenyDoesNotGrant(t *testing.T) {
	p := &fakePrompter{decision: permission.Decision{Allow: false}}
	g := permission.New(p, permission.NewMemoryGrantStore())
	req := permission.Request{ToolID: "t", Risk: permission.RiskLow}
	if ok, _ := g.Allow(context.Background(), req); ok {
		t.Fatal("deny should not allow")
	}
	if ok, _ := g.Allow(context.Background(), req); ok {
		t.Fatal("still denied on the second ask")
	}
	if p.calls != 2 {
		t.Errorf("prompts = %d, want 2 (deny does not record anything)", p.calls)
	}
}

func TestHighRiskOffersOnlyOnce(t *testing.T) {
	p := &fakePrompter{decision: permission.Decision{Allow: true, Scope: permission.ScopeOnce}}
	g := permission.New(p, permission.NewMemoryGrantStore())
	req := permission.Request{ToolID: "danger", Risk: permission.RiskHigh}
	if ok, err := g.Allow(context.Background(), req); err != nil || !ok {
		t.Fatal(err)
	}
	if len(p.lastAllowed) != 1 || p.lastAllowed[0] != permission.ScopeOnce {
		t.Errorf("high-risk offered %v, want [ScopeOnce]", p.lastAllowed)
	}
}

func TestScopeNotOfferedIsError(t *testing.T) {
	// A prompter trying to grant persistent for a high-risk action.
	p := &fakePrompter{decision: permission.Decision{Allow: true, Scope: permission.ScopePersistent}}
	g := permission.New(p, permission.NewMemoryGrantStore())
	req := permission.Request{ToolID: "danger", Risk: permission.RiskHigh}
	if _, err := g.Allow(context.Background(), req); err == nil {
		t.Fatal("want an error when granting a scope the risk tier does not offer")
	}
}
