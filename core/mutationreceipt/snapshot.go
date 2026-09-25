package mutationreceipt

import (
	"bytes"
	"errors"
	"math"
	"sort"
	"time"
)

const snapshotVersion = 1

var (
	ErrInvalidSnapshot         = errors.New("mutationreceipt: invalid receipt snapshot")
	ErrRetiredEpochSnapshot    = errors.New("mutationreceipt: retired-epoch receipt snapshot is not supported")
	ErrSnapshotDoesNotDominate = errors.New("mutationreceipt: snapshot omits or changes a retained receipt")
)

// Snapshot is a detached copy of one Store's known receipts and clock
// high-water. It is only one component of a future graph/receipt/origin
// Snapshot cut: exporting or restoring it alone cannot certify an endpoint
// generation, replay a WAL frontier, or authorize receipt-enabled writes.
type Snapshot struct {
	Version              uint32
	Epoch                Epoch
	PolicyFingerprint    [32]byte
	ClockHighWaterMillis int64
	Receipts             []Receipt
}

// SnapshotInstall is a reversible whole-Store replacement. It holds Store.mu
// from BeginSnapshotInstall until Commit or Abort, so direct readers cannot
// observe the staged map/index set. A staged snapshot retains every receiver
// receipt that remains live at the effective clock high-water. The caller must
// commit an enclosing durable boundary containing that high-water before
// Commit.
type SnapshotInstall struct {
	store    *Store
	previous storeSnapshotState
	closed   bool
}

type storeSnapshotState struct {
	receipts      map[ID]Receipt
	groups        map[GroupID]*groupReceiptRows
	contributions map[ContribID]ID
	deadlines     deadlineIndex
	bytes         uint64
	highWaterMS   int64
}

// Snapshot returns receipt rows sorted by operation ID so equivalent Store
// states have a deterministic representation. Result bytes are copied; the
// caller may retain or mutate the returned value after this call. Store
// snapshots intentionally remain active-epoch-only; a known old-epoch receipt
// fails the whole export instead of creating an incomplete backup.
func (s *Store) Snapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.highWaterFault != nil {
		return Snapshot{}, s.highWaterFault
	}
	state := Snapshot{
		Version:              snapshotVersion,
		Epoch:                s.epoch,
		PolicyFingerprint:    s.fingerprint,
		ClockHighWaterMillis: s.highWaterMS,
		Receipts:             make([]Receipt, 0, len(s.receipts)),
	}
	for _, receipt := range s.receipts {
		epoch, _, err := receipt.ID.parts()
		if err != nil {
			return Snapshot{}, ErrInvalidSnapshot
		}
		if epoch != s.epoch {
			return Snapshot{}, ErrRetiredEpochSnapshot
		}
		state.Receipts = append(state.Receipts, cloneReceipt(receipt))
	}
	sort.Slice(state.Receipts, func(i, j int) bool {
		return bytes.Compare(state.Receipts[i].ID[:], state.Receipts[j].ID[:]) < 0
	})
	return state, nil
}

// NewFromSnapshotWithClockHighWaterSink validates the complete detached
// snapshot before advancing the durable high-water and returning a Store.
// The caller still must certify the matching graph, WAL, origin, and epoch
// cut before publishing it for serving.
func NewFromSnapshotWithClockHighWaterSink(config Config, state Snapshot, sink ClockHighWaterSink) (*Store, error) {
	s, err := NewFromSnapshot(config, state)
	if err != nil {
		return nil, err
	}
	if err := s.attachClockHighWaterSink(sink); err != nil {
		return nil, err
	}
	return s, nil
}

