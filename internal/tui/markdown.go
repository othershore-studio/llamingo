// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package tui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// md is a shared CommonMark parser. Rendering walks its AST; the parser itself is
// stateless and safe to reuse.
var md = goldmark.New()

// renderMarkdown turns a markdown string into tview-tagged terminal text — headings and
// bold/italic/inline-code as styles, lists with bullets or numbers, fenced code set off
// and dimmed, the rest as flowing paragraphs. It is a small subset renderer, enough for
// chat answers, over goldmark's AST (our chosen markdown lib: pure-Go, MIT). Anything it
// doesn't special-case degrades to its text, so nothing is ever lost — and the raw
// markers (** , ###, backticks) don't show. Styles are tracked on a stack and the whole
// current style is re-emitted at each boundary, since tview tags don't nest.
func renderMarkdown(src string) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	source := []byte(src)
	doc := md.Parser().Parse(text.NewReader(source))
	r := &mdRenderer{source: source, styles: []mdStyle{{}}}
	_ = ast.Walk(doc, r.walk)
	return strings.Trim(r.b.String(), "\n")
}

// mdStyle is a resolved terminal style. tview color tags are absolute, not nested, so a
// span emits the full style of the stack top, never a partial toggle.
type mdStyle struct {
	fg                string
	bold, italic, dim bool
}

func (s mdStyle) tag() string {
	fg := s.fg
	if fg == "" {
		fg = "-"
	}
	flags := ""
	if s.dim {
		flags += "d"
	}
	if s.bold {
		flags += "b"
	}
	if s.italic {
		flags += "i"
	}
	if flags == "" {
		flags = "-"
	}
	return "[" + fg + ":-:" + flags + "]"
}

type mdRenderer struct {
	source []byte
	b      strings.Builder
	styles []mdStyle   // style stack; the top is the current style
	lists  []listState // nested list counters
}

type listState struct {
	ordered bool
	n       int
}

func (r *mdRenderer) top() mdStyle   { return r.styles[len(r.styles)-1] }
func (r *mdRenderer) push(s mdStyle)  { r.styles = append(r.styles, s); r.b.WriteString(s.tag()) }
func (r *mdRenderer) pop() {
	r.styles = r.styles[:len(r.styles)-1]
	r.b.WriteString(r.top().tag())
}

// ensureBlank starts a block on its own line, with a blank line before it unless we are
// at the very start.
func (r *mdRenderer) ensureBlank() {
	out := r.b.String()
	switch {
	case out == "":
		return
	case strings.HasSuffix(out, "\n\n"):
		return
	case strings.HasSuffix(out, "\n"):
		r.b.WriteString("\n")
	default:
		r.b.WriteString("\n\n")
	}
}

func (r *mdRenderer) walk(n ast.Node, entering bool) (ast.WalkStatus, error) {
	switch node := n.(type) {
	case *ast.Heading:
		if entering {
			r.ensureBlank()
			s := r.top()
			s.fg, s.bold = "aqua", true
			r.push(s)
		} else {
			r.pop()
			r.b.WriteString("\n")
		}
	case *ast.Paragraph:
		if !entering {
			r.b.WriteString("\n")
		}
	case *ast.TextBlock: // the content of a tight list item
		if !entering {
			r.b.WriteString("\n")
		}
	case *ast.Emphasis:
		if entering {
			s := r.top()
			if node.Level >= 2 {
				s.bold = true
			} else {
				s.italic = true
			}
			r.push(s)
		} else {
			r.pop()
		}
	case *ast.CodeSpan:
		if entering {
			s := r.top()
			s.fg = "lightcyan"
			r.push(s)
		} else {
			r.pop()
		}
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		if entering {
			r.ensureBlank()
			r.b.WriteString(mdStyle{fg: "lightcyan", dim: true}.tag())
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				r.b.WriteString("  ")
				r.b.WriteString(tview.Escape(string(seg.Value(r.source))))
			}
			r.b.WriteString(r.top().tag())
			if !strings.HasSuffix(r.b.String(), "\n") {
				r.b.WriteString("\n")
			}
			return ast.WalkSkipChildren, nil
		}
	case *ast.List:
		if entering {
			r.ensureBlank()
			r.lists = append(r.lists, listState{ordered: node.IsOrdered(), n: node.Start})
		} else {
			r.lists = r.lists[:len(r.lists)-1]
		}
	case *ast.ListItem:
		if entering {
			r.b.WriteString(strings.Repeat("  ", len(r.lists)-1))
			ls := &r.lists[len(r.lists)-1]
			if ls.ordered {
				if ls.n == 0 {
					ls.n = 1
				}
				fmt.Fprintf(&r.b, "%d. ", ls.n)
				ls.n++
			} else {
				r.b.WriteString("• ")
			}
		}
	case *ast.Text:
		if entering {
			r.b.WriteString(tview.Escape(string(node.Segment.Value(r.source))))
			if node.HardLineBreak() {
				r.b.WriteString("\n")
			} else if node.SoftLineBreak() {
				r.b.WriteString(" ")
			}
		}
	case *ast.AutoLink:
		if entering {
			r.b.WriteString(tview.Escape(string(node.URL(r.source))))
		}
	case *ast.ThematicBreak:
		if entering {
			r.ensureBlank()
			r.b.WriteString("[::d]───[-:-:-]\n")
		}
	}
	return ast.WalkContinue, nil
}
