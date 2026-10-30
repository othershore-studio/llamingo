// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
)

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

	// Regression guard: the model buried the image inside the blob and wrote
	// nothing after it — recover the image so the reply is not empty.
	buried := `{
"action": "generate_image",
"action_input": "{ "prompt": "a cat" }",
"result": "![generated image](attachments/z.png)",
"thought": "done"
}`
	if got := stripLeadingToolBlob(buried); got != "![generated image](attachments/z.png)" {
		t.Errorf("buried image not recovered: %q", got)
	}
}

func TestRecoverToolImages(t *testing.T) {
	img := "![generated image](attachments/e2922716.png)"

	// The real failure: the model generated an image, then returned an empty
	// final message. The image lives only in the tool result — recover it.
	silent := []backend.Message{
		{Role: backend.RoleUser, Content: "draw a black cat"},
		{Role: backend.RoleAssistant, ToolCalls: []backend.ToolCall{{ID: "1"}}},
		{Role: backend.RoleTool, Content: "Image generated with DreamShaperXL_Turbo_SFW (seed 42, image/png): " + img, ToolCallID: "1"},
		{Role: backend.RoleAssistant, Content: ""},
	}
	if got := recoverToolImages("", silent); got != img {
		t.Errorf("silent answer: recovered = %q, want %q", got, img)
	}

	// The model wrote prose but no image — append the tool's image to it.
	prose := "Here is your black cat!"
	if got := recoverToolImages(prose, silent); got != prose+"\n\n"+img {
		t.Errorf("prose answer: recovered = %q, want %q", got, prose+"\n\n"+img)
	}

	// The model already echoed the image — leave the answer untouched (no dup).
	echoed := prose + "\n\n" + img
	if got := recoverToolImages(echoed, silent); got != echoed {
		t.Errorf("echoed answer altered: %q", got)
	}

	// A turn with no image-bearing tool result is untouched.
	noImg := []backend.Message{
		{Role: backend.RoleTool, Content: "the time is 04:22", ToolCallID: "1"},
		{Role: backend.RoleAssistant, Content: "It's 4:22."},
	}
	if got := recoverToolImages("It's 4:22.", noImg); got != "It's 4:22." {
		t.Errorf("no-image turn altered: %q", got)
	}
}
