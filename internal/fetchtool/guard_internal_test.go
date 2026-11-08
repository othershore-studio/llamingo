// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fetchtool

import (
	"net"
	"testing"
)

// The SSRF policy is the security spine, so pin it directly: public addresses
// pass, every private / loopback / link-local / metadata / multicast range is
// refused. isPublicIP is what guardedDial trusts to decide whether to connect.
func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"93.184.216.34", true}, // a routable public host
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false}, // cloud metadata endpoint
		{"fe80::1", false},         // link-local
		{"fc00::1", false},         // unique-local
		{"0.0.0.0", false},         // unspecified
		{"224.0.0.1", false},       // multicast
	}
	for _, c := range cases {
		if got := isPublicIP(net.ParseIP(c.ip)); got != c.public {
			t.Errorf("isPublicIP(%s) = %v, want %v", c.ip, got, c.public)
		}
	}
	if isPublicIP(nil) {
		t.Error("isPublicIP(nil) must be false")
	}
}
