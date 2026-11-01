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

// The active connection's sampling (temperature, min_p) rides on the
// /v1/chat/completions body, and is omitted when unset so the server/model
// default applies.
func TestChatStreamSendsSampling(t *testing.T) {
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
	msg := backend.ChatRequest{Messages: []backend.Message{{Role: backend.RoleUser, Content: "hi"}}}

	// Set → present on the wire.
	temp, minP := 0.2, 0.05
	be.SetSampling(&temp, &minP)
	if err := be.ChatStream(context.Background(), msg, drain); err != nil {
		t.Fatal(err)
	}
	if gotBody["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want 0.2", gotBody["temperature"])
	}
	if gotBody["min_p"] != 0.05 {
		t.Errorf("min_p = %v, want 0.05", gotBody["min_p"])
	}

	// Cleared → omitted, so the server/model default stands.
	be.SetSampling(nil, nil)
	if err := be.ChatStream(context.Background(), msg, drain); err != nil {
		t.Fatal(err)
	}
	if _, present := gotBody["temperature"]; present {
		t.Error("temperature should be omitted when sampling is unset")
	}
	if _, present := gotBody["min_p"]; present {
		t.Error("min_p should be omitted when sampling is unset")
	}
}