// NewFromSnapshot validates and rebuilds the bounded indexes for a complete
// Store snapshot of the same active epoch and policy. A higher caller-supplied
// ClockHighWater expires old rows without reopening their IDs. It cannot
// establish that this snapshot includes every committed WAL envelope or the
// matching graph cut; the caller must perform those checks before serving.
func NewFromSnapshot(config Config, state Snapshot) (*Store, error) {
	if state.Version != snapshotVersion || state.Epoch == (Epoch{}) ||
		state.ClockHighWaterMillis < 0 || config.Epoch != state.Epoch {
		return nil, ErrInvalidSnapshot
	}

	s, err := New(config)
	if err != nil {
		return nil, err
	}
	if state.PolicyFingerprint != s.fingerprint {
		return nil, ErrInvalidSnapshot
	}
	if state.ClockHighWaterMillis > s.highWaterMS {
		s.highWaterMS = state.ClockHighWaterMillis
	}
	if len(state.Receipts) > s.maxEntries {
		return nil, ErrInvalidSnapshot
	}
	if err := validateSnapshotRelationships(state.Receipts); err != nil {
		return nil, err
	}
	snapshotBytes := uint64(0)
	for i, receipt := range state.Receipts {
		if i != 0 && bytes.Compare(state.Receipts[i-1].ID[:], receipt.ID[:]) >= 0 {
			return nil, ErrInvalidSnapshot
		}
		if err := s.validateSnapshotReceipt(receipt, state.ClockHighWaterMillis); err != nil {
			return nil, err
		}
		cost := receipt.cost()
		if cost > s.maxBytes-snapshotBytes {
			return nil, ErrInvalidSnapshot
		}
		snapshotBytes += cost
		// Expiry caused by a newer caller-owned clock is safe to apply during
		// restore. The old ID remains non-executable because the high-water is
		// retained even after its bytes are removed.
		if receipt.DeadlineMillis <= s.highWaterMS {
			continue
		}
		if receipt.HasContrib {
			s.contributions[receipt.ContribID] = receipt.ID
		}
		owned := cloneReceipt(receipt)
		s.receipts[owned.ID] = owned
		liveGroup := s.groups[owned.Group]
		if liveGroup == nil {
			liveGroup = &groupReceiptRows{count: owned.Count, items: make(map[uint32]ID)}
			s.groups[owned.Group] = liveGroup
		}
		liveGroup.items[owned.Index] = owned.ID
		s.deadlines.insert(deadlineEntry{id: owned.ID, deadlineMS: owned.DeadlineMillis})
		s.bytes += owned.cost()
	}
	return s, nil
}

func validateSnapshotRelationships(receipts []Receipt) error {
	seenContributions := make(map[ContribID]struct{})
	seenGroups := make(map[GroupID]*groupReceiptRows)
	for _, receipt := range receipts {
		if receipt.HasContrib {
			if _, exists := seenContributions[receipt.ContribID]; exists {
				return ErrInvalidSnapshot
			}
			seenContributions[receipt.ContribID] = struct{}{}
		}
		group := seenGroups[receipt.Group]
		if group == nil {
			group = &groupReceiptRows{count: receipt.Count, items: make(map[uint32]ID)}
			seenGroups[receipt.Group] = group
		} else if group.count != receipt.Count {
			return ErrInvalidSnapshot
		}
		if _, duplicate := group.items[receipt.Index]; duplicate {
			return ErrInvalidSnapshot
		}
		group.items[receipt.Index] = receipt.ID
	}
	return nil
}

