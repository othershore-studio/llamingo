// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/store"
	"github.com/othershore-studio/llamingo/internal/taita"
)

// A local client linked to a taita surfaces the taita's chats via /api/server/chats
// — created and read back through one anonymous server session (the P3 mechanism:
// clients see remote). Uses the real taita gateway as the server so the cookie →
// folder session is exercised end to end.
func TestServerChatsProxyRoundTrip(t *testing.T) {
	// The chat list/create endpoints never call the backend, so a bare one is fine.
	be := backend.New("http://127.0.0.1:0")

	// A real taita gateway, and a local sidecar linked to it.
	gw := httptest.NewServer(taita.New(be, t.TempDir(), nil))
	defer gw.Close()
	local := func() *store.Store {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	sidecar := httptest.NewServer(httpapi.New(be, local(), httpapi.WithTaita(gw.URL)))
	defer sidecar.Close()

	// Create a chat "on the server" through the sidecar's proxy.
	resp, err := http.Post(sidecar.URL+"/api/server/chats", "application/json", strings.NewReader(`{"title":"remote one"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create via proxy: status %d", resp.StatusCode)
	}

	// Read it back through the same sidecar (same taita session) — it must be there.
	got, err := http.Get(sidecar.URL + "/api/server/chats")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	var chats []map[string]any
	if err := json.Unmarshal(body, &chats); err != nil {
		t.Fatalf("decode server chats: %v; body=%s", err, body)
	}
	if len(chats) != 1 || chats[0]["title"] != "remote one" {
		t.Fatalf("server chats = %v, want exactly one titled 'remote one'", chats)
	}

	// Open that chat through the proxy — it loads from the same taita session, so
	// the client can route a server chat's turn to taita (the two-worlds model).
	cid, _ := chats[0]["id"].(string)
	oc, err := http.Get(sidecar.URL + "/api/server/conversations/" + cid)
	if err != nil {
		t.Fatal(err)
	}
	oc.Body.Close()
	if oc.StatusCode != http.StatusOK {
		t.Fatalf("open server conversation via proxy: status %d", oc.StatusCode)
	}

	// Rename it through the proxy — the title updates on taita.
	rn, _ := http.NewRequest(http.MethodPut, sidecar.URL+"/api/server/conversations/"+cid, strings.NewReader(`{"title":"renamed"}`))
	rn.Header.Set("Content-Type", "application/json")
	if rnResp, err := http.DefaultClient.Do(rn); err != nil {
		t.Fatal(err)
	} else {
		rnResp.Body.Close()
	}
	gotR, _ := http.Get(sidecar.URL + "/api/server/chats")
	rnBody, _ := io.ReadAll(gotR.Body)
	gotR.Body.Close()
	var chatsR []map[string]any
	_ = json.Unmarshal(rnBody, &chatsR)
	if len(chatsR) != 1 || chatsR[0]["title"] != "renamed" {
		t.Fatalf("after rename, server chats = %v, want one titled 'renamed'", chatsR)
	}

	// Delete it through the proxy — the list is empty again.
	del, _ := http.NewRequest(http.MethodDelete, sidecar.URL+"/api/server/conversations/"+cid, nil)
	if delResp, err := http.DefaultClient.Do(del); err != nil {
		t.Fatal(err)
	} else {
		delResp.Body.Close()
	}
	gotD, _ := http.Get(sidecar.URL + "/api/server/chats")
	delBody, _ := io.ReadAll(gotD.Body)
	gotD.Body.Close()
	var chatsD []map[string]any
	_ = json.Unmarshal(delBody, &chatsD)
	if len(chatsD) != 0 {
		t.Fatalf("after delete, server chats = %v, want empty", chatsD)
	}

	// A local client with NO taita linked has an empty server list.
	solo := httptest.NewServer(httpapi.New(be, local()))
	defer solo.Close()
	r2, err := http.Get(solo.URL + "/api/server/chats")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	b2, _ := io.ReadAll(r2.Body)
	var empty []map[string]any
	_ = json.Unmarshal(b2, &empty)
	if len(empty) != 0 {
		t.Fatalf("unlinked server chats = %v, want empty", empty)
	}
}
