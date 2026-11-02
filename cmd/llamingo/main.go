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
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/store"
	"github.com/othershore-studio/llamingo/internal/tui"
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
	msg := flag.String("m", "", "user message (default: read from stdin)")
	dataDir := flag.String("data-dir", env("LLAMINGO_DATA_DIR", defaultDataDir()), "data directory for projects, chats, and config")
	resumeLast := flag.Bool("c", false, "resume this project's most recent session (interactive)")
	flag.Parse()
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	st, err := store.Open(*dataDir)
	if err != nil {
		return err
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
		return tui.Run(be, st, cwd, conn.system, *resumeLast)
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

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
