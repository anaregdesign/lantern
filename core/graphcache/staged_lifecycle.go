package graphcache

// stagedMutationLifecycle owns only the close transition of a single-owner
// graph transaction. Its zero value is open. Typed transactions retain their
// own Commit/Apply preconditions and rollback journals: in particular, an
// empty or rejected Vertex stage may commit without ever becoming applied.
// The owner must not reenter its GraphCache until Commit or Abort returns.
type stagedMutationLifecycle struct {
	closed bool
}

// commit is called after the typed transaction has checked its preconditions.
// Release publishes previously staged state; it must not mutate that state.
func (l *stagedMutationLifecycle) commit(release func()) {
	l.closed = true
	release()
}

// abort is called after the typed transaction has checked ownership. Close
// before rollback, preserving the idempotent Abort contract even if a rollback
// panics during stack unwinding.
func (l *stagedMutationLifecycle) abort(rollback, release func()) {
	l.closed = true
	defer release()
	rollback()
}

// releaseStagedMutation reverses mu -> publicationGate -> searchCommitMu.
// The search lock is held by Vertex and Add stages, including empty stages.
// SystemMetadata owns a different lock and installs its image during Commit;
// it must not use this release-only graph lifecycle.
func (c *GraphCache[S, T]) releaseStagedMutation(searchLocked bool) {
	if searchLocked {
		c.searchCommitMu.Unlock()
	}
	c.publicationGate.Unlock()
	c.mu.Unlock()
}

func (c *GraphCache[S, T]) releaseStagedEdges() {
	c.releaseStagedMutation(false)
}
