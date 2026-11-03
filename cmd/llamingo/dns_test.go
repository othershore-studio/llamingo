// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import "testing"

// dnsHostPort adds :53 to a bare host, leaves an existing port alone, and brackets IPv6.
func TestDNSHostPort(t *testing.T) {
	cases := map[string]string{
		"1.1.1.1":      "1.1.1.1:53",
		"1.1.1.1:5353": "1.1.1.1:5353",
		"10.70.1.1":    "10.70.1.1:53",
		"dns.lan":      "dns.lan:53",
		"::1":          "[::1]:53",
		"[::1]:53":     "[::1]:53",
		"  1.1.1.1 ":   "1.1.1.1:53",
	}
	for in, want := range cases {
		if got := dnsHostPort(in); got != want {
			t.Errorf("dnsHostPort(%q) = %q, want %q", in, got, want)
		}
	}
}
