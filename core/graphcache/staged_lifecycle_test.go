package graphcache

import (
	"sync"
	"testing"
	"time"
)

func TestStagedMutationLifecycleAbortPanicReleasesOwnedLocks(t *testing.T) {
	for _, tt := range []struct {
		name         string
		searchLocked bool
	}{
		{name: "edge"},
		{name: "vertex and add", searchLocked: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewGraphCacheWithStaging[string, string](time.Hour)
			locks := []*sync.RWMutex{&c.mu, c.publicationGate}
			if tt.searchLocked {
				locks = append(locks, &c.searchCommitMu)
			}
			for _, lock := range locks {
				lock.Lock()
			}

			var owner stagedMutationLifecycle
			var recovered any
			releases := 0
			const failure = "injected rollback failure"
			func() {
				defer func() { recovered = recover() }()
				owner.abort(func() {
					if !owner.closed {
						t.Error("rollback started before ownership closed")
					}
					for i, lock := range locks {
						if lock.TryLock() {
							lock.Unlock()
							t.Errorf("visibility lock %d released before rollback finished", i)
						}
					}
					panic(failure)
				}, func() {
					releases++
					c.releaseStagedMutation(tt.searchLocked)
				})
			}()

			if recovered != failure || !owner.closed || releases != 1 {
				t.Errorf("panic=%v closed=%v releases=%d", recovered, owner.closed, releases)
			}
			for i, lock := range locks {
				if !lock.TryLock() {
					t.Errorf("rollback panic left visibility lock %d held", i)
					continue
				}
				lock.Unlock()
			}
		})
	}
}
