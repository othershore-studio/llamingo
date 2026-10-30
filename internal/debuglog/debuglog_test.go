// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package debuglog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordWritesPairedFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs") // not pre-created: Record must mkdir
	ctx := With(context.Background(), &Sink{Dir: dir})

	id := NewID()
	Record(ctx, ChannelForge, KindRequest, id, map[string]any{"prompt": "a black cat"})
	Record(ctx, ChannelForge, KindResponse, id, map[string]any{"seed": 42})

	// A request and its response share the id, so both files carry it.
	req := filepath.Join(dir, "FORGE-REQUEST-"+id+".json")
	resp := filepath.Join(dir, "FORGE-RESPONSE-"+id+".json")
	for _, p := range []string{req, resp} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s: %v", filepath.Base(p), err)
		}
	}

	// The payload round-trips inside the envelope.
	b, err := os.ReadFile(req)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Time, Channel, Kind, ID string
		Payload                 struct{ Prompt string }
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Channel != "FORGE" || rec.Kind != "REQUEST" || rec.ID != id || rec.Payload.Prompt != "a black cat" {
		t.Errorf("envelope = %+v", rec)
	}
	if rec.Time == "" {
		t.Error("missing write time")
	}
}

func TestRecordDisabledIsNoOp(t *testing.T) {
	// No sink in the context: Record must not write, create, or panic.
	dir := t.TempDir()
	Record(context.Background(), ChannelLlama, KindRequest, NewID(), map[string]any{"x": 1})
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("debug off wrote %d entries, want 0", len(entries))
	}
	if Enabled(context.Background()) {
		t.Error("Enabled should be false with no sink")
	}
}

func TestNewIDTimeOrderedAndUnique(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b {
		t.Errorf("ids not unique: %q", a)
	}
	// The id begins with a compact UTC timestamp, so lexical order ≈ time order.
	if !strings.HasPrefix(a, "20") || !strings.Contains(a, "T") {
		t.Errorf("id not time-prefixed: %q", a)
	}
}
