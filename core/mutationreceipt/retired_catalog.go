package mutationreceipt

import (
	"bytes"
	"errors"
	"sort"
	"sync"
	"time"
)

const retiredCatalogSnapshotVersion = 1

var (
	ErrInvalidRetiredCatalogConfig   = errors.New("mutationreceipt: invalid retired receipt catalog policy")
	ErrInvalidRetiredCatalogSnapshot = errors.New("mutationreceipt: invalid retired receipt catalog snapshot")
	ErrRetiredCatalogCapacity        = errors.New("mutationreceipt: retired receipt catalog capacity exceeded")
	ErrRetiredCatalogClockRollback   = errors.New("mutationreceipt: retired receipt catalog clock rollback")
	ErrActiveEpochReceipt            = errors.New("mutationreceipt: active-epoch receipt must be routed to the active store")
)

// RetiredCatalogConfig supplies the current active epoch, aggregate bounds,
// and effective clock floor for a read-only retired receipt catalog.
type RetiredCatalogConfig struct {
	ActiveEpoch    Epoch
	MaxEntries     int
	MaxBytes       uint64
	ClockHighWater time.Time
}

// RetiredEpochPolicy is the immutable Store policy that originally admitted
// one retired epoch's receipts. Its fingerprint remains in State and is
// recomputed during import.
type RetiredEpochPolicy struct {
	Epoch      Epoch
	Retention  time.Duration
	MaxEntries int
	MaxBytes   uint64
}

// RetiredEpochSnapshot contains one retired epoch's original policy and exact
// known receipt rows. State must use the enclosing catalog clock high-water.
type RetiredEpochSnapshot struct {
	Policy RetiredEpochPolicy
	State  Snapshot
}

// RetiredCatalogSnapshot is a deterministic, detached copy of all still-live
// retired receipt evidence. Epochs and each epoch's receipt IDs are strictly
// sorted. Empty epoch members are invalid.
type RetiredCatalogSnapshot struct {
	Version              uint32
	ClockHighWaterMillis int64
	Epochs               []RetiredEpochSnapshot
}

// RetiredCatalog is an immutable, bounded set of exact retired-epoch receipt
// evidence. Only its effective clock high-water advances. It has no admission,
// logical-call, contribution, or deadline-eviction indexes and cannot
// authorize a mutation.
type RetiredCatalog struct {
	mu          sync.Mutex
	activeEpoch Epoch
	maxEntries  int
	maxBytes    uint64
	highWaterMS int64
	epochs      map[Epoch]retiredCatalogEpoch
}

type retiredCatalogEpoch struct {
	policy      RetiredEpochPolicy
	fingerprint [32]byte
	receipts    map[ID]Receipt
}

// RetiredCatalogSnapshotFromActive converts one complete active Store
// snapshot into detached retired-epoch evidence without pruning it. A later
// NewRetiredCatalogFromUnion call therefore charges the original raw rows
// against the destination's aggregate bounds before applying its newer clock
// high-water.
func RetiredCatalogSnapshotFromActive(
	config Config,
	state Snapshot,
) (RetiredCatalogSnapshot, error) {
	store, err := NewFromSnapshot(config, state)
	if err != nil {
		return RetiredCatalogSnapshot{}, err
	}
	configuredHighWater := int64(0)
	if !config.ClockHighWater.IsZero() {
		configuredHighWater = config.ClockHighWater.UnixMilli()
	}
	if configuredHighWater != state.ClockHighWaterMillis {
		return RetiredCatalogSnapshot{}, ErrInvalidSnapshot
	}
	canonical, err := store.Snapshot()
	if err != nil {
		return RetiredCatalogSnapshot{}, err
	}
	snapshot := RetiredCatalogSnapshot{
		Version:              retiredCatalogSnapshotVersion,
		ClockHighWaterMillis: canonical.ClockHighWaterMillis,
	}
	if len(canonical.Receipts) == 0 {
		return snapshot, nil
	}
	snapshot.Epochs = []RetiredEpochSnapshot{{
		Policy: RetiredEpochPolicy{
			Epoch:      config.Epoch,
			Retention:  config.Retention,
			MaxEntries: config.MaxEntries,
			MaxBytes:   config.MaxBytes,
		},
		State: canonical,
	}}
	return snapshot, nil
}

