package mutationreceipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

const (
	minRetention          = time.Hour
	maxRetention          = 30 * 24 * time.Hour
	freshWindowMS         = int64((5 * time.Minute) / time.Millisecond)
	committedPastWindowMS = freshWindowMS + int64(hlc.DefaultMaxSkew/time.Millisecond)
	// Logical byte accounting includes the stored ID, digest, group, index,
	// count, deadline, and original result. An Add reverse-index binding
	// additionally owns a copied ContribID and ID. Entry caps bound Go map/
	// heap overhead separately from this logical byte cap.
	receiptFixedBytes = uint64(idSize + sha256.Size + 16 + 4 + 4 + 8 + 1)
	bindingBytes      = uint64(24 + idSize)
)

var (
	ErrInvalidConfig        = errors.New("mutationreceipt: invalid policy")
	ErrInvalidClock         = errors.New("mutationreceipt: invalid clock")
	ErrInvalidBatch         = errors.New("mutationreceipt: invalid logical-call batch")
	ErrNoLongerProvable     = errors.New("mutationreceipt: operation no longer provable")
	ErrNotFresh             = errors.New("mutationreceipt: absent operation is not fresh")
	ErrIntentConflict       = errors.New("mutationreceipt: operation ID has a different intent or group")
	ErrPartialEnvelope      = errors.New("mutationreceipt: only part of the logical call is known")
	ErrContributionConflict = errors.New("mutationreceipt: contribution ID is already bound")
	ErrCapacity             = errors.New("mutationreceipt: live receipt capacity exhausted")
	ErrTransactionState     = errors.New("mutationreceipt: invalid transaction state")
	ErrHighWaterPersistence = errors.New("mutationreceipt: clock high-water persistence failed")
)

// ClockHighWaterSink must durably record at least millis before returning
// nil. It runs while Store.mu is held and must not call back into the Store.
// An error is indeterminate: the value may already be durable, so the Store
// faults rather than retrying a receipt decision against an older clock.
type ClockHighWaterSink func(millis int64) error

// Config is immutable for an epoch. Changing the retention horizon or losing
// ClockHighWater requires the caller to rotate the active epoch before any
// further receipt-capable mutation. There is deliberately no default policy:
// positive caps and a one-hour to 30-day horizon must be configured.
type Config struct {
	Epoch          Epoch
	Retention      time.Duration
	MaxEntries     int
	MaxBytes       uint64
	ClockHighWater time.Time
}

// Kind names the receipt-capable exact mutation family. Prefix Delete is
// deliberately absent. Put's condition flag belongs in canonical intent.
type Kind uint8

const (
	PutVertex Kind = iota + 1
	PutEdge
	AddEdge
	DeleteVertex
	DeleteEdge
)

// Intent names one operation in an ordered logical call. Digest must be
// computed from validated canonical semantic fields, not protobuf bytes.
// AddEdge requires a nonzero ContribID; no other Kind may supply one.
type Intent struct {
	ID         ID
	Group      GroupID
	Index      uint32
	Count      uint32
	Kind       Kind
	Digest     [sha256.Size]byte
	ContribID  ContribID
	HasContrib bool
}

// Receipt retains the original public result, including no-op outcomes.
// Result is opaque, exact encoded response data owned by the caller's wire
// contract. Store methods copy it on entry and exit.
type Receipt struct {
	Intent
	Result         []byte
	DeadlineMillis int64
}

func (r Receipt) cost() uint64 {
	n := receiptFixedBytes + uint64(len(r.Result))
	if r.HasContrib {
		n += bindingBytes
	}
	return n
}

func cloneReceipt(r Receipt) Receipt {
	r.Result = append([]byte(nil), r.Result...)
	return r
}

// Status is a read-only local observation. NotYetObserved never proves that
// the mutation did not execute elsewhere in an asynchronous cluster.
type Status uint8

const (
	Confirmed Status = iota + 1
	NotYetObserved
	NoLongerProvable
)

