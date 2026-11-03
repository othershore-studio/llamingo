// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command llamingo is the minimal CLI shell over the core: it connects to a
// llama-server, meters an honest context budget, and streams one reply. The
// reply goes to stdout; connection, model, and budget lines go to stderr.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/store"
	"github.com/othershore-studio/llamingo/internal/tui"
	"github.com/othershore-studio/llamingo/internal/winsetup"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "llamingo:", err)
		os.Exit(1)
	}
}

func run() error {
	url := flag.String("url", "", "llama-server base URL (default: the active saved connection, else http://localhost:8080)")
	token := flag.String("token", "", "bearer token for an auth-fronted proxy (default: the active connection's)")
	system := flag.String("system", "", "system prompt (default: the active connection's persona)")
	dns := flag.String("dns", "", "DNS server for name resolution, host or host:port (default: LLAMINGO_DNS, else the saved setting; for Android/Termux where Go finds no nameserver)")
	msg := flag.String("m", "", "user message (default: read from stdin)")
	dataDir := flag.String("data-dir", env("LLAMINGO_DATA_DIR", defaultDataDir()), "data directory for projects, chats, and config")
	resumeLast := flag.Bool("c", false, "resume this project's most recent session (interactive)")
	setup := flag.Bool("setup", false, "register llamingo with Windows (PATH + Explorer right-click), then exit")
	flag.Parse()
	if *setup {
		return runSetup()
	}
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	st, err := store.Open(*dataDir)
	if err != nil {
		return err
	}
	if d := effectiveDNS(*dns, st); d != "" {
		setDNSResolver(d) // for Android/Termux where Go's resolver finds no nameserver; inert otherwise
	}
	conn := resolveConnection(st, *url, set["url"], *token, set["token"], *system, set["system"])

	var opts []backend.Option
	if conn.token != "" {
		opts = append(opts, backend.WithToken(conn.token))
	}
	be := backend.New(conn.url, opts...)
	be.SetSampling(conn.temperature, conn.minP) // nil/nil = the server default

	// No message and an interactive terminal: launch the TUI, anchored to the cwd's
	// project. A piped or -m message stays the scriptable one-shot.
	if *msg == "" && term.IsTerminal(int(os.Stdin.Fd())) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return tui.Run(be, st, cwd, conn.system, *resumeLast, setupHint())
	}

	prompt := *msg
	if prompt == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		prompt = strings.TrimSpace(string(b))
	}
	if prompt == "" {
		return fmt.Errorf("no message: pass -m or pipe text on stdin")
	}
	return chat(context.Background(), be, conn.url, conn.system, prompt, os.Stdout, os.Stderr)
}

// connection is the resolved llama-server to talk to for this run.
type connection struct {
	url, token, system string
	temperature, minP  *float64
}

// resolveConnection decides which llama-server to use, sharing the desktop's config.
// Precedence: an explicit -url flag or LLAMINGO_URL wins; otherwise the active saved
// connection (config.json, the one picked in the app); otherwise localhost. When the
// active connection is used, its token, sampling, and persona come along — so `llamingo`
// with no flags is exactly the connection (and persona) you selected in the app. An
// explicit flag always overrides its counterpart.
func resolveConnection(st *store.Store, urlFlag string, urlSet bool, tokenFlag string, tokenSet bool, systemFlag string, systemSet bool) connection {
	cfg, _ := st.Config() // a missing/unreadable config is the zero value: fall back to localhost

	envURL := os.Getenv("LLAMINGO_URL")
	useConn := !urlSet && envURL == "" && cfg.URL != ""

	c := connection{url: urlFlag, token: tokenFlag, system: systemFlag}
	if c.url == "" {
		c.url = envURL
	}
	if useConn {
		c.url = cfg.URL
	}
	if c.url == "" {
		c.url = "http://localhost:8080"
	}

	if c.token == "" {
		c.token = os.Getenv("LLAMINGO_TOKEN")
	}
	if useConn && !tokenSet && os.Getenv("LLAMINGO_TOKEN") == "" {
		c.token = cfg.Token
	}

	if useConn {
		if !systemSet {
			c.system = cfg.SystemPrompt
		}
		c.temperature, c.minP = cfg.Temperature, cfg.MinP
	}
	return c
}