// BeginSnapshotInstall validates a complete same-epoch, same-policy snapshot
// and stages its receipt indexes and nondecreasing clock high-water in place
// while retaining the Store object identity. Every receiver receipt still live
// at the effective high-water must be present byte-for-byte in the candidate;
// otherwise replacing it could turn a known retry into NotYetObserved. It
// deliberately does not invoke the Store's high-water sink: the caller must
// durably record the effective high-water in its enclosing commit before
// calling Commit. Abort restores the exact pre-stage state. The caller must
// hold its outer publication cut and defer Abort immediately.
func (s *Store) BeginSnapshotInstall(state Snapshot) (*SnapshotInstall, error) {
	if s == nil {
		return nil, ErrInvalidSnapshot
	}
	config := Config{
		Epoch:          s.epoch,
		Retention:      time.Duration(s.retentionMS) * time.Millisecond,
		MaxEntries:     s.maxEntries,
		MaxBytes:       s.maxBytes,
		ClockHighWater: state.ClockHighWater(),
	}
	candidate, err := NewFromSnapshot(config, state)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	release := true
	defer func() {
		if release {
			s.mu.Unlock()
		}
	}()
	if state.ClockHighWaterMillis < s.highWaterMS {
		config.ClockHighWater = time.UnixMilli(s.highWaterMS)
		candidate, err = NewFromSnapshot(config, state)
		if err != nil {
			return nil, err
		}
	}
	if s.highWaterFault != nil {
		return nil, s.highWaterFault
	}
	if err := validateRetainedReceiptDominance(candidate.receipts, s.receipts, candidate.highWaterMS); err != nil {
		return nil, err
	}
	stage := &SnapshotInstall{
		store: s,
		previous: storeSnapshotState{
			receipts:      s.receipts,
			groups:        s.groups,
			contributions: s.contributions,
			deadlines:     s.deadlines,
			bytes:         s.bytes,
			highWaterMS:   s.highWaterMS,
		},
	}
	s.receipts = candidate.receipts
	s.groups = candidate.groups
	s.contributions = candidate.contributions
	s.deadlines = candidate.deadlines
	s.bytes = candidate.bytes
	s.highWaterMS = candidate.highWaterMS
	release = false
	return stage, nil
}

// Commit publishes the staged receipt state by releasing Store.mu.
func (s *SnapshotInstall) Commit() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	s.store.mu.Unlock()
}

// Abort restores the exact pre-install receipt maps, indexes, and high-water.
func (s *SnapshotInstall) Abort() {
	if s == nil || s.closed {
		return
	}
	store := s.store
	store.receipts = s.previous.receipts
	store.groups = s.previous.groups
	store.contributions = s.previous.contributions
	store.deadlines = s.previous.deadlines
	store.bytes = s.previous.bytes
	store.highWaterMS = s.previous.highWaterMS
	s.closed = true
	store.mu.Unlock()
}

func validateRetainedReceiptDominance(candidate, current map[ID]Receipt, effectiveHighWater int64) error {
	for id, retained := range current {
		if retained.DeadlineMillis <= effectiveHighWater {
			continue
		}
		replacement, ok := candidate[id]
		if !ok || replacement.Intent != retained.Intent ||
			replacement.DeadlineMillis != retained.DeadlineMillis ||
			!bytes.Equal(replacement.Result, retained.Result) {
			return ErrSnapshotDoesNotDominate
		}
	}
	return nil
}

func (s *Store) validateSnapshotReceipt(receipt Receipt, snapshotHighWater int64) error {
	item := receipt.Intent
	epoch, issued, err := item.ID.parts()
	if err != nil || epoch != s.epoch || issued > math.MaxInt64-s.retentionMS ||
		tooFarFuture(issued, snapshotHighWater) ||
		item.Group == (GroupID{}) || item.Count == 0 || item.Index >= item.Count ||
		item.Kind < PutVertex || item.Kind > DeleteEdge ||
		(item.Kind == AddEdge) != item.HasContrib ||
		(item.HasContrib && item.ContribID == (ContribID{})) ||
		(!item.HasContrib && item.ContribID != (ContribID{})) ||
		receipt.DeadlineMillis != issued+s.retentionMS ||
		receipt.DeadlineMillis <= snapshotHighWater {
		return ErrInvalidSnapshot
	}
	return nil
}

// ClockHighWater returns the monotonic clock bound to serialize alongside a
// graph/receipt cut. A caller that loses this value must rotate the epoch.
func (state Snapshot) ClockHighWater() time.Time {
	return time.UnixMilli(state.ClockHighWaterMillis)
}
