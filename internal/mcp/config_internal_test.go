// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import "testing"

// expanded() substitutes ${VAR} from the environment in the transport values, and
// must leave the stored config untouched (so a secret never gets written back).
func TestServerConfigExpanded(t *testing.T) {
	t.Setenv("LLAMINGO_TEST_TOKEN", "sekret")
	c := ServerConfig{
		URL:     "https://host/${LLAMINGO_TEST_TOKEN}",
		Command: "${LLAMINGO_TEST_TOKEN}-cmd",
		Args:    []string{"--t=${LLAMINGO_TEST_TOKEN}"},
		Env:     map[string]string{"K": "${LLAMINGO_TEST_TOKEN}"},
		Headers: map[string]string{"Authorization": "Bearer ${LLAMINGO_TEST_TOKEN}"},
	}
	e := c.expanded()
	if e.URL != "https://host/sekret" {
		t.Errorf("URL = %q", e.URL)
	}
	if e.Command != "sekret-cmd" {
		t.Errorf("Command = %q", e.Command)
	}
	if len(e.Args) != 1 || e.Args[0] != "--t=sekret" {
		t.Errorf("Args = %v", e.Args)
	}
	if e.Env["K"] != "sekret" {
		t.Errorf("Env = %v", e.Env)
	}
	if e.Headers["Authorization"] != "Bearer sekret" {
		t.Errorf("Headers = %v", e.Headers)
	}
	if c.Headers["Authorization"] != "Bearer ${LLAMINGO_TEST_TOKEN}" {
		t.Errorf("stored config was mutated: %q", c.Headers["Authorization"])
	}
}
