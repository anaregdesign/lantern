package mutationreceipt

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

var testStart = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func testStore(t testing.TB, maxEntries int, maxBytes uint64) *Store {
	t.Helper()
	s, err := New(Config{
		Epoch:      Epoch{1},
		Retention:  time.Hour,
		MaxEntries: maxEntries,
		MaxBytes:   maxBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testIntent(t testing.TB, seed byte, issued time.Time, group GroupID, index, count uint32) Intent {
	t.Helper()
	id, err := NewID(Epoch{1}, issued, [24]byte{seed})
	if err != nil {
		t.Fatal(err)
	}
	return Intent{ID: id, Group: group, Index: index, Count: count, Kind: PutVertex, Digest: IntentDigest([]byte{seed})}
}

func commitTestBatch(t testing.TB, s *Store, now time.Time, intents []Intent, results [][]byte) {
	t.Helper()
	tx, err := s.Begin(now)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	class, _, err := tx.Classify(intents)
	if err != nil || class != Fresh {
		t.Fatalf("Classify = %v, %v, want Fresh", class, err)
	}
	if err := tx.Reserve(results); err != nil {
		t.Fatal(err)
	}
	tx.Install()
}

func TestStoreRetainsOriginalBatchResultsAndRejectsChangedIntent(t *testing.T) {
	s := testStore(t, 2, 1000)
	group := GroupID{8}
	items := []Intent{
		testIntent(t, 1, testStart, group, 0, 2),
		testIntent(t, 2, testStart, group, 1, 2),
	}
	if status, _, err := s.Lookup(items[0].ID, testStart); err != nil || status != NotYetObserved {
		t.Fatalf("new ID lookup = %v, %v", status, err)
	}
	results := [][]byte{[]byte("applied"), []byte("condition-not-met")}
	commitTestBatch(t, s, testStart, items, results)
	results[0][0] = 'X'
	got := s.Stats()
	if got.Entries != 2 || got.OldestDeadlineMillis != testStart.Add(time.Hour).UnixMilli() {
		t.Fatalf("stats = %+v", got)
	}
	if got.Bytes != 2*receiptFixedBytes+uint64(len(results[0])+len(results[1])) {
		t.Fatalf("accounted bytes = %d", got.Bytes)
	}
	tx, err := s.Begin(testStart.Add(10 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	class, receipts, err := tx.Classify(items)
	if err != nil || class != Duplicate || string(receipts[0].Result) != "applied" ||
		string(receipts[1].Result) != "condition-not-met" {
		t.Fatalf("duplicate = %v, %+v, %v", class, receipts, err)
	}
	receipts[0].Result[0] = 'Y'
	tx.Abort()
	if status, receipt, err := s.Lookup(items[0].ID, testStart.Add(11*time.Minute)); err != nil || status != Confirmed || string(receipt.Result) != "applied" {
		t.Fatalf("lookup after caller mutation = %v, %+v, %v", status, receipt, err)
	}
	changed := append([]Intent(nil), items...)
	changed[1].Digest = IntentDigest([]byte("different value"))
	tx, _ = s.Begin(testStart.Add(12 * time.Minute))
	_, _, err = tx.Classify(changed)
	tx.Abort()
	if !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("different semantic intent = %v", err)
	}
	changed = append([]Intent(nil), items...)
	changed[1].Group = GroupID{9}
	tx, _ = s.Begin(testStart.Add(12 * time.Minute))
	_, _, err = tx.Classify(changed)
	tx.Abort()
	if !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("different logical-call grouping = %v", err)
	}
	for i := range changed {
		changed[i].Group = GroupID{9}
	}
	tx, _ = s.Begin(testStart.Add(12 * time.Minute))
	_, _, err = tx.Classify(changed)
	tx.Abort()
	if !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("same IDs reused in another valid group = %v", err)
	}
}

func TestStoreExpiryAndHighWaterPreventOldIDReexecution(t *testing.T) {
	s := testStore(t, 1, 1000)
	item := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	item.Kind = AddEdge
	item.HasContrib = true
	item.ContribID = ContribID{7}
	commitTestBatch(t, s, testStart, []Intent{item}, [][]byte{[]byte("weight: 3")})
	deadline := testStart.Add(time.Hour)
	if status, _, err := s.Lookup(item.ID, deadline.Add(-time.Millisecond)); err != nil || status != Confirmed {
		t.Fatalf("just before deadline = %v, %v", status, err)
	}
	if status, _, err := s.Lookup(item.ID, deadline); err != nil || status != NoLongerProvable {
		t.Fatalf("at deadline = %v, %v", status, err)
	}
	if got := s.Stats(); got.Entries != 0 || got.Bytes != 0 || got.OldestDeadlineMillis != 0 {
		t.Fatalf("expired receipt remains retained: %+v", got)
	}
	// A backwards wall-clock reading cannot reopen the fresh or TTL window.
	tx, err := s.Begin(testStart.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = tx.Classify([]Intent{item})
	tx.Abort()
	if !errors.Is(err, ErrNoLongerProvable) {
		t.Fatalf("backward-clock retry = %v", err)
	}
	if got := s.Stats().HighWaterMillis; got != deadline.UnixMilli() {
		t.Fatalf("clock high-water regressed to %d", got)
	}
	// The Add reverse binding expires with its receipt; a newly minted ID
	// may bind the same ContribID only after that deadline.
	newItem := testIntent(t, 2, deadline, GroupID{2}, 0, 1)
	newItem.Kind = AddEdge
	newItem.HasContrib, newItem.ContribID = true, item.ContribID
	commitTestBatch(t, s, deadline, []Intent{newItem}, [][]byte{[]byte("weight: 4")})
	if got := s.Stats(); got.Entries != 1 || got.Bytes != receiptFixedBytes+bindingBytes+9 {
		t.Fatalf("new binding stats = %+v", got)
	}
}

func TestStoreRejectsCapacityBeforeAnyReceiptInstallation(t *testing.T) {
	s := testStore(t, 2, 2*receiptFixedBytes+1)
	items := []Intent{
		testIntent(t, 1, testStart, GroupID{1}, 0, 2),
		testIntent(t, 2, testStart, GroupID{1}, 1, 2),
	}
	tx, _ := s.Begin(testStart)
	if class, _, err := tx.Classify(items); err != nil || class != Fresh {
		t.Fatalf("Classify = %v, %v", class, err)
	}
	if err := tx.Reserve([][]byte{[]byte("a"), []byte("b")}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("all-or-nothing byte cap error = %v", err)
	}
	tx.Abort()
	if got := s.Stats(); got.Entries != 0 || got.Bytes != 0 || got.LocalAdmissionRejects != 1 {
		t.Fatalf("rejected batch changed live state: %+v", got)
	}
	one := testIntent(t, 3, testStart, GroupID{2}, 0, 1)
	commitTestBatch(t, s, testStart, []Intent{one}, [][]byte{[]byte("a")})
	if got := s.Stats().Bytes; got != receiptFixedBytes+1 {
		t.Fatalf("bytes after admitted singleton = %d", got)
	}
	// A full entry cap is also detected before publication.
	entryStore := testStore(t, 1, 1000)
	commitTestBatch(t, entryStore, testStart, []Intent{one}, [][]byte{nil})
	two := testIntent(t, 4, testStart, GroupID{3}, 0, 1)
	tx, _ = entryStore.Begin(testStart)
	_, _, _ = tx.Classify([]Intent{two})
	err := tx.Reserve([][]byte{nil})
	tx.Abort()
	if !errors.Is(err, ErrCapacity) || entryStore.Stats().Entries != 1 {
		t.Fatalf("entry cap = %v, stats = %+v", err, entryStore.Stats())
	}
}

func TestStoreContributionBindingAndBatchShape(t *testing.T) {
	s := testStore(t, 3, 1000)
	a := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	a.Kind = AddEdge
	a.HasContrib, a.ContribID = true, ContribID{9}
	commitTestBatch(t, s, testStart, []Intent{a}, [][]byte{nil})
	b := testIntent(t, 2, testStart, GroupID{2}, 0, 1)
	b.Kind = AddEdge
	b.HasContrib, b.ContribID = true, a.ContribID
	tx, _ := s.Begin(testStart)
	_, _, err := tx.Classify([]Intent{b})
	tx.Abort()
	if !errors.Is(err, ErrContributionConflict) {
		t.Fatalf("live reverse-index collision = %v", err)
	}
	invalidBatches := [][]Intent{
		{{ID: b.ID, Group: b.Group, Index: 1, Count: 1, Kind: AddEdge, Digest: b.Digest, HasContrib: true, ContribID: b.ContribID}},
		{b, b},
		{{ID: b.ID, Group: GroupID{}, Count: 1}},
		{{ID: b.ID, Group: b.Group, Count: 1, Kind: AddEdge, Digest: b.Digest}},
		{{ID: b.ID, Group: b.Group, Count: 1, Kind: PutEdge, Digest: b.Digest, HasContrib: true, ContribID: b.ContribID}},
	}
	for i, invalid := range invalidBatches {
		tx, _ := s.Begin(testStart)
		_, _, err := tx.Classify(invalid)
		tx.Abort()
		if !errors.Is(err, ErrInvalidBatch) {
			t.Fatalf("invalid batch %d = %v", i, err)
		}
	}
	c := testIntent(t, 3, testStart, GroupID{3}, 0, 2)
	d := testIntent(t, 4, testStart, GroupID{3}, 1, 2)
	c.Kind, d.Kind = AddEdge, AddEdge
	c.HasContrib, d.HasContrib = true, true
	c.ContribID, d.ContribID = ContribID{3}, ContribID{3}
	tx, _ = s.Begin(testStart)
	_, _, err = tx.Classify([]Intent{c, d})
	tx.Abort()
	if !errors.Is(err, ErrContributionConflict) {
		t.Fatalf("within-batch reverse-index collision = %v", err)
	}
}

func TestStoreFreshnessEpochAndPolicy(t *testing.T) {
	s := testStore(t, 1, 1000)
	group := GroupID{1}
	old := testIntent(t, 1, testStart, group, 0, 1)
	future := testIntent(t, 2, testStart.Add(5*time.Minute+time.Millisecond), group, 0, 1)
	otherEpochID, _ := NewID(Epoch{2}, testStart, [24]byte{3})
	otherEpoch := Intent{ID: otherEpochID, Group: group, Count: 1, Kind: PutVertex}
	cases := []struct {
		name string
		item Intent
		now  time.Time
		want error
	}{
		{"future more than five minutes", future, testStart, ErrInvalidID},
		{"absent older than five minutes", old, testStart.Add(5*time.Minute + time.Millisecond), ErrNotFresh},
		{"expired", old, testStart.Add(time.Hour), ErrNoLongerProvable},
		{"retired epoch", otherEpoch, testStart, ErrNoLongerProvable},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			freshStore := testStore(t, 1, 1000)
			tx, _ := freshStore.Begin(tt.now)
			_, _, err := tx.Classify([]Intent{tt.item})
			tx.Abort()
			if !errors.Is(err, tt.want) {
				t.Fatalf("Classify = %v, want %v", err, tt.want)
			}
		})
	}
	boundary := testIntent(t, 4, testStart.Add(5*time.Minute), group, 0, 1)
	tx, _ := s.Begin(testStart)
	if class, _, err := tx.Classify([]Intent{boundary}); err != nil || class != Fresh {
		t.Fatalf("exact five-minute future boundary = %v, %v", class, err)
	}
	tx.Abort()
	if status, _, err := s.Lookup(otherEpochID, testStart); err != nil || status != NoLongerProvable {
		t.Fatalf("absent retired epoch status = %v, %v", status, err)
	}
	if status, _, err := s.Lookup(old.ID, testStart.Add(6*time.Minute)); err != nil || status != NotYetObserved {
		t.Fatalf("absent in-horizon status = %v, %v", status, err)
	}
	if s.Stats().NoLongerProvableLookups != 1 {
		t.Fatalf("no-longer-provable lookup count = %+v", s.Stats())
	}
	same, _ := New(Config{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 1, MaxBytes: 1000})
	different, _ := New(Config{Epoch: Epoch{1}, Retention: 2 * time.Hour, MaxEntries: 1, MaxBytes: 1000})
	if s.PolicyFingerprint() != same.PolicyFingerprint() || s.PolicyFingerprint() == different.PolicyFingerprint() {
		t.Fatal("policy fingerprint does not track immutable retention")
	}
	for _, config := range []Config{
		{Epoch: Epoch{}, Retention: time.Hour, MaxEntries: 1, MaxBytes: 1000},
		{Epoch: Epoch{1}, Retention: time.Minute, MaxEntries: 1, MaxBytes: 1000},
		{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 0, MaxBytes: 1000},
		{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 1, MaxBytes: 0},
		{Epoch: Epoch{1}, Retention: time.Hour + time.Nanosecond, MaxEntries: 1, MaxBytes: 1000},
	} {
		if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("accepted invalid config %+v: %v", config, err)
		}
	}
}

