package dataruntime

import (
	"sync"
	"sync/atomic"
)

// A reader retains its typed before-pointer until comparison. Every stable
// interval has a fresh nonzero-sized certificate, so its identity cannot be
// reused while a reader can still compare it. Certificates are never pooled.
type publicationReadCertificate struct {
	active, faulted bool
}

func (c *publicationReadCertificate) stable() bool { return c != nil && !c.active }

var activePublicationCertificate = &publicationReadCertificate{active: true}

// publicationGate owns every exclusive transition and its certified read cut.
// The numeric generation remains the protected one-shot Search contract.
// Bind the owner once before sharing; never copy or rebind a live gate.
type publicationGate struct {
	mu              sync.RWMutex
	owner           *Executor
	readCertificate atomic.Pointer[publicationReadCertificate]
	generation      uint64 // read under RLock, changed before exclusive Unlock
}

func (g *publicationGate) initialize(owner *Executor) {
	if owner == nil || g.owner != nil || g.readCertificate.Load() != nil {
		panic("publication gate requires one unshared service owner")
	}
	g.owner = owner
	g.readCertificate.Store(&publicationReadCertificate{faulted: owner.publicGraphFaultLocked()})
}

func (g *publicationGate) RLock()         { g.mu.RLock() }
func (g *publicationGate) RUnlock()       { g.mu.RUnlock() }
func (g *publicationGate) TryRLock() bool { return g.mu.TryRLock() }

func (g *publicationGate) Lock() {
	_ = g.LockChecked(nil)
}

// LockChecked validates only after the exclusive gate is owned. Rejection
// leaves both the stable read certificate and numeric generation unchanged.
// check must perform validation only and must not re-enter this executor.
func (g *publicationGate) LockChecked(check func() error) error {
	g.mu.Lock()
	if check != nil {
		if err := check(); err != nil {
			g.mu.Unlock()
			return err
		}
	}
	g.readCertificate.Store(activePublicationCertificate)
	return nil
}

func (g *publicationGate) TryLock() bool {
	if !g.mu.TryLock() {
		return false
	}
	g.readCertificate.Store(activePublicationCertificate)
	return true
}

func (g *publicationGate) Unlock() {
	g.completePublicationLocked()
	g.mu.Unlock()
}

// Caller still owns the exclusive cut. All graph/log/origin/fault work must
// finish before this store, and only mutex handoff may follow it. Storing after
// raw Unlock could overwrite the next writer's active marker with false proof.
func (g *publicationGate) completePublicationLocked() {
	g.generation++
	if g.owner == nil {
		// A narrow zero-value construction has no optimistic proof owner.
		g.readCertificate.Store(nil)
		return
	}
	g.readCertificate.Store(&publicationReadCertificate{faulted: g.owner.publicGraphFaultLocked()})
}

// Caller holds the publication read/write cut, or is the unshared constructor.
// Certificates derive this predicate; fault setters retain sole authority.
func (s *Executor) publicGraphFaultLocked() bool {
	return s.publicationFaultCount != 0 || s.receiptCommitFaulted
}
