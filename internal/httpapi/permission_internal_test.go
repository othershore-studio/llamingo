// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/othershore-studio/llamingo/internal/permission"
)

// A resolved POST unblocks the waiting Ask with the user's decision; the prompt
// is emitted on the stream with an id the answer refers back to.
func TestBrokerResolveUnblocksAsk(t *testing.T) {
	b := newBroker()
	p := newSSEPrompter(b)
	p.timeout = time.Second

	idCh := make(chan string, 1)
	ctx := withEmitter(context.Background(), func(event string, data any) {
		if event == "permission" {
			idCh <- data.(map[string]any)["id"].(string)
		}
	})

	done := make(chan permission.Decision, 1)
	go func() {
		dec, _ := p.Ask(ctx, permission.Request{Label: "update memory", Risk: permission.RiskMedium},
			permission.OfferedScopes(permission.RiskMedium))
		done <- dec
	}()

	id := <-idCh
	if id == "" {
		t.Fatal("prompt emitted without an id")
	}
	if !b.resolve(id, permission.Decision{Allow: true, Scope: permission.ScopeSession}) {
		t.Fatal("resolve reported no pending prompt")
	}
	select {
	case dec := <-done:
		if !dec.Allow || dec.Scope != permission.ScopeSession {
			t.Errorf("decision = %+v, want allow/session", dec)
		}
	case <-time.After(time.Second):
		t.Fatal("Ask did not unblock after resolve")
	}
}

// With no answer, Ask auto-denies when the timeout elapses.
func TestAskTimesOutToDeny(t *testing.T) {
	p := newSSEPrompter(newBroker())
	p.timeout = 20 * time.Millisecond
	ctx := withEmitter(context.Background(), func(string, any) {})

	dec, err := p.Ask(ctx, permission.Request{Risk: permission.RiskMedium}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allow {
		t.Error("want deny on timeout")
	}
}

// Without a stream to ask on, Ask denies rather than acting unasked.
func TestAskWithoutStreamDenies(t *testing.T) {
	p := newSSEPrompter(newBroker())
	dec, err := p.Ask(context.Background(), permission.Request{Risk: permission.RiskMedium}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allow {
		t.Error("want deny when there is no stream")
	}
}

// Answering an unknown id is reported, not silently accepted.
func TestResolveUnknownID(t *testing.T) {
	if newBroker().resolve("nope", permission.Decision{Allow: true}) {
		t.Error("resolve of an unknown id should report false")
	}
}
