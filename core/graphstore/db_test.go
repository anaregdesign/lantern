package graphstore

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestDBOwnershipAndManifest(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "graph")
	if _, err := OpenExisting(ctx, dir, hlc.NodeID{}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	db, err := Open(ctx, dir, hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	id := db.NodeID()
	if id == (hlc.NodeID{}) {
		t.Fatal("empty node ID")
	}
	if _, err := Open(ctx, dir, id); !errors.Is(err, tarp.ErrLocked) {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := id
	wrong[0] ^= 1
	if _, err := OpenExisting(ctx, dir, wrong); !errors.Is(err, ErrNodeMismatch) {
		t.Fatal(err)
	}
	db, err = OpenExisting(ctx, dir, hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	if db.NodeID() != id {
		t.Fatal("node changed on restart")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*tarp.DB) error
	}{
		{"missing head", func(db *tarp.DB) error { return db.Delete([]byte(manifestKey)) }},
		{"bad manifest", func(db *tarp.DB) error { return db.Put([]byte(manifestKey), []byte("other")) }},
		{"missing generation", func(db *tarp.DB) error { return db.Delete(generationMarker(1)) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "store")
			db, err := Open(ctx, dir, id)
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := tarp.OpenExisting(ctx, dir, storageOptions())
			if err != nil {
				t.Fatal(err)
			}
			if err = test.mutate(raw); err != nil {
				t.Fatal(err)
			}
			if err = raw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = Open(ctx, dir, id); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("Open = %v", err)
			}
		})
	}
}

func TestDBUpdateSerializesReadModifyWrite(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "store"), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	outcome := errors.New("declined")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			err := db.Update(func(id uint64, s *tarp.Snapshot, b *tarp.WriteBatch) error {
				key := append(ServicePrefix(id), 'c')
				prev, err := s.Get(key)
				var n byte
				if err == nil {
					n = prev[0]
				} else if !errors.Is(err, tarp.ErrNotFound) {
					return err
				}
				b.Put(key, []byte{n + 1})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := db.Update(func(id uint64, s *tarp.Snapshot, b *tarp.WriteBatch) error {
		b.Put(append(ServicePrefix(id), 'c'), []byte{0})
		return outcome
	}); err != outcome {
		t.Fatal(err)
	}
	v, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	result, err := v.Snapshot.Get(append(ServicePrefix(v.Generation), 'c'))
	if err != nil || v.Revision != 8 || len(result) != 1 || result[0] != 8 {
		t.Fatalf("revision %d counter %x error %v", v.Revision, result, err)
	}
}

func TestDBApplyErrorAdmission(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "store"), hlc.NodeID{4})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Update(func(_ uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error { b.Put(nil, nil); return nil })
	if !errors.Is(err, tarp.ErrEmptyKey) {
		t.Fatal(err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if view.Revision != 0 {
		t.Fatal("failed batch advanced revision")
	}
}

func TestDBWriteOutcomes(t *testing.T) {
	for _, test := range []struct {
		outcome tarp.Outcome
		blocked bool
	}{{tarp.NotApplied, false}, {tarp.Committed, true}, {tarp.Unknown, true}} {
		t.Run(string(test.outcome), func(t *testing.T) {
			db := new(DB)
			failure := &tarp.OpError{Operation: "apply", Phase: "publication", Outcome: test.outcome, Err: errors.New("I/O")}
			if err := db.observeWrite(failure); err != failure {
				t.Fatalf("original outcome lost: %v", err)
			}
			err := db.available()
			if errors.Is(err, tarp.ErrUnhealthy) != test.blocked {
				t.Fatal(err)
			}
			var op *tarp.OpError
			if errors.As(err, &op) {
				t.Fatal("previous operation's outcome attributed to a later write")
			}
		})
	}
}

func TestReadCutDuringWritePreparation(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "store"), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Update(func(id uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		key := append(ServicePrefix(id), 7)
		b.Put(key, []byte("pending"))
		return db.WithCommittedSnapshot(func(gen uint64, s *tarp.Snapshot) error {
			if gen != id {
				t.Error("unexpected generation")
			}
			_, err := s.Get(key)
			if !errors.Is(err, tarp.ErrNotFound) {
				t.Fatalf("observed pending data: %v", err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}
