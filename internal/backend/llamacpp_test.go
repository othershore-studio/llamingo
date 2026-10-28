// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backend_test

import (
	"context"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
)

func newClient(t *testing.T) *backend.LlamaCppNative {
	t.Helper()
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	return backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
}

func TestProps(t *testing.T) {
	p, err := newClient(t).Props(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.NCtx != lcpptest.NCtx {
		t.Errorf("NCtx = %d, want %d", p.NCtx, lcpptest.NCtx)
	}
	if !strings.Contains(p.ModelID, "gemma") {
		t.Errorf("ModelID = %q, want it to mention gemma", p.ModelID)
	}
	if p.ChatTemplate == "" {
		t.Error("ChatTemplate is empty")
	}
}

func TestTokenize(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	n, err := newClient(t).Tokenize(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if want := lcpptest.ApproxTokens(text); n != want {
		t.Errorf("Tokenize = %d, want %d", n, want)
	}
}

func TestApplyTemplate(t *testing.T) {
	msgs := []backend.Message{{Role: backend.RoleUser, Content: "Hello"}}
	prompt, err := newClient(t).ApplyTemplate(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Hello") || !strings.Contains(prompt, "<start_of_turn>model") {
		t.Errorf("prompt = %q, missing content or generation prompt", prompt)
	}
}

func TestChatStream(t *testing.T) {
	var content strings.Builder
	var usage *backend.Usage
	sawDone := false

	err := newClient(t).ChatStream(context.Background(),
		[]backend.Message{{Role: backend.RoleUser, Content: "Hi"}},
		func(d backend.Delta) error {
			content.WriteString(d.Content)
			if d.Usage != nil {
				usage = d.Usage
			}
			if d.Done {
				sawDone = true
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got := content.String(); got != "Hello! How can I help you today?" {
		t.Errorf("content = %q", got)
	}
	if !sawDone {
		t.Error("never saw a terminal (Done) delta")
	}
	if usage == nil || usage.PromptTokens != 12 {
		t.Errorf("usage = %+v, want PromptTokens 12", usage)
	}
}
