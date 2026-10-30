// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import "strings"

// Some small models (notably Gemma under a large toolset) emit a ReAct-style
// tool-call object as text in their final answer — {"action":…,"action_input":…,
// "thought":…} — even when they also made the real native call. It is scratch,
// not answer. stripLeadingToolBlob removes one such leading object so it does not
// clutter the saved reply; the model's reasoning (and the prompt it chose) still
// live in the thinking trace. Anything that is not clearly a tool-call blob is
// left untouched.
func stripLeadingToolBlob(content string) string {
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
	return strings.TrimSpace(t[end+1:])
}

// jsonObjectEnd returns the index of the brace that closes the one at s[0],
// string-aware (quotes and backslash escapes), or -1 if it is unbalanced.
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