// Stats reports bounded live state. HighWaterMillis must be persisted and
// replicated by a future commit integration; this package itself is memory
// only and cannot certify restart continuity.
type Stats struct {
	Entries                   int
	Bytes                     uint64
	OldestDeadlineMillis      int64
	HighWaterMillis           int64
	LocalAdmissionRejects     uint64
	ReplicationCapacityStalls uint64
	NoLongerProvableLookups   uint64
}

// Observation is one read-only, request-index-aligned receipt lookup result.
// Receipt is populated exactly when Status is Confirmed.
type Observation struct {
	Status  Status
	Receipt Receipt
}

// Store holds only receipt bookkeeping. It does not make graph mutations,
// log entries, or Snapshot cuts atomic. Stage may allocate before WAL while
// its writes are hidden by the lock; Abort reverses those writes, and Commit
// only unlocks. Do not attach this Store to a serving mutation path until its
// outer server publication and recovery boundary is implemented.
type Store struct {
	mu                       sync.Mutex
	epoch                    Epoch
	retentionMS              int64
	maxEntries               int
	maxBytes                 uint64
	fingerprint              [sha256.Size]byte
	highWaterMS              int64
	receipts                 map[ID]Receipt
	groups                   map[GroupID]*groupReceiptRows
	contributions            map[ContribID]ID
	deadlines                deadlineIndex
	bytes                    uint64
	admissionReject          uint64
	replicationCapacityStall uint64
	unknownLookups           uint64
	highWaterSink            ClockHighWaterSink
	highWaterFault           error
}

// groupReceiptRows binds every currently retained item position to one
// logical call. Expired positions may disappear independently, but a live
// GroupID cannot be reused for a different call or item at that position.
// Its lookup keys duplicate receipt fields already charged to the logical
// byte ledger; MaxEntries bounds this map's overhead, like deadlineIndex.
type groupReceiptRows struct {
	count uint32
	items map[uint32]ID
}

func New(config Config) (*Store, error) {
	if config.Epoch == (Epoch{}) || config.MaxEntries <= 0 || config.MaxBytes == 0 ||
		config.Retention < minRetention || config.Retention > maxRetention ||
		config.Retention%time.Millisecond != 0 {
		return nil, ErrInvalidConfig
	}
	var highWaterMS int64
	if !config.ClockHighWater.IsZero() {
		highWaterMS = config.ClockHighWater.UnixMilli()
		if highWaterMS < 0 {
			return nil, ErrInvalidConfig
		}
	}
	retentionMS := int64(config.Retention / time.Millisecond)
	var fingerprintData [8 * 3]byte
	binary.BigEndian.PutUint64(fingerprintData[:8], uint64(retentionMS))
	binary.BigEndian.PutUint64(fingerprintData[8:16], uint64(config.MaxEntries))
	binary.BigEndian.PutUint64(fingerprintData[16:24], config.MaxBytes)
	h := sha256.New()
	_, _ = h.Write([]byte("lantern-receipt-policy-v1\x00"))
	_, _ = h.Write(fingerprintData[:])
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], h.Sum(nil))
	return &Store{
		epoch:         config.Epoch,
		retentionMS:   retentionMS,
		maxEntries:    config.MaxEntries,
		maxBytes:      config.MaxBytes,
		fingerprint:   fingerprint,
		highWaterMS:   highWaterMS,
		receipts:      make(map[ID]Receipt),
		groups:        make(map[GroupID]*groupReceiptRows),
		contributions: make(map[ContribID]ID),
		deadlines:     newDeadlineIndex(),
	}, nil
}

// NewWithClockHighWaterSink binds a synchronous persistence boundary before
// any Store operation is visible. New remains available for detached tests;
// a durable serving owner must also prove that the sink resumes the same
// epoch, policy, and WAL cut before using this Store.
func NewWithClockHighWaterSink(config Config, sink ClockHighWaterSink) (*Store, error) {
	s, err := New(config)
	if err != nil {
		return nil, err
	}
	if err := s.attachClockHighWaterSink(sink); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) attachClockHighWaterSink(sink ClockHighWaterSink) error {
	if sink == nil {
		return ErrInvalidConfig
	}
	if err := sink(s.highWaterMS); err != nil {
		return errors.Join(ErrHighWaterPersistence, err)
	}
	s.highWaterSink = sink
	return nil
}