// NewRetiredCatalog returns an empty catalog. Retired evidence is installed
// only by constructing a new catalog from a complete validated snapshot.
func NewRetiredCatalog(config RetiredCatalogConfig) (*RetiredCatalog, error) {
	highWaterMS, err := retiredCatalogClockMillis(config.ClockHighWater)
	if config.ActiveEpoch == (Epoch{}) || config.MaxEntries <= 0 || config.MaxBytes == 0 || err != nil {
		return nil, ErrInvalidRetiredCatalogConfig
	}
	return &RetiredCatalog{
		activeEpoch: config.ActiveEpoch,
		maxEntries:  config.MaxEntries,
		maxBytes:    config.MaxBytes,
		highWaterMS: highWaterMS,
		epochs:      make(map[Epoch]retiredCatalogEpoch),
	}, nil
}

// NewRetiredCatalogFromSnapshot atomically validates and imports a complete
// multi-epoch snapshot. The caller's clock high-water may advance the
// snapshot's floor and omit rows that expired in the meantime, but it may
// never move that floor backward.
func NewRetiredCatalogFromSnapshot(config RetiredCatalogConfig, state RetiredCatalogSnapshot) (*RetiredCatalog, error) {
	catalog, err := NewRetiredCatalog(config)
	if err != nil {
		return nil, err
	}
	if state.Version != retiredCatalogSnapshotVersion || state.ClockHighWaterMillis < 0 {
		return nil, ErrInvalidRetiredCatalogSnapshot
	}
	if catalog.highWaterMS < state.ClockHighWaterMillis {
		return nil, ErrRetiredCatalogClockRollback
	}
	if len(state.Epochs) > catalog.maxEntries {
		return nil, ErrRetiredCatalogCapacity
	}

	inputEntries := 0
	inputBytes := uint64(0)
	var previousEpoch Epoch
	for i, member := range state.Epochs {
		epoch := member.Policy.Epoch
		if member.State.Epoch != epoch ||
			member.State.ClockHighWaterMillis != state.ClockHighWaterMillis ||
			len(member.State.Receipts) == 0 {
			return nil, ErrInvalidRetiredCatalogSnapshot
		}
		if epoch == catalog.activeEpoch {
			return nil, ErrActiveEpochReceipt
		}
		if i != 0 && bytes.Compare(previousEpoch[:], epoch[:]) >= 0 {
			return nil, ErrInvalidRetiredCatalogSnapshot
		}
		previousEpoch = epoch

		if len(member.State.Receipts) > catalog.maxEntries-inputEntries {
			return nil, ErrRetiredCatalogCapacity
		}
		inputEntries += len(member.State.Receipts)
		for _, receipt := range member.State.Receipts {
			cost := receipt.cost()
			if cost > catalog.maxBytes || inputBytes > catalog.maxBytes-cost {
				return nil, ErrRetiredCatalogCapacity
			}
			inputBytes += cost
		}

		epochConfig := Config{
			Epoch:          epoch,
			Retention:      member.Policy.Retention,
			MaxEntries:     member.Policy.MaxEntries,
			MaxBytes:       member.Policy.MaxBytes,
			ClockHighWater: time.UnixMilli(catalog.highWaterMS),
		}
		validated, err := NewFromSnapshot(epochConfig, member.State)
		if err != nil {
			return nil, errors.Join(ErrInvalidRetiredCatalogSnapshot, err)
		}
		canonical, err := validated.Snapshot()
		if err != nil {
			return nil, errors.Join(ErrInvalidRetiredCatalogSnapshot, err)
		}
		if len(canonical.Receipts) == 0 {
			continue
		}

		rows := make(map[ID]Receipt, len(canonical.Receipts))
		for _, receipt := range canonical.Receipts {
			owned := cloneReceipt(receipt)
			rows[owned.ID] = owned
		}
		catalog.epochs[epoch] = retiredCatalogEpoch{
			policy:      member.Policy,
			fingerprint: canonical.PolicyFingerprint,
			receipts:    rows,
		}
	}
	return catalog, nil
}

