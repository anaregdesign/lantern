package graphstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestLayoutScheduleUsesMaterialChangeAndQuiet(t *testing.T) {
	now := time.Unix(100, 0)
	base := layoutSample{generation: 2, revision: 9, records: 100, liveBytes: 150, liveKeys: 24}
	for _, tc := range []struct {
		name string
		cut  layoutSample
		want bool
	}{
		{"reopened existing store", base, false},
		{"small replacement", layoutSample{3, 10, 101, 60, 24}, false},
		{"aborted staging without a commit", layoutSample{2, 9, 180, 150, 24}, false},
		{"few writes", layoutSample{2, 10, 105, 150, 24}, false},
		{"quarter of rows", layoutSample{2, 10, 106, 150, 24}, true},
		{"one segment of growth", layoutSample{2, 10, 102, 250, 24}, true},
		{"published generation", layoutSample{4, 10, 101, 110, 24}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLayoutSchedule(base, now, 2*time.Minute, 100)
			if s.due(now.Add(time.Minute), tc.cut) || s.due(now.Add(2*time.Minute), tc.cut) {
				t.Fatal("maintenance was admitted during the quiet window")
			}
			if got := s.due(now.Add(3*time.Minute), tc.cut); got != tc.want {
				t.Fatalf("idle admission %t, want %t", got, tc.want)
			}
			s.retryAfter = now.Add(6 * time.Minute)
			if s.due(now.Add(4*time.Minute), tc.cut) {
				t.Fatal("retry ignored backoff")
			}
		})
	}
}

func TestOrderedMaintenanceDefersForViewsAndReclaimsReplacedGeneration(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := Open(ctx, dir, hlc.NodeID{7})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Unix(100, 0)
	s := newLayoutSchedule(db.layoutSample(), now, time.Minute, 1)
	old, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	g, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompactByKey(ctx); !errors.Is(err, tarp.ErrBusy) {
		t.Fatalf("admitted while staging: %v", err)
	}
	if err := g.Apply(func(gen uint64, w Writer) error {
		for _, key := range []byte{'z', 'a', 'm'} {
			w.Put(append(ServicePrefix(gen), key), []byte{key})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.Publish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	committed := db.layoutSample()
	db.maintainKeyOrder(ctx, now, &s)
	db.maintainKeyOrder(ctx, now.Add(time.Minute), &s)
	if s.base.generation == g.ID() {
		t.Fatal("ordered while replaced generation was pinned")
	}
	if _, err := db.CompactByKey(ctx); !errors.Is(err, tarp.ErrBusy) {
		t.Fatalf("pinned generation: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	db.maintainKeyOrder(ctx, now.Add(2*time.Minute), &s)
	if s.base.generation == g.ID() {
		t.Fatal("ordered before the replaced generation was reclaimed")
	}
	if err := db.Reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	db.maintainKeyOrder(ctx, now.Add(3*time.Minute), &s)
	db.maintainKeyOrder(ctx, now.Add(4*time.Minute), &s)
	if s.base.generation != g.ID() || s.base.revision != committed.revision {
		t.Fatal("idle ordered pass did not cover the published generation")
	}
	if s.due(now.Add(5*time.Minute), db.layoutSample()) {
		t.Fatal("unchanged store scheduled another ordered checkpoint")
	}
	// Inspect the digest around an explicit pass on this small database.
	digest := func(db *DB) [32]byte {
		t.Helper()
		v, err := db.Read()
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		hash, err := StateDigest(ctx, v.Generation, v.Snapshot)
		if err != nil || v.Generation != committed.generation || v.Revision != committed.revision {
			t.Fatalf("invalid compacted view: %v, %v", v, err)
		}
		return hash
	}
	before := digest(db)
	if _, err := db.CompactByKey(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.CompactByKey(stopped); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled compaction: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExisting(ctx, dir, hlc.NodeID{7})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := digest(reopened); got != before {
		t.Fatal("ordered checkpoint changed the graph or service rows on reopen")
	}
}

func TestKeyOrderRejectsMissingActiveMarker(t *testing.T) {
	db, err := Open(t.Context(), t.TempDir(), hlc.NodeID{7})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.raw.Delete(generationMarker(db.head.active)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompactByKey(t.Context()); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("compaction accepted an invalid graph manifest: %v", err)
	}
}

func TestServingMaintenanceInterruptsAnOrderedPass(t *testing.T) {
	for _, operation := range []string{"backup", "generation", "close"} {
		t.Run(operation, func(t *testing.T) {
			db, err := Open(t.Context(), t.TempDir(), hlc.NodeID{7})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			task := &layoutTask{cancel: cancel, done: make(chan struct{})}
			db.ordering = task
			closed := make(chan error, 1)
			switch operation {
			case "backup":
				_, err = db.Backup(t.Context(), filepath.Join(t.TempDir(), "image"), false, func(uint64, uint64, *tarp.Snapshot) error { return nil })
			case "generation":
				var g *Generation
				g, err = db.BeginGeneration()
				if err == nil {
					g.Abort()
				}
			case "close":
				go func() { closed <- db.Close() }()
			}
			if err != nil {
				t.Error(err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				t.Error("maintenance did not yield to " + operation)
			}
			db.mu.Lock()
			db.ordering = nil
			close(task.done)
			db.mu.Unlock()
			cancel()
			if operation == "close" {
				if err = <-closed; err != nil {
					t.Fatal(err)
				}
			} else if err = db.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
