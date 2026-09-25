package mutationreceipt

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func retiredTestIntent(t testing.TB, epoch Epoch, seed byte, issued time.Time) Intent {
	t.Helper()
	id, err := NewID(epoch, issued, [24]byte{seed})
	if err != nil {
		t.Fatal(err)
	}
	return Intent{
		ID:     id,
		Group:  GroupID{seed},
		Count:  1,
		Kind:   PutVertex,
		Digest: IntentDigest([]byte{seed}),
	}
}

func retiredTestMember(
	t testing.TB,
	config Config,
	highWater time.Time,
	intents []Intent,
	results [][]byte,
) RetiredEpochSnapshot {
	t.Helper()
	if len(intents) != len(results) {
		t.Fatal("retired test member intent/result length mismatch")
	}
	store, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	for i, intent := range intents {
		_, issued, err := intent.ID.parts()
		if err != nil {
			t.Fatal(err)
		}
		commitTestBatch(t, store, time.UnixMilli(issued), []Intent{intent}, [][]byte{results[i]})
	}
	tx, err := store.Begin(highWater)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return RetiredEpochSnapshot{
		Policy: RetiredEpochPolicy{
			Epoch:      config.Epoch,
			Retention:  config.Retention,
			MaxEntries: config.MaxEntries,
			MaxBytes:   config.MaxBytes,
		},
		State: state,
	}
}

func cloneRetiredCatalogSnapshot(state RetiredCatalogSnapshot) RetiredCatalogSnapshot {
	state.Epochs = append([]RetiredEpochSnapshot(nil), state.Epochs...)
	for i := range state.Epochs {
		state.Epochs[i].State = cloneSnapshot(state.Epochs[i].State)
	}
	return state
}

func retiredCatalogFixture(t testing.TB) (RetiredCatalogConfig, RetiredCatalogSnapshot, []Intent) {
	t.Helper()
	highWater := testStart.Add(10 * time.Minute)
	first := retiredTestIntent(t, Epoch{2}, 1, testStart)
	first.Kind, first.HasContrib, first.ContribID = AddEdge, true, ContribID{1}
	second := retiredTestIntent(t, Epoch{2}, 2, testStart.Add(time.Minute))
	third := retiredTestIntent(t, Epoch{3}, 3, testStart.Add(2*time.Minute))
	epochTwo := retiredTestMember(t, Config{
		Epoch: Epoch{2}, Retention: 2 * time.Hour, MaxEntries: 4, MaxBytes: 4096,
	}, highWater, []Intent{first, second}, [][]byte{[]byte("first"), []byte("second")})
	epochThree := retiredTestMember(t, Config{
		Epoch: Epoch{3}, Retention: 3 * time.Hour, MaxEntries: 2, MaxBytes: 4096,
	}, highWater, []Intent{third}, [][]byte{[]byte("third")})
	return RetiredCatalogConfig{
			ActiveEpoch: Epoch{9}, MaxEntries: 3, MaxBytes: 4096, ClockHighWater: highWater,
		}, RetiredCatalogSnapshot{
			Version:              retiredCatalogSnapshotVersion,
			ClockHighWaterMillis: highWater.UnixMilli(),
			Epochs:               []RetiredEpochSnapshot{epochTwo, epochThree},
		}, []Intent{first, second, third}
}

