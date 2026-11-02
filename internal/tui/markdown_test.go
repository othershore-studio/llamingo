// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package tui

import (
	"strings"
	"testing"
)

// renderMarkdown renders the common chat constructs and, crucially, never leaves the raw
// markers on screen (the bug this fixes: ### and ** shown literally in the terminal).
func TestRenderMarkdown(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   []string // substrings that must appear
		absent []string // raw markers that must not
	}{
		{"heading", "### Current Conditions", []string{"Current Conditions", "aqua"}, []string{"###"}},
		{"bold", "**Temperature:** 11C", []string{"Temperature:", "11C"}, []string{"**"}},
		{"italic", "a *soft* word", []string{"soft"}, nil},
		{"bullets", "- one\n- two", []string{"• one", "• two"}, []string{"- one"}},
		{"bullet with bold", "* **Temp:** 11.2C", []string{"• ", "Temp:", "11.2C"}, []string{"**", "* **"}},
		{"ordered", "1. first\n2. second", []string{"1. first", "2. second"}, nil},
		{"inline code", "run `go test` now", []string{"go test", "lightcyan"}, []string{"`"}},
		{"code block", "```\nx := 1\n```", []string{"x := 1"}, []string{"```"}},
		{"paragraph", "Just a sentence.", []string{"Just a sentence."}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderMarkdown(c.in)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) = %q\n missing %q", c.in, got, w)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("render(%q) = %q\n still has raw %q", c.in, got, a)
				}
			}
		})
	}
}

// Bold text keeps the escaped content and carries a bold flag in the tag stream.
func TestRenderMarkdownBoldFlag(t *testing.T) {
	got := renderMarkdown("**loud**")
	if !strings.Contains(got, "loud") {
		t.Fatalf("render = %q, missing text", got)
	}
	if !strings.Contains(got, "b]") {
		t.Errorf("render = %q, expected a bold style tag", got)
	}
}

func TestRenderMarkdownEmpty(t *testing.T) {
	if got := renderMarkdown("   \n  "); got != "" {
		t.Errorf("render(blank) = %q, want empty", got)
	}
}
