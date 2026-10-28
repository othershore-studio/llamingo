// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package contextbudget

import (
	"context"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// Segment is a named group of messages (system, instructions, memory, tools,
// conversation). Increment #1 uses system + conversation; later increments add
// the rest. The slice order is the order rendered.
type Segment struct {
	Name     string
	Messages []backend.Message
}

// Builder assembles a prompt from segments and meters it. Every model call
// routes through one Builder so the budget never drifts.
type Builder struct {
	be backend.Backend
}

// New returns a Builder over the given backend.
func New(be backend.Backend) *Builder { return &Builder{be: be} }

// Messages flattens the segments into one message list, in order.
func (b *Builder) Messages(segments []Segment) []backend.Message {
	var all []backend.Message
	for _, s := range segments {
		all = append(all, s.Messages...)
	}
	return all
}

// Estimate computes a pre-flight Budget: per-segment content tokens, the real
// rendered-prompt total, and the template overhead (total minus the summed
// segment content). It sends no completion. The authoritative count is folded
// in later from a response's usage (see Budget.Authoritative).
func (b *Builder) Estimate(ctx context.Context, segments []Segment) (Budget, error) {
	var counts []SegmentCount
	sum := 0
	for _, s := range segments {
		n, err := b.be.Tokenize(ctx, segmentContent(s))
		if err != nil {
			return Budget{}, err
		}
		counts = append(counts, SegmentCount{Name: s.Name, Tokens: n})
		sum += n
	}

	prompt, err := b.be.ApplyTemplate(ctx, b.Messages(segments))
	if err != nil {
		return Budget{}, err
	}
	total, err := b.be.Tokenize(ctx, prompt)
	if err != nil {
		return Budget{}, err
	}
	overhead := total - sum
	if overhead < 0 {
		overhead = 0
	}

	props, err := b.be.Props(ctx)
	if err != nil {
		return Budget{}, err
	}

	return Budget{
		NCtx:     props.NCtx,
		Estimate: total,
		Segments: counts,
		Overhead: overhead,
	}, nil
}

func segmentContent(s Segment) string {
	var b strings.Builder
	for i, m := range s.Messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.Content)
	}
	return b.String()
}