func TestRetiredCatalogMultiEpochRoundTripAndCopySafety(t *testing.T) {
	config, state, intents := retiredCatalogFixture(t)
	canonical := cloneRetiredCatalogSnapshot(state)
	catalog, err := NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}

	state.Epochs[0].Policy.Retention = time.Hour
	state.Epochs[0].State.Receipts[0].Result[0] = 'X'
	for i, intent := range intents {
		status, receipt, err := catalog.Lookup(intent.ID, config.ClockHighWater)
		if err != nil || status != Confirmed ||
			string(receipt.Result) != []string{"first", "second", "third"}[i] {
			t.Fatalf("Lookup(%d) = %v, %q, %v", i, status, receipt.Result, err)
		}
		receipt.Result[0] = 'Y'
		status, receipt, err = catalog.Lookup(intent.ID, config.ClockHighWater)
		if err != nil || status != Confirmed ||
			string(receipt.Result) != []string{"first", "second", "third"}[i] {
			t.Fatalf("Lookup(%d) after result mutation = %v, %q, %v", i, status, receipt.Result, err)
		}
	}

	got, err := catalog.Snapshot(config.ClockHighWater)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, canonical) {
		t.Fatalf("catalog snapshot = %+v, want %+v", got, canonical)
	}
	got.Epochs[0].Policy.Retention = 30 * 24 * time.Hour
	got.Epochs[0].State.Receipts[0].Result[0] = 'Z'
	again, err := catalog.Snapshot(config.ClockHighWater)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, canonical) {
		t.Fatalf("caller mutated catalog snapshot: %+v", again)
	}

	restored, err := NewRetiredCatalogFromSnapshot(config, again)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := restored.Snapshot(config.ClockHighWater)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, canonical) {
		t.Fatalf("round-trip snapshot = %+v, want %+v", roundTrip, canonical)
	}
}

func TestRetiredCatalogLookupSemanticsAndExpiry(t *testing.T) {
	config, state, intents := retiredCatalogFixture(t)
	catalog, err := NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}

	absentKnown := retiredTestIntent(t, Epoch{2}, 8, testStart)
	absentUnknown := retiredTestIntent(t, Epoch{8}, 8, testStart)
	for name, id := range map[string]ID{
		"known retired epoch":   absentKnown.ID,
		"unknown retired epoch": absentUnknown.ID,
	} {
		t.Run(name, func(t *testing.T) {
			status, receipt, err := catalog.Lookup(id, config.ClockHighWater)
			if err != nil || status != NoLongerProvable || !reflect.DeepEqual(receipt, Receipt{}) {
				t.Fatalf("absent lookup = %v, %+v, %v", status, receipt, err)
			}
		})
	}
	if status, receipt, err := catalog.Lookup(ID{}, config.ClockHighWater); status != 0 ||
		!reflect.DeepEqual(receipt, Receipt{}) || !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid ID lookup = %v, %+v, %v", status, receipt, err)
	}
	active := retiredTestIntent(t, config.ActiveEpoch, 9, testStart)
	if status, receipt, err := catalog.Lookup(active.ID, config.ClockHighWater); status != 0 ||
		!reflect.DeepEqual(receipt, Receipt{}) || !errors.Is(err, ErrActiveEpochReceipt) {
		t.Fatalf("active-epoch lookup = %v, %+v, %v", status, receipt, err)
	}

	epochTwoDeadline := testStart.Add(2*time.Hour + time.Minute)
	if status, _, err := catalog.Lookup(intents[0].ID, epochTwoDeadline); err != nil ||
		status != NoLongerProvable {
		t.Fatalf("expired retired receipt = %v, %v", status, err)
	}
	if status, receipt, err := catalog.Lookup(intents[2].ID, epochTwoDeadline); err != nil ||
		status != Confirmed || string(receipt.Result) != "third" {
		t.Fatalf("still-live retired receipt = %v, %+v, %v", status, receipt, err)
	}
	snapshot, err := catalog.Snapshot(epochTwoDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Epochs) != 1 || snapshot.Epochs[0].Policy.Epoch != (Epoch{3}) ||
		len(snapshot.Epochs[0].State.Receipts) != 1 {
		t.Fatalf("expired epoch was not omitted: %+v", snapshot)
	}
	if snapshot.ClockHighWaterMillis != epochTwoDeadline.UnixMilli() ||
		snapshot.Epochs[0].State.ClockHighWaterMillis != epochTwoDeadline.UnixMilli() {
		t.Fatalf("snapshot high-water was not advanced: %+v", snapshot)
	}
}

