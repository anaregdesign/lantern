package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

const nativeRegistrationProofCount = 128
const nativeRegistrationProofBytes uint64 = 8 << 20

type nativeRegistrationEntry struct {
	proof           *security.CurrentRegistration
	producer        security.CurrentCredentialProducer
	binding, digest [32]byte
	expires         time.Time
	bytes           uint64
	applyStarted    bool
}

// Request-owned bounded storage only. The existing Workflow lifecycle cancels
// and joins its users; no worker, refresh loop or durable credential store exists.
type nativeRegistrationProofs struct {
	mu      sync.Mutex
	entries map[[32]byte]nativeRegistrationEntry
	bytes   uint64
	closed  bool
	bounds  func() (time.Time, time.Time, error)
	discard func(*security.CurrentRegistration)
	ctx     context.Context
	cancel  context.CancelFunc
	calls   sync.WaitGroup
}

func newNativeRegistrationProofs(bounds func() (time.Time, time.Time, error), discard func(*security.CurrentRegistration)) *nativeRegistrationProofs {
	ctx, cancel := context.WithCancel(context.Background())
	return &nativeRegistrationProofs{entries: make(map[[32]byte]nativeRegistrationEntry), bounds: bounds, discard: discard, ctx: ctx, cancel: cancel}
}

func (p *nativeRegistrationProofs) enter(ctx context.Context) (context.Context, func(), error) {
	if p == nil {
		return nil, nil, ErrUnavailable
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, nil, ErrUnavailable
	}
	p.calls.Add(1)
	p.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.ctx, cancel)
	return ctx, func() { stop(); cancel(); p.calls.Done() }, nil
}

func (p *nativeRegistrationProofs) sweepLocked() (time.Time, error) {
	if p.closed || p.bounds == nil {
		return time.Time{}, ErrUnavailable
	}
	low, high, err := p.bounds()
	if err != nil || low.IsZero() || high.Before(low) {
		return time.Time{}, ErrUnavailable
	}
	for id, entry := range p.entries {
		if !high.Before(entry.expires) {
			delete(p.entries, id)
			p.bytes -= entry.bytes
			p.discard(entry.proof)
		}
	}
	return high, nil
}

func (p *nativeRegistrationProofs) put(entry nativeRegistrationEntry) (string, error) {
	if p == nil || entry.proof == nil || entry.producer == nil || entry.binding == [32]byte{} || entry.bytes == 0 || entry.bytes > nativeRegistrationProofBytes {
		return "", ErrUnavailable
	}
	raw, err := newSessionSecret()
	if err != nil {
		return "", err
	}
	id := sha256.Sum256([]byte(raw))
	p.mu.Lock()
	defer p.mu.Unlock()
	high, err := p.sweepLocked()
	if err != nil {
		return "", err
	}
	if !high.Before(entry.expires) || len(p.entries) >= nativeRegistrationProofCount || entry.bytes > nativeRegistrationProofBytes-p.bytes {
		return "", ErrUnavailable
	}
	if _, exists := p.entries[id]; exists {
		return "", ErrUnavailable
	}
	entry.digest = id
	p.entries[id] = entry
	p.bytes += entry.bytes
	return raw, nil
}

// Copy the entry under the mutex. Cleanup discards stored producer references,
// while an entered call retains its own bounded copy until cancellation/join.
func (p *nativeRegistrationProofs) get(raw string, binding [32]byte, apply bool) (nativeRegistrationEntry, bool, error) {
	if p == nil || len(raw) != 43 || binding == [32]byte{} {
		return nativeRegistrationEntry{}, false, ErrUnauthenticated
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return nativeRegistrationEntry{}, false, ErrUnauthenticated
	}
	id := sha256.Sum256([]byte(raw))
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.sweepLocked(); err != nil {
		return nativeRegistrationEntry{}, false, err
	}
	entry, found := p.entries[id]
	if !found || subtle.ConstantTimeCompare(id[:], entry.digest[:]) != 1 || entry.binding != binding {
		return nativeRegistrationEntry{}, false, ErrUnauthenticated
	}
	if apply && entry.proof.RecoveryOnly() {
		return nativeRegistrationEntry{}, false, ErrUnauthenticated
	}
	duplicate := entry.applyStarted
	if apply && !duplicate {
		entry.applyStarted = true
		p.entries[id] = entry
	}
	return entry, duplicate, nil
}

func (p *nativeRegistrationProofs) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	p.cancel()
	p.mu.Unlock()
	p.calls.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, entry := range p.entries {
		p.discard(entry.proof)
		delete(p.entries, id)
	}
	p.bytes = 0
}
