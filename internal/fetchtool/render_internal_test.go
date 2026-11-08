// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fetchtool

import (
	"strings"
	"testing"
)

func TestReadableHTMLDropsBoilerplate(t *testing.T) {
	const page = `<html><head><title>T</title><style>.x{color:red}</style></head>
<body>
<nav>Home About Contact</nav>
<script>trackingEvil()</script>
<article><h1>The Headline</h1><p>First paragraph of the real content.</p>
<p>Second paragraph here.</p></article>
<footer>Copyright junk</footer>
</body></html>`
	got := renderContent("text/html; charset=utf-8", []byte(page))
	for _, want := range []string{"The Headline", "First paragraph of the real content.", "Second paragraph here."} {
		if !strings.Contains(got, want) {
			t.Errorf("extracted text missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"trackingEvil", "Home About Contact", "Copyright junk", "<p>", "<script", "color:red"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("extracted text should not contain %q:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "# The Headline") {
		t.Errorf("heading not rendered as markdown:\n%s", got)
	}
}

func TestRenderContentJSONPassthrough(t *testing.T) {
	got := renderContent("application/json", []byte(`{"a":1,"b":[2,3]}`))
	if !strings.Contains(got, `"a":1`) {
		t.Errorf("JSON not passed through: %q", got)
	}
}

func TestRenderContentBinaryNotDumped(t *testing.T) {
	got := renderContent("image/png", []byte("RAWIMAGEBYTES\x89PNG\r\n"))
	if !strings.Contains(got, "not shown") {
		t.Errorf("binary content should be described, got: %q", got)
	}
	if strings.Contains(got, "RAWIMAGEBYTES") {
		t.Errorf("binary bytes must not be dumped into the result: %q", got)
	}
}

func TestHydrationFallbackForSPAShell(t *testing.T) {
	const shell = `<html><body><div id="app"></div>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"title":"Hydrated Title"}}}</script>
</body></html>`
	got := renderContent("text/html", []byte(shell))
	if !strings.Contains(got, "Hydrated Title") {
		t.Errorf("hydration fallback should surface the embedded data:\n%s", got)
	}
}