func TestRetiredCatalogDoesNotShareActiveStoreIndexesOrCapacity(t *testing.T) {
	config, state, intents := retiredCatalogFixture(t)
	catalog, err := NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	active, err := New(Config{
		Epoch: config.ActiveEpoch, Retention: time.Hour, MaxEntries: 1, MaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	activeIntent := retiredTestIntent(t, config.ActiveEpoch, 7, config.ClockHighWater)
	activeIntent.Kind = AddEdge
	activeIntent.HasContrib = true
	activeIntent.ContribID = intents[0].ContribID
	activeIntent.Group = intents[0].Group
	commitTestBatch(t, active, config.ClockHighWater, []Intent{activeIntent}, [][]byte{[]byte("active")})
	if got := active.Stats(); got.Entries != 1 {
		t.Fatalf("retired rows consumed active Store capacity: %+v", got)
	}
	if status, receipt, err := catalog.Lookup(intents[0].ID, config.ClockHighWater); err != nil ||
		status != Confirmed || string(receipt.Result) != "first" {
		t.Fatalf("active Store changed retired evidence: %v, %+v, %v", status, receipt, err)
	}
}

func TestRetiredCatalogImportPrunesExpiredRowsAndRemainsRoundTrippable(t *testing.T) {
	snapshotHighWater := testStart.Add(31 * time.Minute)
	first := retiredTestIntent(t, Epoch{2}, 1, testStart)
	second := retiredTestIntent(t, Epoch{2}, 2, testStart.Add(30*time.Minute))
	member := retiredTestMember(t, Config{
		Epoch: Epoch{2}, Retention: time.Hour, MaxEntries: 2, MaxBytes: 4096,
	}, snapshotHighWater, []Intent{first, second}, [][]byte{[]byte("expired"), []byte("live")})
	state := RetiredCatalogSnapshot{
		Version:              retiredCatalogSnapshotVersion,
		ClockHighWaterMillis: snapshotHighWater.UnixMilli(),
		Epochs:               []RetiredEpochSnapshot{member},
	}
	effective := testStart.Add(time.Hour + time.Minute)
	config := RetiredCatalogConfig{
		ActiveEpoch: Epoch{9}, MaxEntries: 2, MaxBytes: 4096, ClockHighWater: effective,
	}
	catalog, err := NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, err := catalog.Lookup(first.ID, effective); err != nil || status != NoLongerProvable {
		t.Fatalf("expired imported row = %v, %v", status, err)
	}
	if status, receipt, err := catalog.Lookup(second.ID, effective); err != nil ||
		status != Confirmed || string(receipt.Result) != "live" {
		t.Fatalf("live imported row = %v, %+v, %v", status, receipt, err)
	}
	compacted, err := catalog.Snapshot(effective)
	if err != nil {
		t.Fatal(err)
	}
	if len(compacted.Epochs) != 1 || len(compacted.Epochs[0].State.Receipts) != 1 ||
		compacted.Epochs[0].State.Receipts[0].ID != second.ID {
		t.Fatalf("compacted snapshot = %+v", compacted)
	}
	if _, err := NewRetiredCatalogFromSnapshot(config, compacted); err != nil {
		t.Fatalf("compacted snapshot did not round trip: %v", err)
	}
}

func TestRetiredCatalogRejectsMalformedConflictingOrUnorderedImport(t *testing.T) {
	config, state, _ := retiredCatalogFixture(t)
	cases := []struct {
		name   string
		mutate func(*RetiredCatalogSnapshot)
		want   error
	}{
		{"version", func(s *RetiredCatalogSnapshot) { s.Version++ }, ErrInvalidRetiredCatalogSnapshot},
		{"negative clock", func(s *RetiredCatalogSnapshot) { s.ClockHighWaterMillis = -1 }, ErrInvalidRetiredCatalogSnapshot},
		{"unsorted epochs", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0], s.Epochs[1] = s.Epochs[1], s.Epochs[0]
		}, ErrInvalidRetiredCatalogSnapshot},
		{"duplicate epoch", func(s *RetiredCatalogSnapshot) {
			s.Epochs[1] = s.Epochs[0]
		}, ErrInvalidRetiredCatalogSnapshot},
		{"active epoch", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].Policy.Epoch = config.ActiveEpoch
			s.Epochs[0].State.Epoch = config.ActiveEpoch
			for i := range s.Epochs[0].State.Receipts {
				copy(s.Epochs[0].State.Receipts[i].ID[1:17], config.ActiveEpoch[:])
			}
		}, ErrActiveEpochReceipt},
		{"empty epoch", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.Receipts = nil
		}, ErrInvalidRetiredCatalogSnapshot},
		{"epoch conflict", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.Epoch = Epoch{7}
		}, ErrInvalidRetiredCatalogSnapshot},
		{"clock conflict", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.ClockHighWaterMillis++
		}, ErrInvalidRetiredCatalogSnapshot},
		{"nested version", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.Version++
		}, ErrInvalidRetiredCatalogSnapshot},
		{"policy fingerprint", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.PolicyFingerprint[0] ^= 1
		}, ErrInvalidRetiredCatalogSnapshot},
		{"policy metadata", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].Policy.Retention += time.Hour
		}, ErrInvalidRetiredCatalogSnapshot},
		{"deadline", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.Receipts[0].DeadlineMillis++
		}, ErrInvalidRetiredCatalogSnapshot},
		{"malformed ID", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.Receipts[0].ID[0] = 255
		}, ErrInvalidRetiredCatalogSnapshot},
		{"unsorted IDs", func(s *RetiredCatalogSnapshot) {
			rows := s.Epochs[0].State.Receipts
			rows[0], rows[1] = rows[1], rows[0]
		}, ErrInvalidRetiredCatalogSnapshot},
		{"duplicate ID", func(s *RetiredCatalogSnapshot) {
			s.Epochs[0].State.Receipts[1] = cloneReceipt(s.Epochs[0].State.Receipts[0])
		}, ErrInvalidRetiredCatalogSnapshot},
		{"conflicting group index", func(s *RetiredCatalogSnapshot) {
			rows := s.Epochs[0].State.Receipts
			rows[1].Group = rows[0].Group
		}, ErrInvalidRetiredCatalogSnapshot},
		{"conflicting contribution", func(s *RetiredCatalogSnapshot) {
			rows := s.Epochs[0].State.Receipts
			rows[1].Kind = AddEdge
			rows[1].HasContrib = true
			rows[1].ContribID = rows[0].ContribID
		}, ErrInvalidRetiredCatalogSnapshot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			corrupt := cloneRetiredCatalogSnapshot(state)
			tc.mutate(&corrupt)
			catalog, err := NewRetiredCatalogFromSnapshot(config, corrupt)
			if catalog != nil || !errors.Is(err, tc.want) {
				t.Fatalf("import = %p, %v, want nil and %v", catalog, err, tc.want)
			}
		})
	}
}

