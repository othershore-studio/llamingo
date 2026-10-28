// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backend_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
)

// TestSmoke_LiveServer runs against a real llama-server when LLAMINGO_SMOKE_URL
// is set (e.g. a dev endpoint); it is skipped in CI and by default, so no host
// ever leaks into the repo. An optional LLAMINGO_SMOKE_TOKEN covers an
// auth-fronted proxy.
func TestSmoke_LiveServer(t *testing.T) {
	url := os.Getenv("LLAMINGO_SMOKE_URL")
	if url == "" {
		t.Skip("set LLAMINGO_SMOKE_URL to a running llama-server to run the live smoke test")
	}

	var opts []backend.Option
	if tok := os.Getenv("LLAMINGO_SMOKE_TOKEN"); tok != "" {
		opts = append(opts, backend.WithToken(tok))
	}
	be := backend.New(url, opts...)
	ctx := context.Background()

	props, err := be.Props(ctx)
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	if props.NCtx <= 0 {
		t.Fatalf("NCtx = %d, want > 0", props.NCtx)
	}
	t.Logf("model %q, n_ctx %d", props.ModelID, props.NCtx)

	segments := []contextbudget.Segment{{
		Name:     "conversation",
		Messages: []backend.Message{{Role: backend.RoleUser, Content: "Reply with the single word: pong"}},
	}}
	bld := contextbudget.New(be)
	bud, err := bld.Estimate(ctx, segments)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if bud.Estimate <= 0 {
		t.Fatalf("pre-flight estimate = %d, want > 0", bud.Estimate)
	}

	var reply strings.Builder
	err = be.ChatStream(ctx, backend.ChatRequest{Messages: bld.Messages(segments)}, func(d backend.Delta) error {
		reply.WriteString(d.Content)
		if d.Usage != nil {
			bud.Authoritative = d.Usage.PromptTokens
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if strings.TrimSpace(reply.String()) == "" {
		t.Fatal("empty reply")
	}
	if bud.Authoritative <= 0 {
		t.Fatal("no authoritative usage reported by the server")
	}
	t.Logf("reply %q; budget %d/%d [%s]", strings.TrimSpace(reply.String()), bud.Used(), bud.NCtx, bud.Level())
}
