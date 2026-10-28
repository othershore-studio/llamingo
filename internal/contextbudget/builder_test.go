// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package contextbudget_test

import (
	"context"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
)

func TestEstimateBreakdown(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	b := contextbudget.New(backend.New(srv.URL, backend.WithHTTPClient(srv.Client())))

	const systemText = "You are a helpful local assistant."
	segments := []contextbudget.Segment{
		{Name: "system", Messages: []backend.Message{{Role: backend.RoleSystem, Content: systemText}}},
		{Name: "conversation", Messages: []backend.Message{
			{Role: backend.RoleUser, Content: "Explain the context budget in one sentence."},
		}},
	}

	bud, err := b.Estimate(context.Background(), segments)
	if err != nil {
		t.Fatal(err)
	}

	if bud.NCtx != lcpptest.NCtx {
		t.Errorf("NCtx = %d, want %d", bud.NCtx, lcpptest.NCtx)
	}
	if len(bud.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(bud.Segments))
	}
	if want := lcpptest.ApproxTokens(systemText); bud.Segments[0].Tokens != want {
		t.Errorf("system tokens = %d, want %d", bud.Segments[0].Tokens, want)
	}

	// The breakdown is honest: per-segment content + overhead sum to the real
	// rendered-prompt total.
	sum := bud.Overhead
	for _, s := range bud.Segments {
		sum += s.Tokens
	}
	if sum != bud.Estimate {
		t.Errorf("segments+overhead = %d, want Estimate %d", sum, bud.Estimate)
	}
	// The chat template contributes real, attributed overhead.
	if bud.Overhead <= 0 {
		t.Errorf("Overhead = %d, want > 0 (template markers)", bud.Overhead)
	}
	// With no response yet, Used falls back to the estimate.
	if bud.Used() != bud.Estimate {
		t.Errorf("Used() = %d, want Estimate %d", bud.Used(), bud.Estimate)
	}
}

func TestLevel(t *testing.T) {
	cases := []struct {
		used int
		want contextbudget.Level
	}{
		{1000, contextbudget.LevelHealthy},
		{6500, contextbudget.LevelNear},
		{7800, contextbudget.LevelFull},
	}
	for _, c := range cases {
		b := contextbudget.Budget{NCtx: lcpptest.NCtx, Authoritative: c.used}
		if got := b.Level(); got != c.want {
			t.Errorf("used %d (frac %.2f): Level = %q, want %q", c.used, b.Fraction(), got, c.want)
		}
	}
}