// chat connects, meters the pre-flight budget, streams one reply to out, and
// reports connection + budget on errOut. It is engine-agnostic (takes a
// backend.Backend) so it runs against the fake server in tests.
func chat(ctx context.Context, be backend.Backend, url, system, prompt string, out, errOut io.Writer) error {
	props, err := be.Props(ctx)
	if err != nil {
		return fmt.Errorf("couldn't reach llama-server at %s (is it running? default port is 8080): %w", url, err)
	}
	fmt.Fprintf(errOut, "Connected to %s\nFound %s · %d ctx\n", url, modelName(props.ModelID), props.NCtx)

	var segments []contextbudget.Segment
	if system != "" {
		segments = append(segments, contextbudget.Segment{
			Name:     "system",
			Messages: []backend.Message{{Role: backend.RoleSystem, Content: system}},
		})
	}
	segments = append(segments, contextbudget.Segment{
		Name:     "conversation",
		Messages: []backend.Message{{Role: backend.RoleUser, Content: prompt}},
	})

	bld := contextbudget.New(be)
	bud, err := bld.Estimate(ctx, segments)
	if err != nil {
		return err
	}
	fmt.Fprintf(errOut, "context ~%d/%d [%s]  (%s)\n", bud.Used(), bud.NCtx, bud.Level(), breakdown(bud))

	err = be.ChatStream(ctx, backend.ChatRequest{Messages: bld.Messages(segments)}, func(d backend.Delta) error {
		if d.Content != "" {
			fmt.Fprint(out, d.Content)
		}
		if d.Usage != nil {
			bud.Authoritative = d.Usage.PromptTokens
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	fmt.Fprintf(errOut, "context %d/%d [%s]\n", bud.Used(), bud.NCtx, bud.Level())
	return nil
}

// setupHint is a one-line nudge shown in the TUI when the Windows PATH / right-click
// integration isn't in place (or the binary moved), pointing at `llamingo -setup`. Empty
// off Windows or once it's set up.
func setupHint() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	s, _ := winsetup.Check(exe)
	switch {
	case s.Configured():
		return ""
	case s.NeedsHeal():
		return "[yellow]llamingo moved — run `llamingo -setup` to update the PATH + right-click shortcuts[-]"
	default:
		return "[::d]tip: `llamingo -setup` adds llamingo to your PATH and an Explorer right-click menu[-]"
	}
}

// runSetup registers (or heals) the Windows PATH entry and the Explorer right-click menu —
// user context only, no admin. Idempotent; re-run after moving the binary.
func runSetup() error {
	if runtime.GOOS != "windows" {
		fmt.Println("Setup (PATH + Explorer menu) is Windows-only for now.")
		fmt.Println("On other systems, put the llamingo binary on your PATH yourself")
		fmt.Println("(for example a symlink into ~/.local/bin).")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe = filepath.Clean(exe)
	s, _ := winsetup.Check(exe)
	fmt.Println("llamingo:", exe)
	if s.Configured() {
		fmt.Println("Already set up — your PATH and the right-click menu point here. Nothing to do.")
		return nil
	}
	if s.NeedsHeal() {
		fmt.Println("The right-click menu points at a different binary — it looks like llamingo moved.")
	}
	fmt.Println("This will, in your user context only (no admin, reversible):")
	if !s.OnPath {
		fmt.Println("  - add", filepath.Dir(exe), "to your PATH so `llamingo` runs from anywhere")
	}
	fmt.Println(`  - add an Explorer right-click menu on folders: "Call a llaminGO here" (+ a resume variant)`)
	fmt.Print("Proceed? [y/N] ")
	if !confirmYes() {
		fmt.Println("Cancelled — nothing changed.")
		return nil
	}
	if changed, err := winsetup.ApplyPath(filepath.Dir(exe)); err != nil {
		fmt.Println("  PATH:", err)
	} else if changed {
		fmt.Println("  PATH: added (open a new terminal to pick it up).")
	} else {
		fmt.Println("  PATH: already present.")
	}
	if err := winsetup.ApplyMenu(exe); err != nil {
		fmt.Println("  menu:", err)
	} else {
		fmt.Println(`  menu: installed — right-click a folder and choose "Call a llaminGO here".`)
		fmt.Println(`        On Windows 11 it's in the classic menu: Shift+right-click (or "Show`)
		fmt.Println(`        more options") to see it.`)
	}
	return nil
}

func confirmYes() bool {
	var s string
	_, _ = fmt.Scanln(&s)
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// effectiveDNS resolves the DNS override: the -dns flag, else LLAMINGO_DNS, else the saved
// config setting. Empty means "use the OS resolver" — the default everywhere DNS already works.
func effectiveDNS(flagVal string, st *store.Store) string {
	if flagVal != "" {
		return flagVal
	}
	if e := os.Getenv("LLAMINGO_DNS"); e != "" {
		return e
	}
	cfg, _ := st.Config()
	return cfg.DNS
}

// setDNSResolver points Go's default resolver at an explicit DNS server. On Android/Termux the
// pure-Go resolver finds no nameserver (no usable /etc/resolv.conf), falls back to [::1]:53,
// and every hostname lookup fails; a configured server fixes it. Called only when one is set,
// so it never changes behavior where DNS already works. Set once at startup, before any
// connection, so there's no concurrent access to net.DefaultResolver.
func setDNSResolver(server string) {
	addr := dnsHostPort(server)
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
}

// dnsHostPort appends the DNS port (53) to a bare host, leaving an existing host:port
// untouched. Handles IPv4, IPv6 (bracketed on output), and hostnames.
func dnsHostPort(s string) string {
	s = strings.TrimSpace(s)
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return net.JoinHostPort(s, "53")
}

func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".llamingo")
	}
	return ".llamingo"
}

func modelName(path string) string {
	if path == "" {
		return "unknown model"
	}
	name := path
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(name, ".gguf")
}

func breakdown(b contextbudget.Budget) string {
	parts := make([]string, 0, len(b.Segments)+1)
	for _, s := range b.Segments {
		parts = append(parts, fmt.Sprintf("%s %d", s.Name, s.Tokens))
	}
	parts = append(parts, fmt.Sprintf("overhead %d", b.Overhead))
	return strings.Join(parts, " · ")
}
