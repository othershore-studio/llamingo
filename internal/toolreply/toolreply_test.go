// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package toolreply

import (
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
)

func TestStripImageMarkdown(t *testing.T) {
	// A generated-image link in a reply becomes a neutral marker, so a small model's
	// history can't teach it to emit `![](attachments/…)` and confabulate a file name.
	in := "Here is your cat!\n\n![generated image](attachments/cb597f9a51e7a205.png)"
	want := "Here is your cat!\n\n[generated an image]"
	if got := StripImageMarkdown(in); got != want {
		t.Errorf("stripped = %q,\nwant %q", got, want)
	}

	// Multiple images (local + remote) all replaced; surrounding text preserved.
	multi := "![a](attachments/1.png) and ![b](https://host/2.png) done"
	if got := StripImageMarkdown(multi); got != "[generated an image] and [generated an image] done" {
		t.Errorf("multi = %q", got)
	}

	// Plain prose (no image) is untouched.
	plain := "Just text, no image here."
	if got := StripImageMarkdown(plain); got != plain {
		t.Errorf("plain content altered: %q", got)
	}
}

func TestReActToolCall(t *testing.T) {
	// Gemma's shape: action_input is a JSON-encoded string of the args (double-encoded).
	name, args, ok := ReActToolCall(`{"action":"generate_image","action_input":"{ \"prompt\": \"a black cat\" }"}`)
	if !ok || name != "generate_image" || args != `{ "prompt": "a black cat" }` {
		t.Fatalf("string form: ok=%v name=%q args=%q", ok, name, args)
	}

	// action_input as a plain object is used as-is.
	name, args, ok = ReActToolCall(`{"action":"echo","action_input":{"text":"hi"}}`)
	if !ok || name != "echo" || args != `{"text":"hi"}` {
		t.Fatalf("object form: ok=%v name=%q args=%q", ok, name, args)
	}

	// Ordinary prose and non-ReAct JSON are not tool calls.
	if _, _, ok := ReActToolCall("Here is your answer."); ok {
		t.Error("prose parsed as a tool call")
	}
	if _, _, ok := ReActToolCall(`{"name":"Ada","age":30}`); ok {
		t.Error("non-ReAct JSON parsed as a tool call")
	}
}

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
	if got := StripLeadingToolBlob(blob); got != want {
		t.Errorf("stripped = %q,\nwant %q", got, want)
	}

	// Ordinary prose is untouched.
	plain := "Here is your answer."
	if got := StripLeadingToolBlob(plain); got != plain {
		t.Errorf("plain content altered: %q", got)
	}

	// A leading JSON object the user actually wanted is not a tool blob → kept.
	data := "{\"name\": \"Ada\", \"age\": 30}\nmore"
	if got := StripLeadingToolBlob(data); got != data {
		t.Errorf("non-tool JSON altered: %q", got)
	}

	// Gemma's real shape: unescaped inner quotes in action_input (invalid JSON), yet the
	// object's end is still found — the stray braces fall inside string state, so the
	// brace depth stays balanced.
	messy := `{
"action": "generate_image",
"action_input": "{ "prompt": "A fluffy white cat with blue eyes." }",
"thought": "present it to the user"
}

![generated image](attachments/c9.png)

Here is the image.`
	if got := StripLeadingToolBlob(messy); got != "![generated image](attachments/c9.png)\n\nHere is the image." {
		t.Errorf("messy (unescaped-quote) blob not stripped: %q", got)
	}

	// Regression guard: the model buried the image inside the blob and wrote nothing after
	// it — recover the image so the reply is not empty.
	buried := `{
"action": "generate_image",
"action_input": "{ "prompt": "a cat" }",
"result": "![generated image](attachments/z.png)",
"thought": "done"
}`
	if got := StripLeadingToolBlob(buried); got != "![generated image](attachments/z.png)" {
		t.Errorf("buried image not recovered: %q", got)
	}
}

func TestRecoverToolImages(t *testing.T) {
	img := "![generated image](attachments/e2922716.png)"

	// The real failure: the model generated an image, then returned an empty final
	// message. The image lives only in the tool result — recover it.
	silent := []backend.Message{
		{Role: backend.RoleUser, Content: "draw a black cat"},
		{Role: backend.RoleAssistant, ToolCalls: []backend.ToolCall{{ID: "1"}}},
		{Role: backend.RoleTool, Content: "Image generated with DreamShaperXL_Turbo_SFW (seed 42, image/png): " + img, ToolCallID: "1"},
		{Role: backend.RoleAssistant, Content: ""},
	}
	if got := RecoverToolImages("", silent); got != img {
		t.Errorf("silent answer: recovered = %q, want %q", got, img)
	}

	// The model wrote prose but no image — append the tool's image to it.
	prose := "Here is your black cat!"
	if got := RecoverToolImages(prose, silent); got != prose+"\n\n"+img {
		t.Errorf("prose answer: recovered = %q, want %q", got, prose+"\n\n"+img)
	}

	// The model already echoed the image — leave the answer untouched (no dup).
	echoed := prose + "\n\n" + img
	if got := RecoverToolImages(echoed, silent); got != echoed {
		t.Errorf("echoed answer altered: %q", got)
	}

	// A turn with no image-bearing tool result is untouched.
	noImg := []backend.Message{
		{Role: backend.RoleTool, Content: "the time is 04:22", ToolCallID: "1"},
		{Role: backend.RoleAssistant, Content: "It's 4:22."},
	}
	if got := RecoverToolImages("It's 4:22.", noImg); got != "It's 4:22." {
		t.Errorf("no-image turn altered: %q", got)
	}
}

func TestSurfaceImageDenial(t *testing.T) {
	deniedTurn := []backend.Message{
		{Role: backend.RoleAssistant, ToolCalls: []backend.ToolCall{{ID: "1"}}},
		{Role: backend.RoleTool, Content: "Not generated: you declined to create this image. Do not tell the user an image was made."},
		{Role: backend.RoleAssistant, Content: ""},
	}

	// An empty answer after a denial gets a clear, plain-language notice.
	if got := SurfaceImageDenial("", deniedTurn); !strings.Contains(got, "No image was generated") {
		t.Errorf("denied + empty answer = %q, want a denial notice", got)
	}
	// An answer that already explains the decline is left alone (no doubling up).
	explained := "Sorry, I couldn't create that — the request was declined."
	if got := SurfaceImageDenial(explained, deniedTurn); got != explained {
		t.Errorf("already-explained answer altered: %q", got)
	}
	// If an image did come through, there is nothing to surface.
	withImg := "Here you go! ![generated image](attachments/x.png)"
	if got := SurfaceImageDenial(withImg, deniedTurn); got != withImg {
		t.Errorf("answer with an image altered: %q", got)
	}
	// No denial this turn → untouched.
	okTurn := []backend.Message{{Role: backend.RoleTool, Content: "the time is 5pm"}}
	if got := SurfaceImageDenial("", okTurn); got != "" {
		t.Errorf("no-denial turn altered: %q", got)
	}
}
