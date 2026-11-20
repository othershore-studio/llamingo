// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import "testing"

// Instructions returns a server's handshake guidance only when it sent some AND
// still has an enabled tool — so empty servers and fully-curated-off ones drop out.
func TestServerRegistryInstructions(t *testing.T) {
	r := NewRegistry(nil)
	r.conns = []*connected{
		{server: Server{Name: "biz"}, tools: []Tool{{Name: "query"}}, instructions: "Scope to the active base; periods are months."},
		{server: Server{Name: "quiet"}, tools: []Tool{{Name: "ping"}}, instructions: ""},   // sent none
		{server: Server{Name: "off"}, tools: []Tool{{Name: "z"}}, instructions: "ignored"}, // every tool disabled
	}
	got := r.Instructions(map[string]bool{"z": true})
	if len(got) != 1 {
		t.Fatalf("Instructions = %+v, want exactly one (biz)", got)
	}
	if got[0].Server != "biz" || got[0].Text != "Scope to the active base; periods are months." {
		t.Errorf("got[0] = %+v", got[0])
	}
}
