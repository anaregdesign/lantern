package service

import (
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

var errRetiredCatalogSlotDrift = errors.New("service: retired receipt catalog changed during baseline preparation")

type retiredReceiptCatalogSlot struct {
	mu                sync.Mutex
	catalog           *mutationreceipt.RetiredCatalog
	activeEpoch       mutationreceipt.Epoch
	policyFingerprint [32]byte
	maxEntries        int
	maxBytes          uint64
	highWaterMillis   int64
	revision          uint64
}

type retiredCatalogSlotStage struct {
	slot              *retiredReceiptCatalogSlot
	previous          *mutationreceipt.RetiredCatalog
	previousHighWater int64
	previousRevision  uint64
	finished          bool
}

func newRetiredReceiptCatalogSlot(
	policy mutationreceipt.Config,
	activeHighWaterMillis int64,
	state mutationreceipt.RetiredCatalogSnapshot,
) (*retiredReceiptCatalogSlot, error) {
	config, fingerprint, err := retiredCatalogConfig(policy, activeHighWaterMillis)
	if err != nil {
		return nil, err
	}
	if state.ClockHighWaterMillis > activeHighWaterMillis {
		return nil, mutationreceipt.ErrRetiredCatalogClockRollback
	}
	catalog, err := mutationreceipt.NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		return nil, err
	}
	canonical, err := catalog.Snapshot(time.UnixMilli(activeHighWaterMillis))
	if err != nil {
		return nil, err
	}
	return &retiredReceiptCatalogSlot{
		catalog:           catalog,
		activeEpoch:       policy.Epoch,
		policyFingerprint: fingerprint,
		maxEntries:        policy.MaxEntries,
		maxBytes:          policy.MaxBytes,
		highWaterMillis:   canonical.ClockHighWaterMillis,
		revision:          1,
	}, nil
}

func newEmptyRetiredReceiptCatalogSlot(
	policy mutationreceipt.Config,
	activeHighWaterMillis int64,
) (*retiredReceiptCatalogSlot, error) {
	config, _, err := retiredCatalogConfig(policy, activeHighWaterMillis)
	if err != nil {
		return nil, err
	}
	catalog, err := mutationreceipt.NewRetiredCatalog(config)
	if err != nil {
		return nil, err
	}
	state, err := catalog.Snapshot(time.UnixMilli(activeHighWaterMillis))
	if err != nil {
		return nil, err
	}
	return newRetiredReceiptCatalogSlot(policy, activeHighWaterMillis, state)
}

func newEmptyRetiredCatalogSnapshot(
	policy mutationreceipt.Config,
	activeHighWaterMillis int64,
) (mutationreceipt.RetiredCatalogSnapshot, error) {
	slot, err := newEmptyRetiredReceiptCatalogSlot(policy, activeHighWaterMillis)
	if err != nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, err
	}
	state, _, err := slot.snapshot(policy, activeHighWaterMillis)
	return state, err
}

func retiredCatalogConfig(
	policy mutationreceipt.Config,
	highWaterMillis int64,
) (mutationreceipt.RetiredCatalogConfig, [32]byte, error) {
	if highWaterMillis < 0 {
		return mutationreceipt.RetiredCatalogConfig{}, [32]byte{}, mutationreceipt.ErrInvalidClock
	}
	validated, err := mutationreceipt.New(clearReceiptClockHighWater(policy))
	if err != nil {
		return mutationreceipt.RetiredCatalogConfig{}, [32]byte{}, err
	}
	return mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch:    policy.Epoch,
		MaxEntries:     policy.MaxEntries,
		MaxBytes:       policy.MaxBytes,
		ClockHighWater: time.UnixMilli(highWaterMillis),
	}, validated.PolicyFingerprint(), nil
}

