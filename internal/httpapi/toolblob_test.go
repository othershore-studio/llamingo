// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import "testing"

func TestStripLeadingToolBlob(t *testing.T) {
	// A ReAct blob (with an escaped-JSON action_input) before the real answer.
	blob := `{
  "action": "generate_image",
  "action_input": "{ \"prompt\": \"a black cat\" }",
  "thought": "I will draw a black cat."
}

![generated image](attachments/x.png)

Here is an image of a black cat for you!`
	want := "![generated image](attachments/x.png)\n\nHere is an image of a black cat for you!"
	if got := stripLeadingToolBlob(blob); got != want {
		t.Errorf("stripped = %q,\nwant %q", got, want)
	}

	// Ordinary prose is untouched.
	plain := "Here is your answer."
	if got := stripLeadingToolBlob(plain); got != plain {
		t.Errorf("plain content altered: %q", got)
	}

	// A leading JSON object the user actually wanted is not a tool blob → kept.
	data := "{\"name\": \"Ada\", \"age\": 30}\nmore"
	if got := stripLeadingToolBlob(data); got != data {
		t.Errorf("non-tool JSON altered: %q", got)
	}
}