func (s *Store) Epoch() Epoch { return s.epoch }

func (s *Store) PolicyFingerprint() [sha256.Size]byte { return s.fingerprint }

// Begin takes the receipt lock for one in-memory bookkeeping attempt. The
// caller must defer Abort and call Commit only after Classify, Reserve, Stage,
// and a successful external WAL commit. The observed clock high-water
// advances even when the attempt aborts; a future durable integration must
// preserve this metadata across restart or rotate the active epoch.
func (s *Store) Begin(now time.Time) (*Tx, error) {
	s.mu.Lock()
	effective, err := s.advanceLocked(now)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.expireLocked(effective)
	return &Tx{store: s, effectiveMS: effective}, nil
}

// ClockHighWaterMillis returns the monotonic wall sampled by Begin. The
// transaction remains the single owner of the Store lock, so callers can use
// this value to align an enclosing commit clock without a second Store read.
func (tx *Tx) ClockHighWaterMillis() (int64, error) {
	if tx == nil || tx.closed {
		return 0, ErrTransactionState
	}
	return tx.effectiveMS, nil
}

// Lookup is read-only with respect to live receipts. It advances the clock
// high-water and evicts only receipts already past their own deadline. A
// retained exact old-epoch receipt may answer Confirmed, but an absent
// old-epoch ID is never executable or reported NotYetObserved.
func (s *Store) Lookup(id ID, now time.Time) (Status, Receipt, error) {
	_, observations, err := s.ObserveMany([]ID{id}, now)
	if err != nil {
		return 0, Receipt{}, err
	}
	return observations[0].Status, observations[0].Receipt, nil
}

// ObserveMany advances and persists the Store clock once, expires rows once,
// and returns request-index-aligned observations. An empty ID list is valid
// and is used by capability preflight to sample the authoritative clock.
func (s *Store) ObserveMany(ids []ID, now time.Time) (time.Time, []Observation, error) {
	type identity struct {
		epoch  Epoch
		issued int64
	}
	identities := make([]identity, len(ids))
	for i, id := range ids {
		epoch, issued, err := id.parts()
		if err != nil {
			return time.Time{}, nil, err
		}
		identities[i] = identity{epoch: epoch, issued: issued}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	effective, err := s.advanceLocked(now)
	if err != nil {
		return time.Time{}, nil, err
	}
	s.expireLocked(effective)

	observations := make([]Observation, len(ids))
	for i, id := range ids {
		// An exact retained receipt remains authoritative even after an epoch
		// rollover. It carries its own original deadline; the new active epoch
		// may have a different retention policy. Absence in a retired epoch
		// must still never authorize execution of that ID.
		if receipt, ok := s.receipts[id]; ok {
			if receipt.DeadlineMillis > effective {
				observations[i] = Observation{
					Status: Confirmed, Receipt: cloneReceipt(receipt),
				}
				continue
			}
			s.unknownLookups++
			observations[i].Status = NoLongerProvable
			continue
		}
		identity := identities[i]
		if identity.issued > math.MaxInt64-s.retentionMS ||
			tooFarFuture(identity.issued, effective) {
			return time.Time{}, nil, ErrInvalidID
		}
		if identity.issued+s.retentionMS <= effective || identity.epoch != s.epoch {
			s.unknownLookups++
			observations[i].Status = NoLongerProvable
			continue
		}
		observations[i].Status = NotYetObserved
	}
	return time.UnixMilli(effective), observations, nil
}

func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := Stats{
		Entries:                   len(s.receipts),
		Bytes:                     s.bytes,
		HighWaterMillis:           s.highWaterMS,
		LocalAdmissionRejects:     s.admissionReject,
		ReplicationCapacityStalls: s.replicationCapacityStall,
		NoLongerProvableLookups:   s.unknownLookups,
	}
	stats.OldestDeadlineMillis = s.deadlines.oldestMillis()
	return stats
}

func (s *Store) advanceLocked(now time.Time) (int64, error) {
	if s.highWaterFault != nil {
		return 0, s.highWaterFault
	}
	ms := now.UnixMilli()
	if ms < 0 {
		return 0, ErrInvalidClock
	}
	if ms > s.highWaterMS {
		if s.highWaterSink != nil {
			if err := s.highWaterSink(ms); err != nil {
				s.highWaterFault = errors.Join(ErrHighWaterPersistence, err)
				return 0, s.highWaterFault
			}
		}
		s.highWaterMS = ms
	}
	return s.highWaterMS, nil
}