// NewRetiredCatalogFromUnion returns the exact union of complete retired
// catalog snapshots. Exact duplicate rows are idempotent; conflicting
// policies, receipt evidence, group positions, or contribution bindings fail
// closed. Aggregate capacity is charged to the distinct raw union before the
// caller's clock high-water prunes expired rows. Inputs and existing catalogs
// remain detached and unchanged.
func NewRetiredCatalogFromUnion(
	config RetiredCatalogConfig,
	states ...RetiredCatalogSnapshot,
) (*RetiredCatalog, error) {
	empty, err := NewRetiredCatalog(config)
	if err != nil {
		return nil, err
	}
	for _, state := range states {
		if _, err := NewRetiredCatalogFromSnapshot(config, state); err != nil {
			return nil, err
		}
	}

	union := make(map[Epoch]*retiredCatalogEpoch)
	entries := 0
	byteCount := uint64(0)
	for _, state := range states {
		for _, member := range state.Epochs {
			epoch := member.Policy.Epoch
			merged := union[epoch]
			if merged == nil {
				merged = &retiredCatalogEpoch{
					policy:      member.Policy,
					fingerprint: member.State.PolicyFingerprint,
					receipts:    make(map[ID]Receipt),
				}
				union[epoch] = merged
			} else if merged.policy != member.Policy {
				return nil, ErrInvalidRetiredCatalogSnapshot
			}

			for _, receipt := range member.State.Receipts {
				if existing, ok := merged.receipts[receipt.ID]; ok {
					if !sameRetiredReceipt(existing, receipt) {
						return nil, ErrInvalidRetiredCatalogSnapshot
					}
					continue
				}
				if entries == empty.maxEntries {
					return nil, ErrRetiredCatalogCapacity
				}
				cost := receipt.cost()
				if cost > empty.maxBytes || byteCount > empty.maxBytes-cost {
					return nil, ErrRetiredCatalogCapacity
				}
				entries++
				byteCount += cost
				owned := cloneReceipt(receipt)
				merged.receipts[owned.ID] = owned
			}
		}
	}

	epochs := make([]Epoch, 0, len(union))
	for epoch := range union {
		epochs = append(epochs, epoch)
	}
	sortEpochs(epochs)
	canonical := RetiredCatalogSnapshot{
		Version:              retiredCatalogSnapshotVersion,
		ClockHighWaterMillis: empty.highWaterMS,
		Epochs:               make([]RetiredEpochSnapshot, 0, len(epochs)),
	}
	for _, epoch := range epochs {
		merged := union[epoch]
		raw := make([]Receipt, 0, len(merged.receipts))
		for _, receipt := range merged.receipts {
			raw = append(raw, receipt)
		}
		sort.Slice(raw, func(i, j int) bool {
			return bytes.Compare(raw[i].ID[:], raw[j].ID[:]) < 0
		})
		if err := validateSnapshotRelationships(raw); err != nil {
			return nil, errors.Join(ErrInvalidRetiredCatalogSnapshot, err)
		}

		live := make([]Receipt, 0, len(raw))
		for _, receipt := range raw {
			if receipt.DeadlineMillis > empty.highWaterMS {
				live = append(live, receipt)
			}
		}
		if len(live) == 0 {
			continue
		}
		canonical.Epochs = append(canonical.Epochs, RetiredEpochSnapshot{
			Policy: merged.policy,
			State: Snapshot{
				Version:              snapshotVersion,
				Epoch:                epoch,
				PolicyFingerprint:    merged.fingerprint,
				ClockHighWaterMillis: empty.highWaterMS,
				Receipts:             live,
			},
		})
	}
	return NewRetiredCatalogFromSnapshot(config, canonical)
}

// Lookup returns Confirmed only for an exact known receipt that remains live
// at the supplied nondecreasing clock high-water. Every absent or expired
// retired ID is NoLongerProvable. Active-epoch IDs must be routed elsewhere.
func (c *RetiredCatalog) Lookup(id ID, highWater time.Time) (Status, Receipt, error) {
	observations, err := c.LookupMany([]ID{id}, highWater)
	if err != nil {
		return 0, Receipt{}, err
	}
	return observations[0].Status, observations[0].Receipt, nil
}

