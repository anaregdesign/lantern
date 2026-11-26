package graphstore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestGenerationPublishAndReclaim(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "store")
	db, err := Open(ctx, dir, hlc.NodeID{42})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	key := func(id uint64) []byte { return append(ServicePrefix(id), 1) }
	if err = db.Update(func(id uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error { b.Put(key(id), []byte("old")); return nil }); err != nil {
		t.Fatal(err)
	}
	old, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	g, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginGeneration(); !errors.Is(err, ErrGenerationBusy) {
		t.Fatal(err)
	}
	if err = g.Apply(func(id uint64, w Writer) error {
		w.Put(key(id), []byte("new"))
		w.Delete(key(old.Generation))
		return nil
	}); err == nil {
		t.Fatal("accepted foreign key")
	}
	if err = g.Apply(func(id uint64, w Writer) error { w.Put(key(id), []byte("new")); return nil }); err != nil {
		t.Fatal(err)
	}
	reject := errors.New("footer invalid")
	if err = g.Publish(ctx, func(uint64, uint64, *tarp.Snapshot) error { return reject }); err != reject {
		t.Fatal(err)
	}
	if err = g.Publish(ctx, func(active, staged uint64, s *tarp.Snapshot) error {
		if active != old.Generation || staged != g.ID() {
			t.Fatal("incorrect verification cut")
		}
		for id, want := range map[uint64]string{active: "old", staged: "new"} {
			b, err := s.Get(key(id))
			if err != nil || string(b) != want {
				t.Fatalf("verify id %d=%q, %v", id, b, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = g.Apply(func(uint64, Writer) error { return nil }); !errors.Is(err, ErrGenerationClosed) {
		t.Fatal(err)
	}
	if err = db.Reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := db.raw.Get(key(old.Generation)); err != nil || string(got) != "old" {
		t.Fatalf("reclaimed pinned generation: %q %v", got, err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.raw.Get(key(old.Generation)); !errors.Is(err, tarp.ErrNotFound) {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenExisting(ctx, dir, hlc.NodeID{42})
	if err != nil {
		t.Fatal(err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	got, err := view.Snapshot.Get(key(view.Generation))
	if err != nil || string(got) != "new" || view.Revision != 2 || view.Generation != g.ID() {
		t.Fatalf("reopened %+v %q %v", view, got, err)
	}
}

func TestGenerationInterruptedAndStale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	ctx := t.Context()
	db, err := Open(ctx, dir, hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Apply(func(id uint64, w Writer) error { w.Put(append(ServicePrefix(id), 1), []byte("partial")); return nil }); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenExisting(ctx, dir, hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fresh, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID() <= stage.ID() {
		t.Fatal("reused incomplete generation")
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	if view.Generation != 1 || view.Revision != 0 {
		t.Fatalf("partial published %+v", view)
	}
	view.Close()
	if err = db.Reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.raw.Get(append(ServicePrefix(stage.ID()), 1)); !errors.Is(err, tarp.ErrNotFound) {
		t.Fatal(err)
	}
	if err = db.Update(func(id uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		b.Put(append(ServicePrefix(id), 2), []byte("accepted"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = fresh.Publish(ctx, nil); !errors.Is(err, ErrGenerationChanged) {
		t.Fatal(err)
	}
	fresh.Abort()
	if err = fresh.Publish(ctx, nil); !errors.Is(err, ErrGenerationClosed) {
		t.Fatal(err)
	}
	v, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.Generation != 1 || v.Revision != 1 {
		t.Fatal("stale generation published")
	}
}

func TestGenerationCrashCutover(t *testing.T) {
	const childDir = "LANTERN_TEST_CRASH_GENERATION_DIRECTORY"
	const childPhase = "LANTERN_TEST_CRASH_GENERATION_PHASE"
	if dir := os.Getenv(childDir); dir != "" {
		db, err := OpenExisting(t.Context(), dir, hlc.NodeID{9})
		if err != nil {
			t.Fatal(err)
		}
		g, err := db.BeginGeneration()
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range []byte{'v', 'f'} {
			if err = g.Apply(func(id uint64, w Writer) error { w.Put(append(ServicePrefix(id), part), []byte("new")); return nil }); err != nil {
				t.Fatal(err)
			}
		}
		if os.Getenv(childPhase) == "after" {
			if err = g.Publish(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(83) // Lose all process memory. Deliberately bypass DB.Close.
	}
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "graph")
			db, err := Open(t.Context(), dir, hlc.NodeID{9})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Update(func(id uint64, _ *tarp.Snapshot, w *tarp.WriteBatch) error {
				for _, part := range []byte{'v', 'f'} {
					w.Put(append(ServicePrefix(id), part), []byte("old"))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestGenerationCrashCutover$")
			cmd.Env = append(os.Environ(), childDir+"="+dir, childPhase+"="+phase)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 83 {
				t.Fatalf("unexpected child result: %s %v", out, err)
			}
			db, err = OpenExisting(t.Context(), dir, hlc.NodeID{9})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			view, err := db.Read()
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			want := "old"
			if phase == "after" {
				want = "new"
			}
			for _, part := range []byte{'v', 'f'} {
				got, err := view.Snapshot.Get(append(ServicePrefix(view.Generation), part))
				if err != nil || string(got) != want {
					t.Fatalf("cut %s = %q, %v", phase, got, err)
				}
			}
		})
	}
}
