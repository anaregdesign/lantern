package mutationreceipt

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func snapshotFixture(t *testing.T) (Config, Snapshot, Intent, Intent) {
	t.Helper()
	config := Config{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 3, MaxBytes: 1000}
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	first := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	first.Kind, first.HasContrib, first.ContribID = AddEdge, true, ContribID{9}
	second := testIntent(t, 2, testStart.Add(time.Minute), GroupID{2}, 0, 1)
	commitTestBatch(t, s, testStart, []Intent{first}, [][]byte{[]byte("first")})
	commitTestBatch(t, s, testStart.Add(time.Minute), []Intent{second}, [][]byte{[]byte("second")})
	state, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return config, state, first, second
}

func cloneSnapshot(state Snapshot) Snapshot {
	state.Receipts = append([]Receipt(nil), state.Receipts...)
	for i := range state.Receipts {
		state.Receipts[i] = cloneReceipt(state.Receipts[i])
	}
	return state
}

func TestSnapshotRoundTripPreservesResultsIndexesAndClock(t *testing.T) {
	config, state, first, second := snapshotFixture(t)
	if state.Version != snapshotVersion || state.Epoch != config.Epoch ||
		state.ClockHighWaterMillis != testStart.Add(time.Minute).UnixMilli() ||
		len(state.Receipts) != 2 || state.Receipts[0].ID != first.ID ||
		state.Receipts[1].ID != second.ID {
		t.Fatalf("unexpected snapshot header/order: %+v", state)
	}
	if got := state.ClockHighWater(); !got.Equal(testStart.Add(time.Minute)) {
		t.Fatalf("clock high-water = %v", got)
	}
	restored, err := NewFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, state) {
		t.Fatalf("restored snapshot = %+v, want %+v", got, state)
	}
	state.Receipts[0].Result[0] = 'X'
	if status, receipt, err := restored.Lookup(first.ID, testStart.Add(2*time.Minute)); err != nil || status != Confirmed || string(receipt.Result) != "first" {
		t.Fatalf("restored original result = %v, %+v, %v", status, receipt, err)
	}
	tx, err := restored.Begin(testStart.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	classification, receipts, err := tx.Classify([]Intent{first})
	tx.Abort()
	if err != nil || classification != Duplicate || string(receipts[0].Result) != "first" {
		t.Fatalf("restored duplicate = %v, %+v, %v", classification, receipts, err)
	}
	other := testIntent(t, 3, testStart.Add(2*time.Minute), GroupID{3}, 0, 1)
	other.Kind, other.HasContrib, other.ContribID = AddEdge, true, first.ContribID
	tx, err = restored.Begin(testStart.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = tx.Classify([]Intent{other})
	tx.Abort()
	if !errors.Is(err, ErrContributionConflict) {
		t.Fatalf("restored contribution binding = %v", err)
	}
}

func TestSnapshotRestoreHigherClockEvictsExpiredButNeverReopensID(t *testing.T) {
	config, state, first, second := snapshotFixture(t)
	config.ClockHighWater = testStart.Add(time.Hour + 30*time.Second)
	restored, err := NewFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Stats(); got.Entries != 1 || got.HighWaterMillis != config.ClockHighWater.UnixMilli() {
		t.Fatalf("higher-clock restore stats = %+v", got)
	}
	if status, _, err := restored.Lookup(first.ID, testStart); err != nil || status != NoLongerProvable {
		t.Fatalf("expired ID after backward clock = %v, %v", status, err)
	}
	if status, _, err := restored.Lookup(second.ID, testStart); err != nil || status != Confirmed {
		t.Fatalf("still-live ID after backward clock = %v, %v", status, err)
	}
}

func TestSnapshotAllowsPartiallyExpiredLogicalCall(t *testing.T) {
	config := Config{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 2, MaxBytes: 1000}
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	items := []Intent{
		testIntent(t, 1, testStart, GroupID{9}, 0, 2),
		testIntent(t, 2, testStart.Add(time.Minute), GroupID{9}, 1, 2),
	}
	commitTestBatch(t, s, testStart.Add(time.Minute), items, [][]byte{[]byte("first"), []byte("second")})
	if status, _, err := s.Lookup(items[0].ID, testStart.Add(time.Hour+30*time.Second)); err != nil || status != NoLongerProvable {
		t.Fatalf("first expired item = %v, %v", status, err)
	}
	state, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Receipts) != 1 || state.Receipts[0].Index != 1 {
		t.Fatalf("partial group snapshot = %+v", state.Receipts)
	}
	restored, err := NewFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	if status, receipt, err := restored.Lookup(items[1].ID, testStart); err != nil || status != Confirmed || string(receipt.Result) != "second" {
		t.Fatalf("surviving item = %v, %+v, %v", status, receipt, err)
	}
}

