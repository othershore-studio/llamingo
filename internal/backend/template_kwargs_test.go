// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backend_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// TemplateKwargs ride on the /v1/chat/completions body as chat_template_kwargs
// (llama.cpp's hook for per-request template variables, e.g. enable_thinking),
// and are omitted entirely when empty so the server's own default applies.
func TestChatStreamSendsTemplateKwargs(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = nil
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	drain := func(backend.Delta) error { return nil }

	// With kwargs → present on the wire.
	if err := be.ChatStream(context.Background(), backend.ChatRequest{
		Messages:       []backend.Message{{Role: backend.RoleUser, Content: "hi"}},
		TemplateKwargs: map[string]any{"enable_thinking": false},
	}, drain); err != nil {
		t.Fatal(err)
	}
	kw, ok := gotBody["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("chat_template_kwargs missing from body: %v", gotBody)
	}
	if kw["enable_thinking"] != false {
		t.Errorf("enable_thinking = %v, want false", kw["enable_thinking"])
	}

	// Without kwargs → omitted, so the server default stands.
	if err := be.ChatStream(context.Background(), backend.ChatRequest{
		Messages: []backend.Message{{Role: backend.RoleUser, Content: "hi"}},
	}, drain); err != nil {
		t.Fatal(err)
	}
	if _, present := gotBody["chat_template_kwargs"]; present {
		t.Error("chat_template_kwargs should be omitted when no kwargs are set")
	}
}
