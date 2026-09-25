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

// Lookup returns Confirmed only for an exact known receipt that remains live
// at the supplied nondecreasing clock high-water. Every absent or expired
// retired ID is NoLongerProvable. Active-epoch IDs must be routed elsewhere.
func (c *RetiredCatalog) Lookup(id ID, highWater time.Time) (Status, Receipt, error) {
	epoch, _, err := id.parts()
	if err != nil {
		return 0, Receipt{}, err
	}
	if epoch == c.activeEpoch {
		return 0, Receipt{}, ErrActiveEpochReceipt
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advanceLocked(highWater); err != nil {
		return 0, Receipt{}, err
	}
	retired, ok := c.epochs[epoch]
	if !ok {
		return NoLongerProvable, Receipt{}, nil
	}
	receipt, ok := retired.receipts[id]
	if !ok || receipt.DeadlineMillis <= c.highWaterMS {
		return NoLongerProvable, Receipt{}, nil
	}
	return Confirmed, cloneReceipt(receipt), nil
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
