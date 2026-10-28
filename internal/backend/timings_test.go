// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backend_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// The server's timings (llama.cpp performance report) arrive on the terminal
// delta alongside usage, so the client can show tokens/sec.
func TestChatStreamParsesTimings(t *testing.T) {
	sse := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}," +
		"\"timings\":{\"prompt_n\":5,\"prompt_per_second\":100.0,\"predicted_n\":2,\"predicted_per_second\":42.5}}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse)
	}))
	defer srv.Close()
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))

	var done backend.Delta
	if err := be.ChatStream(context.Background(), backend.ChatRequest{
		Messages: []backend.Message{{Role: backend.RoleUser, Content: "hi"}},
	}, func(d backend.Delta) error {
		if d.Done {
			done = d
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if done.Timings == nil {
		t.Fatal("no timings on the terminal delta")
	}
	if done.Timings.PredictPerSecond != 42.5 || done.Timings.PredictN != 2 {
		t.Errorf("predict timings = %+v, want 42.5/s over 2 tokens", done.Timings)
	}
	if done.Timings.PromptPerSecond != 100.0 {
		t.Errorf("prompt_per_second = %v, want 100", done.Timings.PromptPerSecond)
	}
}
