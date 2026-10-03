package graphcache

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// EnableSystemMetadata reserves one complete internal image independently of
// client graph capacity. Call it during construction, before sharing the cache.
// The returned handle is the only write/read boundary for this image; a graph
// vertex with the same string key remains a distinct client data value.
// Authentication, durability and serving freshness belong to the caller.
func (c *GraphCache[S, T]) EnableSystemMetadata(key string, maxBytes int) (*SystemMetadata, error) {
	if !strings.HasPrefix(key, "sys:") || len(key) <= len("sys:") || len(key) > 256 || !utf8.ValidString(key) ||
		maxBytes <= 0 || maxBytes > MaxSystemMetadataBytes {
		return nil, ErrSystemMetadataInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.systemMetadata != nil {
		return nil, errors.New("graphcache: system metadata already reserved")
	}
	c.systemMetadata = &SystemMetadata{key: key, maxBytes: maxBytes}
	return c.systemMetadata, nil
}

// SnapshotSystemMetadata is the explicitly classified internal recovery image.
// Ordinary SnapshotGraph/SnapshotReplication never include these bytes. The
// caller must separately authenticate any private recovery/peer transport.
func (c *GraphCache[S, T]) SnapshotSystemMetadata() (string, SystemMetadataRecord, bool) {
	c.mu.RLock()
	m := c.systemMetadata
	c.mu.RUnlock()
	if m == nil {
		return "", SystemMetadataRecord{}, false
	}
	return m.Key(), m.Snapshot(), true
}