func TestStoreAbortDoesNotPublish(t *testing.T) {
	s := testStore(t, 1, 1000)
	item := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	tx, _ := s.Begin(testStart)
	_, _, _ = tx.Classify([]Intent{item})
	if err := tx.Reserve([][]byte{[]byte("would-have-committed")}); err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	tx.Abort()
	if status, _, err := s.Lookup(item.ID, testStart); err != nil || status != NotYetObserved || s.Stats().Entries != 0 {
		t.Fatalf("aborted result became visible: %v, %v, %+v", status, err, s.Stats())
	}
	commitTestBatch(t, s, testStart, []Intent{item}, [][]byte{[]byte("committed")})
}

func TestStoreRejectsPartiallyKnownEnvelope(t *testing.T) {
	s := testStore(t, 2, 1000)
	items := []Intent{
		testIntent(t, 1, testStart, GroupID{1}, 0, 2),
		testIntent(t, 2, testStart, GroupID{1}, 1, 2),
	}
	commitTestBatch(t, s, testStart, items, [][]byte{nil, nil})
	// Simulate an incomplete future restore or replication bug. A recipient
	// must fail closed rather than re-execute the absent half of this call.
	s.mu.Lock()
	delete(s.receipts, items[1].ID)
	s.mu.Unlock()
	tx, _ := s.Begin(testStart.Add(time.Minute))
	_, _, err := tx.Classify(items)
	tx.Abort()
	if !errors.Is(err, ErrPartialEnvelope) {
		t.Fatalf("partially retained envelope = %v", err)
	}
}

