// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package contextbudget is the single place a conversation is assembled into a
// prompt and metered against the model's context window. Honest metering is the
// headline feature; routing every model call through one builder is what keeps
// the number truthful. See docs/architecture.md and docs/ui.md.
package contextbudget

// Level is the ambient health of the budget, driving the status-bar color cue
// (calm -> amber -> red) described in docs/ui.md.
type Level string

const (
	LevelHealthy Level = "healthy"
	LevelNear    Level = "near"
	LevelFull    Level = "full"
)

// SegmentCount attributes content tokens to a named segment of the prompt.
type SegmentCount struct {
	Name   string
	Tokens int
}

// Budget is the honest accounting of a conversation against the context window:
// a pre-flight Estimate (from /apply-template + /tokenize), the Authoritative
// count once a response reports it, a per-segment content breakdown, and the
// template Overhead not attributable to any one segment.
type Budget struct {
	NCtx          int
	Estimate      int
	Authoritative int
	Segments      []SegmentCount
	Overhead      int
}

// Used returns the authoritative count when a response has reported one, else
// the pre-flight estimate.
func (b Budget) Used() int {
	if b.Authoritative > 0 {
		return b.Authoritative
	}
	return b.Estimate
}

// Fraction is Used/NCtx in [0,1]; 0 when the context size is unknown.
func (b Budget) Fraction() float64 {
	if b.NCtx <= 0 {
		return 0
	}
	return float64(b.Used()) / float64(b.NCtx)
}

// Level maps the fill fraction to the ambient color cue: healthy below 0.75,
// near below 0.90, full at or above 0.90.
func (b Budget) Level() Level {
	switch f := b.Fraction(); {
	case f >= 0.90:
		return LevelFull
	case f >= 0.75:
		return LevelNear
	default:
		return LevelHealthy
	}
}