func tooFarFuture(issued, now int64) bool {
	return issued > now && issued-now > freshWindowMS
}

func (s *Store) expireLocked(nowMS int64) {
	for s.deadlines.Len() > 0 && s.deadlines.oldestMillis() <= nowMS {
		expired := s.deadlines.popOldest()
		r, ok := s.receipts[expired.id]
		if !ok {
			panic("mutationreceipt: deadline heap missing receipt")
		}
		delete(s.receipts, expired.id)
		s.bytes -= r.cost()
		if group := s.groups[r.Group]; group != nil {
			if boundID, bound := group.items[r.Index]; bound && boundID == r.ID {
				delete(group.items, r.Index)
				if len(group.items) == 0 {
					delete(s.groups, r.Group)
				}
			}
		}
		if r.HasContrib {
			delete(s.contributions, r.ContribID)
		}
	}
}

// Tx serializes classification, capacity reservation, hidden staging, and
// rollback. It intentionally holds Store.mu until Commit or Abort. This only
// protects Store's own data; it cannot form an atomic graph/log publication.
type Tx struct {
	store       *Store
	effectiveMS int64
	intents     []Intent
	staged      []Receipt
	applied     int
	groupAdded  bool
	mode        txMode
	closed      bool
}

type txMode uint8

const (
	txUnclassified txMode = iota
	txFresh
	txDuplicate
	txReserved
	txStaged
)

// Classification describes whether every item is fresh or every item is a
// matching duplicate. A mixed known/unknown call is never partially executed.
type Classification uint8

const (
	Fresh Classification = iota + 1
	Duplicate
)

// Classify checks all IDs and intents before any capacity decision. For a
// matching duplicate it returns deep copies of the exact original results;
// a caller sends those without staging another graph mutation. For Fresh it
// returns nil receipts; the caller may stage graph results and call Reserve.
func (tx *Tx) Classify(intents []Intent) (Classification, []Receipt, error) {
	if tx.closed || tx.mode != txUnclassified {
		return 0, nil, ErrTransactionState
	}
	if len(intents) == 0 || uint64(len(intents)) > math.MaxUint32 {
		return 0, nil, ErrInvalidBatch
	}
	group := intents[0].Group
	if group == (GroupID{}) {
		return 0, nil, ErrInvalidBatch
	}
	seenID := make(map[ID]struct{}, len(intents))
	seenContrib := make(map[ContribID]ID)
	existing := make([]Receipt, len(intents))
	known := 0
	stale := false
	for i, item := range intents {
		if item.Group != group || item.Count != uint32(len(intents)) || item.Index != uint32(i) ||
			item.Kind < PutVertex || item.Kind > DeleteEdge ||
			(item.Kind == AddEdge) != item.HasContrib ||
			(item.HasContrib && item.ContribID == (ContribID{})) ||
			(!item.HasContrib && item.ContribID != (ContribID{})) {
			return 0, nil, ErrInvalidBatch
		}
		if _, duplicate := seenID[item.ID]; duplicate {
			return 0, nil, ErrInvalidBatch
		}
		seenID[item.ID] = struct{}{}
		if item.HasContrib {
			if other, used := seenContrib[item.ContribID]; used && other != item.ID {
				return 0, nil, ErrContributionConflict
			}
			seenContrib[item.ContribID] = item.ID
		}
		epoch, issued, err := item.ID.parts()
		if err != nil {
			return 0, nil, err
		}
		if issued > math.MaxInt64-tx.store.retentionMS || tooFarFuture(issued, tx.effectiveMS) {
			return 0, nil, ErrInvalidID
		}
		if epoch != tx.store.epoch || issued+tx.store.retentionMS <= tx.effectiveMS {
			return 0, nil, ErrNoLongerProvable
		}
		if r, ok := tx.store.receipts[item.ID]; ok {
			if r.Intent != item {
				return 0, nil, ErrIntentConflict
			}
			existing[i] = cloneReceipt(r)
			known++
		} else if tx.effectiveMS-issued > freshWindowMS {
			stale = true
		}
	}
	if rows := tx.store.groups[group]; rows != nil {
		if rows.count != uint32(len(intents)) {
			return 0, nil, ErrIntentConflict
		}
		for i, item := range intents {
			if priorID, exists := rows.items[uint32(i)]; exists && priorID != item.ID {
				return 0, nil, ErrIntentConflict
			}
		}
	}
	if known == len(intents) {
		tx.mode = txDuplicate
		return Duplicate, existing, nil
	}
	if stale {
		return 0, nil, ErrNotFresh
	}
	if known != 0 {
		return 0, nil, ErrPartialEnvelope
	}
	if _, used := tx.store.groups[group]; used {
		return 0, nil, ErrIntentConflict
	}
	for contrib, id := range seenContrib {
		if old, bound := tx.store.contributions[contrib]; bound && old != id {
			return 0, nil, ErrContributionConflict
		}
	}
	tx.intents = append([]Intent(nil), intents...)
	tx.mode = txFresh
	return Fresh, nil, nil
}