func TestRetiredCatalogRejectsAggregateCapacityOverflow(t *testing.T) {
	config, state, _ := retiredCatalogFixture(t)
	entryLimited := config
	entryLimited.MaxEntries = 2
	if catalog, err := NewRetiredCatalogFromSnapshot(entryLimited, state); catalog != nil ||
		!errors.Is(err, ErrRetiredCatalogCapacity) {
		t.Fatalf("entry overflow = %p, %v", catalog, err)
	}

	var exactBytes uint64
	for _, member := range state.Epochs {
		for _, receipt := range member.State.Receipts {
			exactBytes += receipt.cost()
		}
	}
	byteLimited := config
	byteLimited.MaxBytes = exactBytes - 1
	if catalog, err := NewRetiredCatalogFromSnapshot(byteLimited, state); catalog != nil ||
		!errors.Is(err, ErrRetiredCatalogCapacity) {
		t.Fatalf("byte overflow = %p, %v", catalog, err)
	}
	exact := config
	exact.MaxBytes = exactBytes
	if _, err := NewRetiredCatalogFromSnapshot(exact, state); err != nil {
		t.Fatalf("exact aggregate bounds were rejected: %v", err)
	}
}

func TestRetiredCatalogRejectsClockRollbackAndInvalidConfig(t *testing.T) {
	config, state, intents := retiredCatalogFixture(t)
	rollbackConfig := config
	rollbackConfig.ClockHighWater = config.ClockHighWater.Add(-time.Millisecond)
	if catalog, err := NewRetiredCatalogFromSnapshot(rollbackConfig, state); catalog != nil ||
		!errors.Is(err, ErrRetiredCatalogClockRollback) {
		t.Fatalf("import clock rollback = %p, %v", catalog, err)
	}

	catalog, err := NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	advanced := config.ClockHighWater.Add(time.Minute)
	if _, err := catalog.Snapshot(advanced); err != nil {
		t.Fatal(err)
	}
	if status, receipt, err := catalog.Lookup(intents[0].ID, config.ClockHighWater); status != 0 ||
		!reflect.DeepEqual(receipt, Receipt{}) || !errors.Is(err, ErrRetiredCatalogClockRollback) {
		t.Fatalf("lookup clock rollback = %v, %+v, %v", status, receipt, err)
	}
	if snapshot, err := catalog.Snapshot(config.ClockHighWater); snapshot.Version != 0 ||
		snapshot.ClockHighWaterMillis != 0 || snapshot.Epochs != nil ||
		!errors.Is(err, ErrRetiredCatalogClockRollback) {
		t.Fatalf("snapshot clock rollback = %+v, %v", snapshot, err)
	}
	if _, err := catalog.Snapshot(time.UnixMilli(-1)); !errors.Is(err, ErrInvalidClock) {
		t.Fatalf("negative effective clock = %v", err)
	}

	for _, invalid := range []RetiredCatalogConfig{
		{ActiveEpoch: Epoch{}, MaxEntries: 1, MaxBytes: 1},
		{ActiveEpoch: Epoch{1}, MaxEntries: 0, MaxBytes: 1},
		{ActiveEpoch: Epoch{1}, MaxEntries: 1, MaxBytes: 0},
		{ActiveEpoch: Epoch{1}, MaxEntries: 1, MaxBytes: 1, ClockHighWater: time.UnixMilli(-1)},
	} {
		if catalog, err := NewRetiredCatalog(invalid); catalog != nil ||
			!errors.Is(err, ErrInvalidRetiredCatalogConfig) {
			t.Fatalf("invalid config %+v = %p, %v", invalid, catalog, err)
		}
	}
}

