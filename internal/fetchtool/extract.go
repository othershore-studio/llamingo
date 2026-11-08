// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fetchtool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// renderContent turns a fetched body into text fit for the model, routed by
// content type: HTML is reduced to its readable article; JSON and text pass
// through; anything binary is described, never dumped (its bytes would flood or
// corrupt the context — the out-of-band rule). The body is already size-capped.
func renderContent(contentType string, body []byte) string {
	switch mt := mediaType(contentType); {
	case mt == "text/html", mt == "application/xhtml+xml":
		text := readableHTML(body)
		if len(strings.TrimSpace(text)) < 200 {
			// Thin result: likely a client-rendered shell. Fall back to the
			// structured data such pages ship in their initial HTML.
			if h := hydrationJSON(body); h != "" {
				text = strings.TrimSpace(text + "\n\n" + h)
			}
		}
		if strings.TrimSpace(text) == "" {
			return "(no readable text could be extracted from the page)"
		}
		return text
	case mt == "application/json", strings.HasSuffix(mt, "+json"):
		return string(body)
	case strings.HasPrefix(mt, "text/"):
		return string(body)
	default:
		return fmt.Sprintf("(%s content, %d bytes — not shown as text)", mt, len(body))
	}
}

// mediaType returns the lowercased media type without parameters ("text/html").
// An absent type is treated as HTML, which is what such servers usually serve.
func mediaType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct == "" {
		return "text/html"
	}
	return ct
}

// readableHTML extracts a page's main readable text. It drops non-content
// elements (script/style/nav/header/footer/aside/form/…), prefers <article> or
// <main> when present, and renders the remaining block structure as light
// markdown-ish text (headings, paragraphs, list items). A lean pass over
// x/net/html — not a full Readability port, just enough to keep raw markup out
// of the model's context. (Upgrade path: go-shiori/go-readability.)
func readableHTML(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var b strings.Builder
	renderNode(&b, pickRoot(doc))
	return normalizeText(b.String())
}

// dropTags are elements whose subtrees are never content.
var dropTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Head: true,
	atom.Nav: true, atom.Header: true, atom.Footer: true, atom.Aside: true,
	atom.Form: true, atom.Button: true, atom.Svg: true, atom.Iframe: true,
	atom.Template: true,
}

func renderNode(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		if dropTags[n.DataAtom] {
			return
		}
		switch n.DataAtom {
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			b.WriteString("\n\n" + strings.Repeat("#", headingLevel(n.DataAtom)) + " ")
			renderChildren(b, n)
			b.WriteString("\n\n")
			return
		case atom.Li:
			b.WriteString("\n- ")
			renderChildren(b, n)
			return
		case atom.Br:
			b.WriteString("\n")
			return
		case atom.P, atom.Div, atom.Section, atom.Article, atom.Tr,
			atom.Blockquote, atom.Pre, atom.Ul, atom.Ol, atom.Table:
			b.WriteString("\n\n")
			renderChildren(b, n)
			b.WriteString("\n\n")
			return
		}
	}
	renderChildren(b, n)
}

func renderChildren(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderNode(b, c)
	}
}

// headingLevel maps an h1–h6 atom to its level. The atom values are not
// sequential, so switch rather than subtract.
func headingLevel(a atom.Atom) int {
	switch a {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	default:
		return 6
	}
}

// pickRoot chooses the content subtree: the first <article>, else <main>, else
// <body>, else the whole document.
func pickRoot(doc *html.Node) *html.Node {
	if a := findFirst(doc, atom.Article); a != nil {
		return a
	}
	if m := findFirst(doc, atom.Main); m != nil {
		return m
	}
	if body := findFirst(doc, atom.Body); body != nil {
		return body
	}
	return doc
}

func findFirst(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := findFirst(c, a); r != nil {
			return r
		}
	}
	return nil
}

// normalizeText collapses intra-line whitespace and runs of blank lines, so the
// block structure survives but the markup's incidental spacing does not.
func normalizeText(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.Join(strings.Fields(ln), " ")
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

// hydrationJSON pulls the structured data a JS-rendered page ships in its initial
// HTML — <script type="application/ld+json"> and the Next.js / Nuxt hydration
// blobs — so an SPA shell still yields something without executing JS.
func hydrationJSON(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Script {
			id := attrOf(n, "id")
			if strings.ToLower(attrOf(n, "type")) == "application/ld+json" ||
				id == "__NEXT_DATA__" || id == "__NUXT_DATA__" {
				if txt := strings.TrimSpace(textOf(n)); txt != "" {
					out = append(out, compactJSON(txt))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(out) == 0 {
		return ""
	}
	return "Structured data embedded in the page:\n" + strings.Join(out, "\n")
}

func attrOf(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

// compactJSON strips insignificant whitespace when txt is valid JSON, so a
// pretty-printed blob doesn't waste context; otherwise it returns txt unchanged.
func compactJSON(txt string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(txt)); err != nil {
		return txt
	}
	return buf.String()
}
