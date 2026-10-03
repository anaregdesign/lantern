package graphcache

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/search"
)

func TestSystemMetadataOwnershipCASAndRecovery(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	m, err := c.EnableSystemMetadata("sys:security:revision", 64)
	if err != nil {
		t.Fatal(err)
	}
	value := []byte("initial")
	stage, err := m.Prepare([32]byte{}, 1, value)
	if err != nil {
		t.Fatal(err)
	}
	value[0] = 'X'
	stage.Commit()
	got := m.Snapshot()
	if !bytes.Equal(got.Value, []byte("initial")) || got.Digest != sha256.Sum256([]byte("initial")) || got.Revision != 1 {
		t.Fatalf("aliased or incomplete state: %+v", got)
	}
	got.Value[0] = 'Y'
	if string(m.Snapshot().Value) != "initial" {
		t.Fatal("snapshot changed owned state")
	}
	for _, seq := range []uint64{0, 1, 3, ^uint64(0)} {
		if _, err := m.Prepare(got.Digest, seq, []byte("next")); err == nil {
			t.Fatalf("accepted sequence %d", seq)
		}
	}
	if _, err := m.Prepare([32]byte{1}, 2, []byte("next")); !errors.Is(err, ErrSystemMetadataConflict) {
		t.Fatal("accepted wrong digest")
	}
	if _, err := m.Prepare(got.Digest, 2, bytes.Repeat([]byte("x"), 65)); !errors.Is(err, ErrSystemMetadataInvalid) {
		t.Fatal("exceeded independent byte budget")
	}
	if err := m.InstallRecovered(7, []byte("checkpoint")); !errors.Is(err, ErrSystemMetadataConflict) {
		t.Fatal("recovery replaced live state")
	}
	fresh := NewGraphCache[string, string](time.Minute)
	restored, _ := fresh.EnableSystemMetadata("sys:security:revision", 64)
	if err := restored.InstallRecovered(7, []byte("checkpoint")); err != nil {
		t.Fatal(err)
	}
	if restored.Snapshot().Revision != 7 {
		t.Fatal("lost recovered revision")
	}
}

func TestSystemMetadataExcludedFromGraphAndGC(t *testing.T) {
	c := NewGraphCache[string, string](time.Nanosecond)
	c.EnablePrefixIndex(func(key string) string { return key })
	c.EnableSearchIndex(func(key, value string) search.Document { return search.Text(value) },
		func(a, b string) int { return bytes.Compare([]byte(a), []byte(b)) })
	m, _ := c.EnableSystemMetadata("sys:security:revision", 64)
	stage, err := m.Prepare([32]byte{}, 1, []byte("secretword"))
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
	if _, found := c.GetVertex(m.Key()); found {
		t.Fatal("system image surfaced as a graph vertex")
	}
	if err := c.PutVertexWithExpiration(m.Key(), "clientword", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if value, found := c.GetVertex(m.Key()); !found || value != "clientword" {
		t.Fatal("same-named graph value did not remain separate")
	}
	c.DeleteVertex(m.Key())
	c.flush()
	if c.VertexCount() != 0 || c.EdgeCount() != 0 || len(c.SnapshotGraph().Vertices) != 0 ||
		len(c.SnapshotReplication().Graph.Vertices) != 0 || c.CountByPrefix("") != 0 ||
		len(c.SearchVertices("secretword", 10, "")) != 0 || string(m.Snapshot().Value) != "secretword" {
		t.Fatal("system metadata leaked into graph storage, statistics, or TTL lifecycle")
	}
}

func BenchmarkSystemMetadataPrepare(b *testing.B) {
	for _, size := range []int{4 << 10, 4 << 20} {
		b.Run(map[int]string{4 << 10: "4KiB", 4 << 20: "4MiB"}[size], func(b *testing.B) {
			c := NewGraphCache[string, string](time.Minute)
			metadata, err := c.EnableSystemMetadata("sys:security:revision", size)
			if err != nil {
				b.Fatal(err)
			}
			value := bytes.Repeat([]byte("x"), size)
			expected, next := [32]byte{}, sha256.Sum256(value)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stage, err := metadata.Prepare(expected, uint64(i)+1, value)
				if err != nil {
					b.Fatal(err)
				}
				stage.Commit()
				expected = next
			}
		})
	}
}

func TestSystemMetadataCheckpointMonotonicCAS(t *testing.T) {
	graph := NewGraphCache[string, string](time.Minute)
	metadata, err := graph.EnableSystemMetadata("sys:security", 1024)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := metadata.PrepareCheckpoint([32]byte{}, 10, []byte("verified checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
	before := metadata.Snapshot()
	for _, revision := range []uint64{9, 10} {
		if _, err = metadata.PrepareCheckpoint(before.Digest, revision, []byte("old")); !errors.Is(err, ErrSystemMetadataConflict) {
			t.Fatal("checkpoint rolled back", err)
		}
	}
	if _, err = metadata.PrepareCheckpoint([32]byte{1}, 11, []byte("new")); !errors.Is(err, ErrSystemMetadataConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	if _, err = metadata.Prepare(before.Digest, 12, []byte("ordinary gap")); !errors.Is(err, ErrSystemMetadataConflict) {
		t.Fatal("ordinary transaction skipped sequence", err)
	}
	stage, err = metadata.PrepareCheckpoint(before.Digest, 100, []byte("new checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	stage.Abort()
	if metadata.Snapshot().Revision != 10 {
		t.Fatal("aborted checkpoint installed")
	}
}
