package graphcache

import (
	"testing"
	"time"
)

func TestSystemMetadataStageAbortAndIndependentGraphProgress(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	m, _ := c.EnableSystemMetadata("sys:security:revision", 64)
	stage, err := m.Prepare([32]byte{}, 1, []byte("pending"))
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Abort()
	graphDone := make(chan error, 1)
	go func() { graphDone <- c.PutVertexWithExpiration("data:x", "client", time.Time{}) }()
	select {
	case err := <-graphDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("system staging blocked the data graph")
	}
	started := make(chan struct{})
	read := make(chan SystemMetadataRecord, 1)
	go func() {
		close(started)
		read <- m.Snapshot()
	}()
	<-started
	select {
	case <-read:
		t.Fatal("system reader bypassed unresolved stage")
	case <-time.After(20 * time.Millisecond):
	}
	stage.Abort()
	stage.Commit() // Resolution cannot publish again.
	select {
	case got := <-read:
		if got.Revision != 0 || len(got.Value) != 0 {
			t.Fatal("abort published partial state")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not release reader")
	}
}

func BenchmarkSystemMetadataStageDataRead(b *testing.B) {
	for _, staged := range []bool{false, true} {
		b.Run(map[bool]string{false: "ordinary", true: "system commit pending"}[staged], func(b *testing.B) {
			c := NewGraphCache[string, string](time.Minute)
			if err := c.PutVertexWithExpiration("key", "value", time.Time{}); err != nil {
				b.Fatal(err)
			}
			if staged {
				metadata, err := c.EnableSystemMetadata("sys:security:revision", 64)
				if err != nil {
					b.Fatal(err)
				}
				stage, err := metadata.Prepare([32]byte{}, 1, []byte("pending"))
				if err != nil {
					b.Fatal(err)
				}
				defer stage.Abort()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				value, found := c.GetVertex("key")
				if !found || value != "value" {
					b.Fatal("system staging changed data visibility")
				}
			}
		})
	}
}
