// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package toolreply shapes a tool-loop turn's final answer for saving: it strips the
// ReAct scratch some small models emit, recovers an image a tool produced when the model
// forgets to show it, and surfaces a declined image rather than a blank reply. Both the
// HTTP adapter and the TUI run a turn through the same functions so the saved answer — and
// any image reference in it — is identical, and an image generated in the terminal shows
// in the desktop too.
package toolreply

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// StripLeadingToolBlob removes a leading ReAct-style tool-call object some small models
// (notably Gemma under a large toolset) emit as text in their final answer —
// {"action":…,"action_input":…,"thought":…} — even when they also made the real native
// call. It is scratch, not answer. Anything that is not clearly a tool-call blob is left
// untouched.
func StripLeadingToolBlob(content string) string {
	t := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(t, "{") {
		return content
	}
	end := jsonObjectEnd(t)
	if end < 0 {
		return content
	}
	blob := t[:end+1]
	if !strings.Contains(blob, "action") || (!strings.Contains(blob, "action_input") && !strings.Contains(blob, "thought")) {
		return content // a leading JSON object, but not a ReAct tool-call blob
	}
	rest := strings.TrimSpace(t[end+1:])
	// The model sometimes buries the image it was handed inside the blob (a "result"
	// field) and writes nothing usable after it; recover the image markdown so it still
	// shows rather than leaving an empty reply.
	if !strings.Contains(rest, "](") {
		if img := firstImageMarkdown(blob); img != "" {
			if rest == "" {
				return img
			}
			return img + "\n\n" + rest
		}
	}
	return rest
}

// SurfaceImageDenial makes a declined (or timed-out) image request legible. When the gate
// denies generate_image, its tool result tells the model not to claim an image was made,
// so a small model often falls silent (or re-narrates) and the reply ends up empty. If the
// answer carries no image but a generate_image was denied this turn, append a plain note so
// the user isn't left with a blank reply.
func SurfaceImageDenial(answer string, msgs []backend.Message) string {
	if imageMarkdownRe.MatchString(answer) {
		return answer // an image came through
	}
	denied := false
	for _, m := range msgs {
		if m.Role == backend.RoleTool && strings.HasPrefix(strings.TrimSpace(m.Content), "Not generated:") {
			denied = true
		}
	}
	if !denied {
		return answer
	}
	if lower := strings.ToLower(answer); strings.Contains(lower, "declin") || strings.Contains(lower, "not generat") {
		return answer // the model already explained it
	}
	const notice = "_No image was generated — the request was declined, or the approval prompt timed out._"
	if strings.TrimSpace(answer) == "" {
		return notice
	}
	return answer + "\n\n" + notice
}

var imageMarkdownRe = regexp.MustCompile(`!\[[^\]]*\]\([^)\s]+\)`)

// StripImageMarkdown replaces image markdown (![alt](url)) destined for the MODEL's
// context with a neutral text marker, so a prior turn's generated-image link doesn't
// teach a small model to emit `![](attachments/…)` itself (it then invents a file name
// instead of calling the image tool). The DISPLAY keeps the real markdown; only the
// model's projected history is rewritten. Both httpapi's messagesFromEntries and the
// TUI's messagesFrom call this, so the two projections stay identical.
func StripImageMarkdown(content string) string {
	return imageMarkdownRe.ReplaceAllString(content, "[generated an image]")
}

// ReActToolCall extracts a tool call a small model wrote as a leading ReAct JSON blob
// ({"action":"<tool>","action_input":<args>}) instead of a native function call — Gemma
// under a large toolset degrades like this, so the native call never fires and the reply
// comes back empty. It returns the tool name and its arguments as a JSON string; ok is
// false when content is not such a blob. action_input may be a JSON object OR a
// JSON-encoded string of one (Gemma double-encodes it); both yield an args JSON string.
// The caller must still check the name is a tool it actually offers before running it.
func ReActToolCall(content string) (name, args string, ok bool) {
	t := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(t, "{") {
		return "", "", false
	}
	end := jsonObjectEnd(t)
	if end < 0 {
		return "", "", false
	}
	var blob struct {
		Action      string          `json:"action"`
		ActionInput json.RawMessage `json:"action_input"`
	}
	if json.Unmarshal([]byte(t[:end+1]), &blob) != nil || blob.Action == "" || len(blob.ActionInput) == 0 {
		return "", "", false
	}
	args = strings.TrimSpace(string(blob.ActionInput))
	// Unwrap the double-encoded form: action_input is a JSON string holding the args JSON.
	if strings.HasPrefix(args, `"`) {
		var inner string
		if json.Unmarshal(blob.ActionInput, &inner) == nil {
			args = strings.TrimSpace(inner)
		}
	}
	return blob.Action, args, true
}

// firstImageMarkdown finds an ![alt](url) image in s, unescaping a JSON-escaped blob first
// so a markdown image buried in a string field is still matched.
func firstImageMarkdown(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\n`, "\n")
	return imageMarkdownRe.FindString(s)
}

// RecoverToolImages makes sure an image produced by a tool this turn still reaches the
// saved reply even when the model echoes nothing (or no image) in its final message. The
// native image tool and MCP image servers return the image as markdown in their tool
// result; the last assistant message is kept as the answer, so a silent model loses the
// image although the file is already on disk. If the answer carries no image of its own,
// surface the most recent one found in the turn's tool results.
func RecoverToolImages(answer string, msgs []backend.Message) string {
	if imageMarkdownRe.MatchString(answer) {
		return answer // the model already showed an image
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != backend.RoleTool {
			continue
		}
		if img := firstImageMarkdown(msgs[i].Content); img != "" {
			if strings.TrimSpace(answer) == "" {
				return img
			}
			return answer + "\n\n" + img
		}
	}
	return answer
}

// jsonObjectEnd returns the index of the brace that closes the one at s[0], string-aware
// (quotes and backslash escapes), or -1 if it is unbalanced.
func jsonObjectEnd(s string) int {
	depth, inStr, esc := 0, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// inside a string literal
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
