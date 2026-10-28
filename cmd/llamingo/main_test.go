// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
)

func TestChatAgainstFake(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))

	var out, errOut bytes.Buffer
	if err := chat(context.Background(), be, srv.URL, "", "Hi", &out, &errOut); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(out.String()); got != "Hello! How can I help you today?" {
		t.Errorf("reply = %q", got)
	}
	// The budget line reports the authoritative count from the stream's usage.
	if !strings.Contains(errOut.String(), "context 12/8192") {
		t.Errorf("stderr missing authoritative budget line:\n%s", errOut.String())
	}
}

func TestModelName(t *testing.T) {
	cases := map[string]string{
		`models/gemma-3-12b-it-Q4_K_M.gguf`:      "gemma-3-12b-it-Q4_K_M",
		`C:\models\qwen2.5-7b-instruct-q5.gguf`:  "qwen2.5-7b-instruct-q5",
		"":                                       "unknown model",
	}
	for in, want := range cases {
		if got := modelName(in); got != want {
			t.Errorf("modelName(%q) = %q, want %q", in, got, want)
		}
	}
}
