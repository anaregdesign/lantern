package service

// withCommittedView holds the server publication cut while a caller copies a
// composite observation. The callback must finish its reads before returning;
// retaining a backend, Store, or log pointer is not a committed view. A failed
// local/relay publication, indeterminate receipt commit, or incomplete
// Snapshot install must not be reported as a healthy graph or receipt read.
//
// This protects server-owned observations only. Direct GraphCache and receipt
// Store reads bypass the service fault check. A healthy private receipt commit
// releases their staged locks after the matching log entry is installed, but
// these error-free Core reads cannot report an indeterminate WAL outcome.
func (s *LanternService) withCommittedView(capture func() error) error {
	s.replicationCutMu.RLock()
	defer s.replicationCutMu.RUnlock()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
		return publicationGapError()
	}
	return capture()
}