func (s *retiredReceiptCatalogSlot) initializeIfNeeded(
	policy mutationreceipt.Config,
	activeHighWaterMillis int64,
) error {
	if s == nil {
		return errors.New("service: retired receipt catalog slot is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalog != nil {
		return s.validatePolicyLocked(policy)
	}
	initialized, err := newEmptyRetiredReceiptCatalogSlot(policy, activeHighWaterMillis)
	if err != nil {
		return err
	}
	s.catalog = initialized.catalog
	s.activeEpoch = initialized.activeEpoch
	s.policyFingerprint = initialized.policyFingerprint
	s.maxEntries = initialized.maxEntries
	s.maxBytes = initialized.maxBytes
	s.highWaterMillis = initialized.highWaterMillis
	s.revision = initialized.revision
	return nil
}

func (s *retiredReceiptCatalogSlot) snapshot(
	policy mutationreceipt.Config,
	activeHighWaterMillis int64,
) (mutationreceipt.RetiredCatalogSnapshot, uint64, error) {
	if s == nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, 0, errors.New("service: retired receipt catalog slot is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalog == nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, 0, errors.New("service: retired receipt catalog slot is uninitialized")
	}
	if err := s.validatePolicyLocked(policy); err != nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, 0, err
	}
	if activeHighWaterMillis < s.highWaterMillis {
		return mutationreceipt.RetiredCatalogSnapshot{}, 0, mutationreceipt.ErrRetiredCatalogClockRollback
	}
	state, err := s.catalog.Snapshot(time.UnixMilli(activeHighWaterMillis))
	if err != nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, 0, err
	}
	if state.ClockHighWaterMillis != activeHighWaterMillis {
		return mutationreceipt.RetiredCatalogSnapshot{}, 0, mutationreceipt.ErrInvalidRetiredCatalogSnapshot
	}
	if activeHighWaterMillis != s.highWaterMillis {
		s.highWaterMillis = activeHighWaterMillis
		s.revision++
	}
	return state, s.revision, nil
}

func (s *retiredReceiptCatalogSlot) beginReplace(
	policy mutationreceipt.Config,
	expectedRevision uint64,
	activeHighWaterMillis int64,
	state mutationreceipt.RetiredCatalogSnapshot,
) (*retiredCatalogSlotStage, error) {
	if s == nil {
		return nil, errors.New("service: retired receipt catalog slot is nil")
	}
	config, _, err := retiredCatalogConfig(policy, activeHighWaterMillis)
	if err != nil {
		return nil, err
	}
	if state.ClockHighWaterMillis != activeHighWaterMillis {
		return nil, mutationreceipt.ErrRetiredCatalogClockRollback
	}
	candidate, err := mutationreceipt.NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		return nil, err
	}
	canonical, err := candidate.Snapshot(time.UnixMilli(activeHighWaterMillis))
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(canonical, state) {
		return nil, mutationreceipt.ErrInvalidRetiredCatalogSnapshot
	}

	s.mu.Lock()
	if err := s.validatePolicyLocked(policy); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if s.revision != expectedRevision || activeHighWaterMillis < s.highWaterMillis {
		s.mu.Unlock()
		return nil, errRetiredCatalogSlotDrift
	}
	stage := &retiredCatalogSlotStage{
		slot:              s,
		previous:          s.catalog,
		previousHighWater: s.highWaterMillis,
		previousRevision:  s.revision,
	}
	s.catalog = candidate
	s.highWaterMillis = activeHighWaterMillis
	s.revision++
	return stage, nil
}

func (s *retiredReceiptCatalogSlot) validatePolicyLocked(policy mutationreceipt.Config) error {
	_, fingerprint, err := retiredCatalogConfig(policy, s.highWaterMillis)
	if err != nil {
		return err
	}
	if s.activeEpoch != policy.Epoch ||
		s.policyFingerprint != fingerprint ||
		s.maxEntries != policy.MaxEntries ||
		s.maxBytes != policy.MaxBytes {
		return mutationreceipt.ErrInvalidRetiredCatalogConfig
	}
	return nil
}

func (s *retiredCatalogSlotStage) Commit() {
	if s == nil || s.finished {
		return
	}
	s.finished = true
	s.slot.mu.Unlock()
}

func (s *retiredCatalogSlotStage) Abort() {
	if s == nil || s.finished {
		return
	}
	s.slot.catalog = s.previous
	s.slot.highWaterMillis = s.previousHighWater
	s.slot.revision = s.previousRevision
	s.finished = true
	s.slot.mu.Unlock()
}
