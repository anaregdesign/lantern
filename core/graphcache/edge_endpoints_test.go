package graphcache

import (
	"errors"
	"testing"
	"time"
)

func TestEdgeEndpointConstraints(t *testing.T) {
	now := time.Now()
	c := NewGraphCache[string, string](time.Hour)
	c.RetainDanglingEdgeHistory()
	c.applicationClock = func() time.Time { return now }
	if err := c.PutVertexWithExpiration("tail", "tail value", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.PutVertexWithExpiration("head", "head value", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, required := range []bool{false, true} {
		for _, head := range []string{"head", "missing"} {
			c.mu.Lock()
			err := c.validateEdgeEndpointsLocked([]EdgeItem[string]{{Tail: "tail", Head: head, RequireLiveEndpoints: required}}, now)
			c.mu.Unlock()
			wantMissing := required && head == "missing"
			if errors.Is(err, ErrEdgeEndpointNotLive) != wantMissing {
				t.Fatalf("required=%v/head=%s: %v", required, head, err)
			}
		}
	}
	c.mu.Lock()
	err := c.validateEdgeEndpointsLocked([]EdgeItem[string]{{Tail: "tail", Head: "head", RequireLiveEndpoints: true}}, now.Add(time.Minute))
	c.mu.Unlock()
	if !errors.Is(err, ErrEdgeEndpointNotLive) {
		t.Fatalf("expired endpoints: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("storage mode changed after writes")
		}
	}()
	c.RetainDanglingEdgeHistory()
}
