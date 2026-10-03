package graphcache

import (
	"errors"
	"testing"
	"time"
)

func TestEnableSystemMetadata(t *testing.T) {
	for _, key := range []string{"", "sys:", "data:x", "sys:\xff"} {
		c := NewGraphCache[string, string](time.Minute)
		if _, err := c.EnableSystemMetadata(key, 64); !errors.Is(err, ErrSystemMetadataInvalid) {
			t.Fatalf("accepted invalid key %q: %v", key, err)
		}
	}
	c := NewGraphCache[string, string](time.Minute)
	if _, _, reserved := c.SnapshotSystemMetadata(); reserved {
		t.Fatal("ordinary graph acquired system state")
	}
	if _, err := c.EnableSystemMetadata("sys:security:revision", MaxSystemMetadataBytes+1); err == nil {
		t.Fatal("accepted unbounded reserve")
	}
	m, err := c.EnableSystemMetadata("sys:security:revision", 64)
	if err != nil || m.Key() != "sys:security:revision" {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := c.EnableSystemMetadata("sys:other", 64); err == nil {
		t.Fatal("replaced reserved namespace")
	}
	if key, record, reserved := c.SnapshotSystemMetadata(); !reserved || key != m.Key() || record.Revision != 0 {
		t.Fatal("internal snapshot did not classify empty reserve")
	}
}
