package graphcache

// SystemMetadataStage has one goroutine owner. Commit publishes all bytes in
// one transition after caller-owned persistence; Abort leaves the prior image.
// Resolve it before invoking another SystemMetadata method on the same handle.
type SystemMetadataStage struct {
	metadata *SystemMetadata
	next     SystemMetadataRecord
	resolved bool
}

func (s *SystemMetadataStage) Commit() {
	if s == nil || s.resolved {
		return
	}
	s.metadata.current = s.next
	s.resolved = true
	s.metadata.mu.Unlock()
}

func (s *SystemMetadataStage) Abort() {
	if s == nil || s.resolved {
		return
	}
	s.resolved = true
	s.metadata.mu.Unlock()
}
