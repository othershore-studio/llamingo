// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package tui

import (
	"reflect"
	"testing"
)

// kvParse reads space-separated KEY=VALUE pairs; tokens without a real key are skipped.
func TestKVParse(t *testing.T) {
	got := kvParse("API_KEY=abc  REGION=eu-1")
	want := map[string]string{"API_KEY": "abc", "REGION": "eu-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kvParse = %v, want %v", got, want)
	}
	if kvParse("   ") != nil {
		t.Error("blank should parse to nil")
	}
	// a bare token and a leading '=' (empty key) are both skipped
	if got := kvParse("bare =noKey OK=1"); !reflect.DeepEqual(got, map[string]string{"OK": "1"}) {
		t.Errorf("malformed tokens not skipped: %v", got)
	}
}

// kvJoin renders sorted KEY=VALUE pairs and round-trips through kvParse.
func TestKVJoinRoundTrip(t *testing.T) {
	if s := kvJoin(nil); s != "" {
		t.Errorf("nil map: want empty, got %q", s)
	}
	m := map[string]string{"B": "2", "A": "1"}
	if s := kvJoin(m); s != "A=1 B=2" {
		t.Errorf("kvJoin = %q, want sorted \"A=1 B=2\"", s)
	}
	if got := kvParse(kvJoin(m)); !reflect.DeepEqual(got, m) {
		t.Errorf("round-trip = %v, want %v", got, m)
	}
}