func TestStoreRestoredClockHighWaterRejectsBackwardExpiry(t *testing.T) {
	s, err := New(Config{
		Epoch:          Epoch{1},
		Retention:      time.Hour,
		MaxEntries:     1,
		MaxBytes:       1000,
		ClockHighWater: testStart.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	old := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	tx, err := s.Begin(testStart.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = tx.Classify([]Intent{old})
	tx.Abort()
	if !errors.Is(err, ErrNoLongerProvable) || s.Stats().HighWaterMillis != testStart.Add(time.Hour).UnixMilli() {
		t.Fatalf("restored high-water allowed old ID: %v, %+v", err, s.Stats())
	}
}

func BenchmarkStoreDuplicateLookup(b *testing.B) {
	s := testStore(b, 1, 1000)
	item := testIntent(b, 1, testStart, GroupID{1}, 0, 1)
	commitTestBatch(b, s, testStart, []Intent{item}, [][]byte{[]byte("condition-not-met")})
	now := testStart.Add(10 * time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := s.Begin(now)
		if err != nil {
			b.Fatal(err)
		}
		class, receipts, err := tx.Classify([]Intent{item})
		tx.Abort()
		if err != nil || class != Duplicate || len(receipts) != 1 {
			b.Fatal(fmt.Errorf("duplicate = %v, %v, %v", class, receipts, err))
		}
	}
}
