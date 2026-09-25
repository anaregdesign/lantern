package service

import (
	"context"
	"sync"
)

// receiptOperationAdmission admits any number of healthy public receipt
// operations while giving baseline installation exclusive ownership. Once an
// installer claims exclusivity, new readers fail closed and existing readers
// drain; this avoids writer starvation without misclassifying reader overlap
// as recovery.
type receiptOperationAdmission struct {
	mu        sync.Mutex
	readers   int
	exclusive bool
	changed   chan struct{}
}

func newReceiptOperationAdmission() *receiptOperationAdmission {
	return &receiptOperationAdmission{changed: make(chan struct{})}
}

func (a *receiptOperationAdmission) tryAcquireShared() (func(), bool) {
	if a == nil {
		return nil, false
	}
	a.mu.Lock()
	if a.exclusive {
		a.mu.Unlock()
		return nil, false
	}
	a.readers++
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		if a.readers <= 0 {
			a.mu.Unlock()
			panic("service: receipt shared admission released without ownership")
		}
		a.readers--
		if a.readers == 0 {
			a.notifyLocked()
		}
		a.mu.Unlock()
	}, true
}

func (a *receiptOperationAdmission) acquireExclusive(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a.mu.Lock()
		if !a.exclusive {
			a.exclusive = true
			a.mu.Unlock()
			break
		}
		changed := a.changed
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}

	for {
		a.mu.Lock()
		if err := ctx.Err(); err != nil {
			a.exclusive = false
			a.notifyLocked()
			a.mu.Unlock()
			return nil, err
		}
		if a.readers == 0 {
			a.mu.Unlock()
			return func() {
				a.mu.Lock()
				if !a.exclusive {
					a.mu.Unlock()
					panic("service: receipt exclusive admission released without ownership")
				}
				a.exclusive = false
				a.notifyLocked()
				a.mu.Unlock()
			}, nil
		}
		changed := a.changed
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			a.mu.Lock()
			a.exclusive = false
			a.notifyLocked()
			a.mu.Unlock()
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (a *receiptOperationAdmission) notifyLocked() {
	close(a.changed)
	a.changed = make(chan struct{})
}