func TestRetiredCatalogConcurrentLookupAndSnapshot(t *testing.T) {
	config, state, intents := retiredCatalogFixture(t)
	catalog, err := NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, 1)
	report := func(err error) {
		select {
		case errs <- err:
		default:
		}
	}
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 100; iteration++ {
				intent := intents[(worker+iteration)%len(intents)]
				status, receipt, err := catalog.Lookup(intent.ID, config.ClockHighWater)
				if err != nil || status != Confirmed {
					report(fmt.Errorf("lookup = %v, %v", status, err))
					return
				}
				receipt.Result[0] ^= 0xff
				snapshot, err := catalog.Snapshot(config.ClockHighWater)
				if err != nil || len(snapshot.Epochs) != 2 {
					report(fmt.Errorf("snapshot epochs = %d, %v", len(snapshot.Epochs), err))
					return
				}
				snapshot.Epochs[0].State.Receipts[0].Result[0] ^= 0xff
			}
		}(worker)
	}
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	for i, intent := range intents {
		status, receipt, err := catalog.Lookup(intent.ID, config.ClockHighWater)
		if err != nil || status != Confirmed ||
			string(receipt.Result) != []string{"first", "second", "third"}[i] {
			t.Fatalf("post-concurrency lookup %d = %v, %q, %v", i, status, receipt.Result, err)
		}
	}
}