func TestSnapshotWaitsForStagedReceiptPublication(t *testing.T) {
	s := testStore(t, 1, 1000)
	item := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	tx, err := s.Begin(testStart)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if class, _, err := tx.Classify([]Intent{item}); err != nil || class != Fresh {
		t.Fatalf("classify = %v, %v", class, err)
	}
	if err := tx.Reserve([][]byte{[]byte("tentative")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan Snapshot, 1)
	errorsSeen := make(chan error, 1)
	go func() {
		close(started)
		state, err := s.Snapshot()
		errorsSeen <- err
		finished <- state
	}()
	<-started
	select {
	case <-finished:
		t.Fatal("snapshot exposed a tentative receipt")
	case <-time.After(20 * time.Millisecond):
	}
	tx.Abort()
	select {
	case state := <-finished:
		if err := <-errorsSeen; err != nil {
			t.Fatal(err)
		}
		if len(state.Receipts) != 0 {
			t.Fatalf("aborted receipt in snapshot: %+v", state.Receipts)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot did not resume after Abort")
	}
}

func TestSnapshotRefusesRetiredEpochRowsWithoutPartialExport(t *testing.T) {
	s := testStore(t, 2, 1000)
	active := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	commitTestBatch(t, s, testStart, []Intent{active}, [][]byte{nil})
	oldID, err := NewID(Epoch{2}, testStart, [24]byte{2})
	if err != nil {
		t.Fatal(err)
	}
	old := Receipt{
		Intent:         Intent{ID: oldID, Group: GroupID{2}, Count: 1, Kind: PutVertex},
		DeadlineMillis: testStart.Add(time.Hour).UnixMilli(),
	}
	s.mu.Lock()
	s.receipts[oldID] = old
	s.deadlines.insert(deadlineEntry{id: oldID, deadlineMS: old.DeadlineMillis})
	s.bytes += old.cost()
	s.mu.Unlock()
	state, err := s.Snapshot()
	if !errors.Is(err, ErrRetiredEpochSnapshot) || len(state.Receipts) != 0 {
		t.Fatalf("retired-epoch export = %+v, %v", state, err)
	}
}

func TestSnapshotRestoreRejectsCorruptOrIncompatibleState(t *testing.T) {
	config, state, _, _ := snapshotFixture(t)
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"version", func(s *Snapshot) { s.Version++ }},
		{"epoch", func(s *Snapshot) { s.Epoch = Epoch{2} }},
		{"fingerprint", func(s *Snapshot) { s.PolicyFingerprint[0] ^= 1 }},
		{"negative clock", func(s *Snapshot) { s.ClockHighWaterMillis = -1 }},
		{"unsorted", func(s *Snapshot) { s.Receipts[0], s.Receipts[1] = s.Receipts[1], s.Receipts[0] }},
		{"duplicate", func(s *Snapshot) { s.Receipts[1] = s.Receipts[0] }},
		{"deadline", func(s *Snapshot) { s.Receipts[0].DeadlineMillis++ }},
		{"group", func(s *Snapshot) { s.Receipts[0].Group = GroupID{} }},
		{"duplicate group index", func(s *Snapshot) { s.Receipts[1].Group = s.Receipts[0].Group }},
		{"mismatched group count", func(s *Snapshot) {
			s.Receipts[1].Group = s.Receipts[0].Group
			s.Receipts[1].Count = 2
		}},
		{"kind", func(s *Snapshot) { s.Receipts[0].Kind = 255 }},
		{"contribution", func(s *Snapshot) { s.Receipts[0].ContribID = ContribID{} }},
		{"duplicate contribution", func(s *Snapshot) {
			s.Receipts[1].Kind = AddEdge
			s.Receipts[1].HasContrib = true
			s.Receipts[1].ContribID = s.Receipts[0].ContribID
		}},
		{"byte capacity", func(s *Snapshot) { s.Receipts[0].Result = make([]byte, 1000) }},
		{"malformed ID", func(s *Snapshot) { s.Receipts[0].ID[0] = 255 }},
		{"expired at snapshot", func(s *Snapshot) { s.ClockHighWaterMillis = testStart.Add(time.Hour).UnixMilli() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			corrupt := cloneSnapshot(state)
			tc.mutate(&corrupt)
			if _, err := NewFromSnapshot(config, corrupt); !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("restore error = %v, want invalid snapshot", err)
			}
		})
	}
	changedPolicy := config
	changedPolicy.Retention = 2 * time.Hour
	if _, err := NewFromSnapshot(changedPolicy, state); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("changed policy = %v", err)
	}
	// A later clock may discard the first row, but corruption in the
	// original backup must still be rejected before that eviction.
	duplicate := cloneSnapshot(state)
	duplicate.Receipts[1].Kind = AddEdge
	duplicate.Receipts[1].HasContrib = true
	duplicate.Receipts[1].ContribID = duplicate.Receipts[0].ContribID
	config.ClockHighWater = testStart.Add(time.Hour + 30*time.Second)
	if _, err := NewFromSnapshot(config, duplicate); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("corrupt expired binding = %v", err)
	}
}
