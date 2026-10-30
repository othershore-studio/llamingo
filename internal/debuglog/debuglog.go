// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package debuglog writes the raw request/response payloads exchanged with the
// model backend (LLAMA), the image server (FORGE), and the tools between them
// (TOOL) into a per-conversation logs/ folder, when debug mode is on. It turns
// questions like "which checkpoint did Forge actually load?" or "did two
// requests overlap?" into files you can read after the fact.
//
// A Sink carried in the context says debug is on and where to write; with no
// Sink — debug off — every call is a no-op. Logging is best-effort: a write
// error never fails the turn. Each payload lands at
//
//	<Dir>/<CHANNEL>-<KIND>-<id>.json
//
// where a request and its matching response share one time-ordered id, so the
// files for one exchange sort next to each other and the whole folder sorts in
// event order.
package debuglog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Channels and kinds — the fixed vocabulary of a log filename.
const (
	ChannelLlama = "LLAMA"
	ChannelForge = "FORGE"
	ChannelTool  = "TOOL"

	KindRequest  = "REQUEST"
	KindResponse = "RESPONSE"
)

// Sink is the destination for one conversation's debug payloads: its logs/ dir.
// It lives inside the conversation's own folder, so deleting the chat removes
// its logs too.
type Sink struct {
	Dir string
}

type ctxKey struct{}

// With attaches a sink so Record, called deeper in the turn, writes payloads.
func With(ctx context.Context, s *Sink) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func from(ctx context.Context) *Sink {
	s, _ := ctx.Value(ctxKey{}).(*Sink)
	return s
}

// Enabled reports whether debug logging is on for this context.
func Enabled(ctx context.Context) bool { return from(ctx) != nil }

// NewID returns a time-ordered, unique id to share between a request and its
// matching response. The millisecond timestamp makes files sort in event order;
// the random suffix keeps two exchanges in the same millisecond distinct.
func NewID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405.000") + "-" + hex.EncodeToString(b[:])
}

type record struct {
	Time    string `json:"time"` // actual write time (a response's differs from its id's time)
	Channel string `json:"channel"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Payload any    `json:"payload"`
}

// Record writes one payload as <Dir>/<CHANNEL>-<KIND>-<id>.json when debug is on,
// and otherwise does nothing. channel is one of the Channel* constants, kind one
// of the Kind* constants, and id an id from NewID (shared by a request and its
// response). Errors are swallowed — debug logging must never break a turn — so
// callers pass payloads free of bulk data (e.g. strip base64 image bytes first).
func Record(ctx context.Context, channel, kind, id string, payload any) {
	s := from(ctx)
	if s == nil {
		return
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return
	}
	b, err := json.MarshalIndent(record{
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
		Channel: channel,
		Kind:    kind,
		ID:      id,
		Payload: payload,
	}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.Dir, channel+"-"+kind+"-"+id+".json"), b, 0o644)
}
