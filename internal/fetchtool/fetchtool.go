// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package fetchtool is the native web-fetch tool: a safe wrapper over net/http
// that lets the model read an http(s) URL. The transport is three lines; the
// tool is the policy around it — a scheme allowlist, a dial-time guard that
// refuses any non-public IP (on the original request and every redirect hop, so
// it defeats redirect-into-internal and DNS rebinding), size and time caps, an
// honest User-Agent, and the permission gate bound PER HOST so a grant means
// "trust this site", never "fetch anything forever". See docs/tools.md.
//
// This is the safe transport only. Readability extraction, markdown conversion,
// and content-type routing (bulk bytes out-of-band to attachments/) come next;
// for now the tool returns the body text as served, capped.
package fetchtool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/permission"
)

// ToolName is the fetch tool's advertised name.
const ToolName = "fetch"

const (
	defaultMaxBytes = 512 << 10 // 512 KiB of body read into context by default
	hardMaxBytes    = 4 << 20   // ceiling even if the model asks for more
	defaultTimeout  = 30 * time.Second
	maxRedirects    = 5
)

// defaultUserAgent is honest: it names the client and links the project, so a
// site operator sees exactly what is calling. A user may have a legitimate
// reason to present a browser UA instead; that is their transparent choice,
// exposed as a setting (WithUserAgent), not a default we make for them.
const defaultUserAgent = "llaminGO (+https://github.com/othershore-studio/llamingo)"

// Tool is the native web-fetch tool. It gates each new host through the gate.
type Tool struct {
	gate      *permission.Gate
	client    *http.Client
	userAgent string
	maxBytes  int64
}

// Option configures a Tool.
type Option func(*Tool)

// WithUserAgent overrides the default (honest) User-Agent. Empty is ignored.
func WithUserAgent(ua string) Option {
	return func(t *Tool) {
		if strings.TrimSpace(ua) != "" {
			t.userAgent = ua
		}
	}
}

// WithMaxBytes overrides the default body cap (clamped to hardMaxBytes per call).
func WithMaxBytes(n int64) Option {
	return func(t *Tool) {
		if n > 0 {
			t.maxBytes = n
		}
	}
}

// WithHTTPClient replaces the SSRF-guarded client. Tests inject one pointed at a
// local httptest server, which the guard would otherwise, correctly, refuse.
func WithHTTPClient(c *http.Client) Option {
	return func(t *Tool) {
		if c != nil {
			t.client = c
		}
	}
}

// NewTool returns a web-fetch tool whose default client is SSRF-guarded: it
// refuses to connect to any non-public address, caps redirects, and times out.
func NewTool(gate *permission.Gate, opts ...Option) *Tool {
	t := &Tool{
		gate:      gate,
		userAgent: defaultUserAgent,
		maxBytes:  defaultMaxBytes,
	}
	t.client = &http.Client{
		Timeout:   defaultTimeout,
		Transport: &http.Transport{DialContext: guardedDial},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Tools advertises the fetch tool to the model.
func (t *Tool) Tools() []backend.ToolDef {
	return []backend.ToolDef{{
		Name: ToolName,
		Description: "Fetch an http(s) web page or file and return its text. Use it to read a URL " +
			"the user gives you, or one you need to consult (docs, an article). Returns the page " +
			"text, truncated if large. http/https only; the user approves each new site, so prefer " +
			"one focused fetch over many.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"url":{"type":"string","description":"The http(s) URL to fetch."},` +
			`"max_bytes":{"type":"integer","description":"Optional cap on how many bytes of the body to read."}` +
			`},"required":["url"]}`),
	}}
}

// Call fetches a URL after the scheme check and the per-host gate.
func (t *Tool) Call(ctx context.Context, name, arguments string) (string, error) {
	if name != ToolName {
		return "", fmt.Errorf("fetchtool: unknown tool %q", name)
	}
	var a struct {
		URL      string `json:"url"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return "", fmt.Errorf("fetchtool: arguments: %w", err)
	}
	u, err := url.Parse(strings.TrimSpace(a.URL))
	if err != nil {
		return "", fmt.Errorf("fetchtool: invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("fetchtool: only http and https URLs are allowed (got %q)", u.Scheme)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("fetchtool: url has no host")
	}

	// Gate PER HOST: the grant binds to the host, so "allow example.com" is smooth
	// for many pages there, while a new destination re-prompts and shows the URL —
	// the user vetting each new site is the anti-injection / anti-exfiltration check.
	allow, err := t.gate.Allow(ctx, permission.Request{
		ToolID: "native:fetch:" + strings.ToLower(u.Hostname()),
		Label:  "fetch from " + u.Hostname(),
		Detail: u.String(),
		Risk:   permission.RiskLow, // read-only, but gated because it reaches the open net
	})
	if err != nil {
		return "", err
	}
	if !allow {
		// A denial is a result, not an error (like memtool): tell the model plainly.
		return fmt.Sprintf("The user declined fetching %s.", u.Hostname()), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("fetchtool: build request: %w", err)
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en;q=0.9,*;q=0.5")

	resp, err := t.client.Do(req)
	if err != nil {
		// Surface a blocked-address refusal and any transport error loudly.
		return "", fmt.Errorf("fetchtool: %w", err)
	}
	defer resp.Body.Close()

	limit := t.maxBytes
	if a.MaxBytes > 0 {
		limit = a.MaxBytes
	}
	if limit > hardMaxBytes {
		limit = hardMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("fetchtool: read body: %w", err)
	}
	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}

	final := resp.Request.URL.String() // post-redirect
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetchtool: %s returned HTTP %d %s", final, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	// Honest header, then the body text. (Readability + content-type routing next.)
	trunc := ""
	if truncated {
		trunc = " (truncated)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Fetched %s\nStatus: %d %s\nContent-Type: %s\nBytes: %d%s\n\n",
		final, resp.StatusCode, http.StatusText(resp.StatusCode),
		resp.Header.Get("Content-Type"), len(body), trunc)
	b.Write(body)
	return b.String(), nil
}

// guardedDial is the SSRF guard: it resolves the address and refuses to connect
// to any non-public IP. It runs for the original request and for every redirect
// hop, after DNS resolution, so it blocks redirect-into-internal and DNS
// rebinding alike — validating the resolved IP, never the hostname string. It
// then dials a validated IP directly, closing the resolve-to-dial rebinding gap.
func guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	// Reject if ANY resolved address is non-public (a conservative stance against
	// a resolver that returns a mix to slip one past).
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("refusing to connect to non-public address %s (%s)", ip, host)
		}
	}
	var d net.Dialer
	var firstErr error
	for _, ip := range ips {
		conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		firstErr = derr
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("no address for %q", host)
}

// isPublicIP reports whether ip is a routable public address — not loopback,
// private (RFC1918 / ULA), link-local (including the 169.254.169.254 cloud
// metadata endpoint), multicast, or unspecified.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	return true
}