// PrepareCommitted validates and reserves the live receipt rows from an
// already-committed replication envelope. acceptedAtMillis is the trusted
// origin acceptance timestamp, after the caller has bounded its clock skew.
// IDs must have been fresh at that origin cut, but delayed delivery is not
// judged against the follower's current clock. The old-side bound additionally
// admits the HLC's maximum forward skew because the origin can accept an ID
// against its Store wall before stamping the mutation from an observed clock
// floor; the future-side bound remains the ordinary freshness window. Expired
// rows are validated but omitted, while exact live duplicates require no
// additional capacity.
// Conflicts and capacity failures leave the transaction ready only for Abort.
// Like Begin, a validated trusted acceptance time advances the persisted
// clock high-water even when later admission fails or the transaction aborts.
//
// A successful call moves the transaction directly to the reserved state;
// the caller must call Stage before its external WAL commit.
func (tx *Tx) PrepareCommitted(receipts []Receipt, acceptedAtMillis int64) error {
	if tx.closed || tx.mode != txUnclassified {
		return ErrTransactionState
	}
	s := tx.store
	group, seenContrib, err := s.validateCommitted(receipts, acceptedAtMillis)
	if err != nil {
		return err
	}
	effective, err := s.advanceLocked(time.UnixMilli(acceptedAtMillis))
	if err != nil {
		return err
	}
	tx.effectiveMS = effective
	s.expireLocked(effective)
	liveMissing := make([]Receipt, 0, len(receipts))
	liveKnown := 0
	for _, receipt := range receipts {
		item := receipt.Intent
		if receipt.DeadlineMillis <= tx.effectiveMS {
			continue
		}
		if prior, ok := s.receipts[item.ID]; ok {
			if prior.Intent != item || prior.DeadlineMillis != receipt.DeadlineMillis ||
				!bytes.Equal(prior.Result, receipt.Result) {
				return ErrIntentConflict
			}
			liveKnown++
			continue
		}
		liveMissing = append(liveMissing, cloneReceipt(receipt))
	}
	if rows := s.groups[group]; rows != nil {
		if rows.count != uint32(len(receipts)) {
			return ErrIntentConflict
		}
		for i, receipt := range receipts {
			if boundID, exists := rows.items[uint32(i)]; exists && boundID != receipt.ID {
				return ErrIntentConflict
			}
		}
	}
	if liveKnown != 0 && len(liveMissing) != 0 {
		return ErrPartialEnvelope
	}
	if liveKnown == 0 && len(liveMissing) != 0 {
		if _, used := s.groups[group]; used {
			return ErrIntentConflict
		}
		for contrib, id := range seenContrib {
			if old, bound := s.contributions[contrib]; bound && old != id {
				return ErrContributionConflict
			}
		}
		if len(liveMissing) > s.maxEntries-len(s.receipts) {
			s.replicationCapacityStall++
			return ErrCapacity
		}
		var additional uint64
		for _, receipt := range liveMissing {
			cost := receipt.cost()
			if cost > s.maxBytes || additional > s.maxBytes-cost {
				s.replicationCapacityStall++
				return ErrCapacity
			}
			additional += cost
		}
		if additional > s.maxBytes-s.bytes {
			s.replicationCapacityStall++
			return ErrCapacity
		}
	}
	tx.intents = make([]Intent, len(liveMissing))
	for i, receipt := range liveMissing {
		tx.intents[i] = receipt.Intent
	}
	tx.staged = liveMissing
	tx.mode = txReserved
	return nil
}

