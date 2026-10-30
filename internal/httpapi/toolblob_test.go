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

	// Gemma's real shape: unescaped inner quotes in action_input (invalid JSON),
	// yet the object's end is still found — the stray braces fall inside string
	// state, so the brace depth stays balanced.
	messy := `{
"action": "generate_image",
"action_input": "{ "prompt": "A fluffy white cat with blue eyes." }",
"thought": "present it to the user"
}

![generated image](attachments/c9.png)

Here is the image.`
	if got := stripLeadingToolBlob(messy); got != "![generated image](attachments/c9.png)\n\nHere is the image." {
		t.Errorf("messy (unescaped-quote) blob not stripped: %q", got)
	}
}
