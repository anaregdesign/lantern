package mutationreceipt

import (
	"bytes"
	"errors"
	"math"
	"sort"
	"time"
)

const snapshotVersion = 1

var ErrInvalidSnapshot = errors.New("mutationreceipt: invalid receipt snapshot")
var ErrRetiredEpochSnapshot = errors.New("mutationreceipt: retired-epoch receipt snapshot is not supported")

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

// Snapshot returns receipt rows sorted by operation ID so equivalent Store
// states have a deterministic representation. Result bytes are copied; the
// caller may retain or mutate the returned value after this call. Until a
// retired-epoch archive shape exists, a known old-epoch receipt fails the
// whole export instead of creating an incomplete backup.
func (s *Store) Snapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	snapshotBytes := uint64(0)
	seenContributions := make(map[ContribID]struct{})
	seenGroups := make(map[GroupID]*groupReceiptRows)
	for i, receipt := range state.Receipts {
		if i != 0 && bytes.Compare(state.Receipts[i-1].ID[:], receipt.ID[:]) >= 0 {
			return nil, ErrInvalidSnapshot
		}
		if err := s.validateSnapshotReceipt(receipt, state.ClockHighWaterMillis); err != nil {
			return nil, err
		}
		cost := receipt.cost()
		if i >= s.maxEntries || cost > s.maxBytes-snapshotBytes {
			return nil, ErrInvalidSnapshot
		}
		snapshotBytes += cost
		if receipt.HasContrib {
			if _, exists := seenContributions[receipt.ContribID]; exists {
				return nil, ErrInvalidSnapshot
			}
			seenContributions[receipt.ContribID] = struct{}{}
		}
		group := seenGroups[receipt.Group]
		if group == nil {
			group = &groupReceiptRows{count: receipt.Count, items: make(map[uint32]ID)}
			seenGroups[receipt.Group] = group
		} else if group.count != receipt.Count {
			return nil, ErrInvalidSnapshot
		}
		if _, duplicate := group.items[receipt.Index]; duplicate {
			return nil, ErrInvalidSnapshot
		}
		group.items[receipt.Index] = receipt.ID
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