// LookupMany advances the catalog clock once and returns request-index-aligned
// retired evidence. Active-epoch IDs are rejected as a whole so callers
// cannot accidentally bypass the mutable Store.
func (c *RetiredCatalog) LookupMany(ids []ID, highWater time.Time) ([]Observation, error) {
	epochs := make([]Epoch, len(ids))
	for i, id := range ids {
		epoch, _, err := id.parts()
		if err != nil {
			return nil, err
		}
		if epoch == c.activeEpoch {
			return nil, ErrActiveEpochReceipt
		}
		epochs[i] = epoch
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advanceLocked(highWater); err != nil {
		return nil, err
	}
	observations := make([]Observation, len(ids))
	for i, id := range ids {
		retired, ok := c.epochs[epochs[i]]
		if !ok {
			observations[i].Status = NoLongerProvable
			continue
		}
		receipt, ok := retired.receipts[id]
		if !ok || receipt.DeadlineMillis <= c.highWaterMS {
			observations[i].Status = NoLongerProvable
			continue
		}
		observations[i] = Observation{
			Status: Confirmed, Receipt: cloneReceipt(receipt),
		}
	}
	return observations, nil
}

// Snapshot returns a deterministic deep copy at the supplied nondecreasing
// clock high-water. Expired rows and the now-empty epoch members they belonged
// to are omitted without mutating the catalog's bounded immutable evidence.
func (c *RetiredCatalog) Snapshot(highWater time.Time) (RetiredCatalogSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advanceLocked(highWater); err != nil {
		return RetiredCatalogSnapshot{}, err
	}

	state := RetiredCatalogSnapshot{
		Version:              retiredCatalogSnapshotVersion,
		ClockHighWaterMillis: c.highWaterMS,
		Epochs:               make([]RetiredEpochSnapshot, 0, len(c.epochs)),
	}
	epochs := make([]Epoch, 0, len(c.epochs))
	for epoch := range c.epochs {
		epochs = append(epochs, epoch)
	}
	sortEpochs(epochs)

	entryCount := 0
	byteCount := uint64(0)
	for _, epoch := range epochs {
		member := c.epochs[epoch]
		receipts := make([]Receipt, 0, len(member.receipts))
		for _, receipt := range member.receipts {
			if receipt.DeadlineMillis > c.highWaterMS {
				receipts = append(receipts, cloneReceipt(receipt))
			}
		}
		if len(receipts) == 0 {
			continue
		}
		sort.Slice(receipts, func(i, j int) bool {
			return bytes.Compare(receipts[i].ID[:], receipts[j].ID[:]) < 0
		})
		if len(receipts) > c.maxEntries-entryCount {
			return RetiredCatalogSnapshot{}, ErrRetiredCatalogCapacity
		}
		entryCount += len(receipts)
		for _, receipt := range receipts {
			cost := receipt.cost()
			if cost > c.maxBytes || byteCount > c.maxBytes-cost {
				return RetiredCatalogSnapshot{}, ErrRetiredCatalogCapacity
			}
			byteCount += cost
		}
		state.Epochs = append(state.Epochs, RetiredEpochSnapshot{
			Policy: member.policy,
			State: Snapshot{
				Version:              snapshotVersion,
				Epoch:                epoch,
				PolicyFingerprint:    member.fingerprint,
				ClockHighWaterMillis: c.highWaterMS,
				Receipts:             receipts,
			},
		})
	}
	return state, nil
}

func (c *RetiredCatalog) advanceLocked(highWater time.Time) error {
	millis, err := retiredCatalogClockMillis(highWater)
	if err != nil {
		return err
	}
	if millis < c.highWaterMS {
		return ErrRetiredCatalogClockRollback
	}
	c.highWaterMS = millis
	return nil
}

func retiredCatalogClockMillis(highWater time.Time) (int64, error) {
	if highWater.IsZero() {
		return 0, nil
	}
	millis := highWater.UnixMilli()
	if millis < 0 {
		return 0, ErrInvalidClock
	}
	return millis, nil
}

func sortEpochs(epochs []Epoch) {
	sort.Slice(epochs, func(i, j int) bool {
		return bytes.Compare(epochs[i][:], epochs[j][:]) < 0
	})
}

func sameRetiredReceipt(left, right Receipt) bool {
	return left.Intent == right.Intent &&
		left.DeadlineMillis == right.DeadlineMillis &&
		bytes.Equal(left.Result, right.Result)
}
