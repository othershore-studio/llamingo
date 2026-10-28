// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import "testing"

// A per-request thinking toggle is offered only when the model's chat template
// actually exposes enable_thinking.
func TestThinkingSupported(t *testing.T) {
	with := "{{ bos_token }}{% if enable_thinking %}{{ '<think>' }}{% endif %}{% for m in messages %}x{% endfor %}"
	if !thinkingSupported(with) {
		t.Error("want supported when the template references enable_thinking")
	}
	without := "{{ bos_token }}{% for m in messages %}x{% endfor %}"
	if thinkingSupported(without) {
		t.Error("want unsupported when the template has no enable_thinking")
	}
}