// ValidateCommitted checks the immutable epoch, policy-derived deadlines,
// logical-call shape, and origin-time freshness of a committed envelope. It
// does not inspect or mutate live Store state, so a follower can reject an
// invalid future sequence before admitting it to its pending queue.
func (s *Store) ValidateCommitted(receipts []Receipt, acceptedAtMillis int64) error {
	_, _, err := s.validateCommitted(receipts, acceptedAtMillis)
	return err
}

func (s *Store) validateCommitted(receipts []Receipt, acceptedAtMillis int64) (GroupID, map[ContribID]ID, error) {
	if acceptedAtMillis < 0 || len(receipts) == 0 || uint64(len(receipts)) > math.MaxUint32 {
		return GroupID{}, nil, ErrInvalidBatch
	}
	group := receipts[0].Group
	if group == (GroupID{}) {
		return GroupID{}, nil, ErrInvalidBatch
	}
	seenID := make(map[ID]struct{}, len(receipts))
	seenContrib := make(map[ContribID]ID)
	for i, receipt := range receipts {
		item := receipt.Intent
		if item.Group != group || item.Count != uint32(len(receipts)) || item.Index != uint32(i) ||
			item.Kind < PutVertex || item.Kind > DeleteEdge ||
			(item.Kind == AddEdge) != item.HasContrib ||
			(item.HasContrib && item.ContribID == (ContribID{})) ||
			(!item.HasContrib && item.ContribID != (ContribID{})) {
			return GroupID{}, nil, ErrInvalidBatch
		}
		if _, duplicate := seenID[item.ID]; duplicate {
			return GroupID{}, nil, ErrInvalidBatch
		}
		seenID[item.ID] = struct{}{}
		if item.HasContrib {
			if other, used := seenContrib[item.ContribID]; used && other != item.ID {
				return GroupID{}, nil, ErrContributionConflict
			}
			seenContrib[item.ContribID] = item.ID
		}
		epoch, issued, err := item.ID.parts()
		if err != nil {
			return GroupID{}, nil, err
		}
		if epoch != s.epoch || issued > math.MaxInt64-s.retentionMS ||
			tooFarFuture(issued, acceptedAtMillis) ||
			(issued <= acceptedAtMillis && acceptedAtMillis-issued > committedPastWindowMS) ||
			receipt.DeadlineMillis != issued+s.retentionMS {
			return GroupID{}, nil, ErrInvalidBatch
		}
	}
	return group, seenContrib, nil
}

// Reserve copies all original results and proves that the entire new batch
// fits without evicting any live receipt. It does not publish a result; Stage
// must run before the external WAL commit while this transaction holds mu.
func (tx *Tx) Reserve(results [][]byte) error {
	if tx.closed || tx.mode != txFresh {
		return ErrTransactionState
	}
	if len(results) != len(tx.intents) {
		return ErrInvalidBatch
	}
	s := tx.store
	if len(tx.intents) > s.maxEntries-len(s.receipts) {
		s.admissionReject++
		return ErrCapacity
	}
	var additional uint64
	for i, result := range results {
		cost := receiptFixedBytes + uint64(len(result))
		if tx.intents[i].HasContrib {
			cost += bindingBytes
		}
		if cost > s.maxBytes || additional > s.maxBytes-cost {
			s.admissionReject++
			return ErrCapacity
		}
		additional += cost
	}
	if additional > s.maxBytes-s.bytes {
		s.admissionReject++
		return ErrCapacity
	}
	tx.staged = make([]Receipt, len(results))
	for i, result := range results {
		_, issued, _ := tx.intents[i].ID.parts()
		tx.staged[i] = Receipt{
			Intent:         tx.intents[i],
			Result:         append([]byte(nil), result...),
			DeadlineMillis: issued + s.retentionMS,
		}
	}
	tx.mode = txReserved
	return nil
}

