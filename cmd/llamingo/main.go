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
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "llamingo:", err)
		os.Exit(1)
	}
}

func run() error {
	url := flag.String("url", env("LLAMINGO_URL", "http://localhost:8080"), "llama-server base URL")
	token := flag.String("token", os.Getenv("LLAMINGO_TOKEN"), "optional bearer token for an auth-fronted proxy")
	system := flag.String("system", "", "optional system prompt")
	msg := flag.String("m", "", "user message (default: read from stdin)")
	flag.Parse()

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

	var opts []backend.Option
	if *token != "" {
		opts = append(opts, backend.WithToken(*token))
	}
	return chat(context.Background(), backend.New(*url, opts...), *url, *system, prompt, os.Stdout, os.Stderr)
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

	err = be.ChatStream(ctx, bld.Messages(segments), func(d backend.Delta) error {
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
