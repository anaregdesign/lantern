package dataruntime

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
)

func TestTarpRetainedRelayPinsExactPayloadsAcrossEvictionAndReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	r, s := openTarpTestRuntime(t, dir, 2)
	payload := bytes.Repeat([]byte{0x8f}, 128<<10)
	for _, key := range []string{"a", "b"} {
		if _, err := s.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_Bytes{Bytes: payload}}}); err != nil {
			t.Fatal(err)
		}
	}
	checkRefs := func() {
		t.Helper()
		for _, e := range r.log.RetainedEntries() {
			if ref, ok := e.Op.(relayOrigin); !ok || ref.Digest == ([32]byte{}) {
				t.Fatalf("ring retained %T instead of a durable reference", e.Op)
			}
		}
	}
	checkRefs()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, s = openTarpTestRuntime(t, dir, 2)
	checkRefs()
	st, err := r.log.SubscribeStream(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// No subscriber payload has been consumed. Evict the entire original
	// physical suffix and retain its two records through the pin only.
	for _, k := range []string{"c", "d", "e"} {
		if _, err := s.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: k}}); err != nil {
			t.Fatal(err)
		}
	}
	for i, key := range []string{"a", "b"} {
		select {
		case e := <-st.Entries():
			m, ok := graphMutationFromLog(e.Op)
			if !ok {
				t.Fatalf("missing replay %s: %v", key, st.Err())
			}
			rows := m.GetOp().GetReplicatedPutVertices().GetEntries()
			if len(rows) != 1 || e.Seq != uint64(i+1) || rows[0].GetLive().GetKey() != key || !bytes.Equal(rows[0].GetLive().GetBytes(), payload) {
				t.Fatalf("wrong pinned payload at seq %d: %T", e.Seq, m.GetOp().GetOp())
			}
		case <-time.After(time.Second):
			t.Fatal("blocked replay")
		}
	}
	<-st.Done()
	if st.Err() != mutationlog.ErrGapped {
		t.Fatalf("wanted actual post-window gap, got %v", st.Err())
	}
}

func TestTarpRetainedRelayReadErrorFencesServing(t *testing.T) {
	r, s := openTarpTestRuntime(t, filepath.Join(t.TempDir(), "store"), 3)
	if _, err := s.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "original"}}); err != nil {
		t.Fatal(err)
	}
	err := r.GraphStore().Update(func(gen uint64, snap *tarp.Snapshot, b *tarp.WriteBatch) error {
		key := tarpEntryKey(gen, 1)
		raw, err := snap.Get(key)
		if err != nil {
			return err
		}
		raw = bytes.Clone(raw)
		raw[len(raw)-1] ^= 1
		b.Put(key, raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.log.VisitRetained(func(mutationlog.Entry) error { t.Fatal("corrupt entry delivered"); return nil }); err == nil {
		t.Fatal("corruption accepted")
	}
	if _, err = s.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "original"}); err == nil {
		t.Fatal("corrupt history left public reads open")
	}
}