// ReplaceReservedResults fills an already capacity-reserved batch without
// changing any result length or allocating. Coordinators with graph-derived
// one-byte results use this after capacity admission but before graph staging
// is made durable.
func (tx *Tx) ReplaceReservedResults(results [][]byte) error {
	if tx.closed || tx.mode != txReserved || len(results) != len(tx.staged) {
		return ErrTransactionState
	}
	for i, result := range results {
		if len(result) != len(tx.staged[i].Result) {
			return ErrInvalidBatch
		}
	}
	for i, result := range results {
		copy(tx.staged[i].Result, result)
	}
	return nil
}

// Stage inserts all new receipts into the private, locked state before an
// external WAL commit. It may allocate; no Store reader can see the rows while
// the transaction holds mu. A failed WAL call must be followed by Abort.
func (tx *Tx) Stage() error {
	if tx.closed || tx.mode != txReserved {
		return ErrTransactionState
	}
	s := tx.store
	tx.mode = txStaged
	if len(tx.staged) == 0 {
		return nil
	}
	group := tx.staged[0].Group
	s.groups[group] = &groupReceiptRows{count: tx.intents[0].Count, items: make(map[uint32]ID, len(tx.staged))}
	tx.groupAdded = true
	for _, r := range tx.staged {
		// Ledger updates happen before the potentially allocating index/map
		// changes. A deferred Abort can unwind this item even if Stage panics.
		s.bytes += r.cost()
		tx.applied++
		s.deadlines.insert(deadlineEntry{id: r.ID, deadlineMS: r.DeadlineMillis})
		s.receipts[r.ID] = r
		s.groups[group].items[r.Index] = r.ID
		if r.HasContrib {
			s.contributions[r.ContribID] = r.ID
		}
	}
	return nil
}

// StagedReceipts returns owned copies for an external WAL envelope. It is
// available only after Stage and before Commit/Abort, while the Store lock
// still hides the tentative rows. The caller must serialize these receipts
// with the matching graph mutation before allowing WAL publication.
func (tx *Tx) StagedReceipts() ([]Receipt, error) {
	if tx.closed || tx.mode != txStaged || tx.applied != len(tx.staged) {
		return nil, ErrTransactionState
	}
	result := make([]Receipt, len(tx.staged))
	for i, receipt := range tx.staged {
		result[i] = cloneReceipt(receipt)
	}
	return result, nil
}

// Commit has no Store mutation or allocation: releasing mu makes all staged
// rows visible at once to Store readers. Call only after the external WAL
// commits. The caller must separately coordinate graph/log visibility under
// its outer cut gate, and replay committed WAL records before serving after
// a crash. Commit cannot prove those external obligations itself.
func (tx *Tx) Commit() {
	if tx.closed || tx.mode != txStaged || tx.applied != len(tx.staged) {
		panic(ErrTransactionState)
	}
	tx.close()
}

// Abort removes every staged receipt and reverse binding, and restores the
// deadline index and byte ledger in O(touched log n). It leaves expiry of
// already-dead receipts and monotonic clock observation intact. After an
// ambiguous WAL error, this rollback does not prove non-commit; the caller
// must fail-stop/quarantine until recovery establishes the committed cut. It
// is safe to defer and is idempotent after Commit.
func (tx *Tx) Abort() {
	if tx != nil && !tx.closed {
		if tx.mode == txStaged {
			s := tx.store
			if tx.groupAdded {
				delete(s.groups, tx.staged[0].Group)
			}
			for i := tx.applied - 1; i >= 0; i-- {
				r := tx.staged[i]
				s.deadlines.remove(r.ID)
				delete(s.receipts, r.ID)
				if r.HasContrib {
					delete(s.contributions, r.ContribID)
				}
				s.bytes -= r.cost()
			}
		}
		tx.close()
	}
}

func (tx *Tx) close() {
	tx.closed = true
	tx.intents = nil
	tx.staged = nil
	tx.store.mu.Unlock()
}
