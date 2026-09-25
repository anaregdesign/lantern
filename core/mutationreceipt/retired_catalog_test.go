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
	config := RetiredCatalogConfig{
		ActiveEpoch: Epoch{9}, MaxEntries: 3, MaxBytes: 4096, ClockHighWater: highWater,
	}
	snapshot := RetiredCatalogSnapshot{
		Version:              retiredCatalogSnapshotVersion,
		ClockHighWaterMillis: highWater.UnixMilli(),
		Epochs:               []RetiredEpochSnapshot{epochTwo, epochThree},
	}
	return config, snapshot, []Intent{first, second, third}
}

func retiredTestCatalogSnapshot(
	highWater time.Time,
	members ...RetiredEpochSnapshot,
) RetiredCatalogSnapshot {
	return RetiredCatalogSnapshot{
		Version:              retiredCatalogSnapshotVersion,
		ClockHighWaterMillis: highWater.UnixMilli(),
		Epochs:               members,
	}
}

func TestRetiredCatalogUnionCanonicalExactUnion(t *testing.T) {
	epochTwoConfig := Config{
		Epoch: Epoch{2}, Retention: 2 * time.Hour, MaxEntries: 8, MaxBytes: 4096,
	}
	epochThreeConfig := Config{
		Epoch: Epoch{3}, Retention: 3 * time.Hour, MaxEntries: 8, MaxBytes: 4096,
	}
	early := retiredTestIntent(t, Epoch{2}, 3, testStart)
	later := retiredTestIntent(t, Epoch{2}, 1, testStart.Add(time.Minute))
	otherEpoch := retiredTestIntent(t, Epoch{3}, 2, testStart.Add(2*time.Minute))
	earlyClock := testStart.Add(5 * time.Minute)
	laterClock := testStart.Add(10 * time.Minute)
	otherClock := testStart.Add(15 * time.Minute)
	earlyState := retiredTestCatalogSnapshot(
		earlyClock,
		retiredTestMember(t, epochTwoConfig, earlyClock, []Intent{early}, [][]byte{[]byte("early")}),
	)
	laterState := retiredTestCatalogSnapshot(
		laterClock,
		retiredTestMember(t, epochTwoConfig, laterClock, []Intent{later}, [][]byte{[]byte("later")}),
	)
	otherState := retiredTestCatalogSnapshot(
		otherClock,
		retiredTestMember(t, epochThreeConfig, otherClock, []Intent{otherEpoch}, [][]byte{[]byte("other")}),
	)
	duplicateState := cloneRetiredCatalogSnapshot(earlyState)
	duplicateState.ClockHighWaterMillis = laterClock.UnixMilli()
	duplicateState.Epochs[0].State.ClockHighWaterMillis = laterClock.UnixMilli()
	config := RetiredCatalogConfig{
		ActiveEpoch:    Epoch{9},
		MaxEntries:     3,
		MaxBytes:       4096,
		ClockHighWater: testStart.Add(20 * time.Minute),
	}

	catalog, err := NewRetiredCatalogFromUnion(
		config,
		otherState,
		laterState,
		duplicateState,
		earlyState,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := catalog.Snapshot(config.ClockHighWater)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClockHighWaterMillis != config.ClockHighWater.UnixMilli() ||
		len(got.Epochs) != 2 ||
		got.Epochs[0].Policy.Epoch != (Epoch{2}) ||
		got.Epochs[1].Policy.Epoch != (Epoch{3}) {
		t.Fatalf("union epoch order = %+v", got)
	}
	epochTwoRows := got.Epochs[0].State.Receipts
	if len(epochTwoRows) != 2 ||
		epochTwoRows[0].ID != early.ID ||
		epochTwoRows[1].ID != later.ID ||
		string(epochTwoRows[0].Result) != "early" ||
		string(epochTwoRows[1].Result) != "later" {
		t.Fatalf("union receipt order = %+v", epochTwoRows)
	}
	if rows := got.Epochs[1].State.Receipts; len(rows) != 1 ||
		rows[0].ID != otherEpoch.ID || string(rows[0].Result) != "other" {
		t.Fatalf("disjoint epoch union = %+v", rows)
	}

	reordered, err := NewRetiredCatalogFromUnion(
		config,
		earlyState,
		duplicateState,
		laterState,
		otherState,
	)
	if err != nil {
		t.Fatal(err)
	}
	reorderedState, err := reordered.Snapshot(config.ClockHighWater)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reorderedState, got) {
		t.Fatalf("input order changed union:\nfirst:  %+v\nsecond: %+v", got, reorderedState)
	}

	canonical := cloneRetiredCatalogSnapshot(got)
	laterState.Epochs[0].Policy.Retention = time.Hour
	laterState.Epochs[0].State.Receipts[0].Result[0] = 'X'
	got.Epochs[0].State.Receipts[0].Result[0] = 'Y'
	again, err := catalog.Snapshot(config.ClockHighWater)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, canonical) {
		t.Fatalf("union shares caller memory: %+v", again)
	}
	restored, err := NewRetiredCatalogFromSnapshot(config, canonical)
	if err != nil {
		t.Fatalf("union did not round trip through snapshot import: %v", err)
	}
	roundTrip, err := restored.Snapshot(config.ClockHighWater)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, canonical) {
		t.Fatalf("union round trip = %+v, want %+v", roundTrip, canonical)
	}
}

func TestRetiredCatalogUnionRejectsEvidenceAndRelationshipConflicts(t *testing.T) {
	highWater := testStart.Add(10 * time.Minute)
	policy := Config{
		Epoch: Epoch{2}, Retention: 2 * time.Hour, MaxEntries: 8, MaxBytes: 4096,
	}
	config := RetiredCatalogConfig{
		ActiveEpoch: Epoch{9}, MaxEntries: 8, MaxBytes: 8192, ClockHighWater: highWater,
	}
	baseIntent := retiredTestIntent(t, policy.Epoch, 1, testStart)
	base := retiredTestCatalogSnapshot(
		highWater,
		retiredTestMember(t, policy, highWater, []Intent{baseIntent}, [][]byte{[]byte("base")}),
	)
	resultConflict := cloneRetiredCatalogSnapshot(base)
	resultConflict.Epochs[0].State.Receipts[0].Result = []byte("changed")
	intentConflict := cloneRetiredCatalogSnapshot(base)
	intentConflict.Epochs[0].State.Receipts[0].Digest[0] ^= 1
	deadlineConflict := cloneRetiredCatalogSnapshot(base)
	deadlineConflict.Epochs[0].State.Receipts[0].DeadlineMillis++
	groupConflict := cloneRetiredCatalogSnapshot(base)
	groupConflict.Epochs[0].State.Receipts[0].Group = GroupID{99}
	countConflict := cloneRetiredCatalogSnapshot(base)
	countConflict.Epochs[0].State.Receipts[0].Count = 2
	indexBase := cloneRetiredCatalogSnapshot(base)
	indexBase.Epochs[0].State.Receipts[0].Count = 2
	indexConflict := cloneRetiredCatalogSnapshot(indexBase)
	indexConflict.Epochs[0].State.Receipts[0].Index = 1
	hasContribConflict := cloneRetiredCatalogSnapshot(base)
	hasContribConflict.Epochs[0].State.Receipts[0].Kind = AddEdge
	hasContribConflict.Epochs[0].State.Receipts[0].HasContrib = true
	hasContribConflict.Epochs[0].State.Receipts[0].ContribID = ContribID{98}
	contribIDBase := cloneRetiredCatalogSnapshot(hasContribConflict)
	contribIDConflict := cloneRetiredCatalogSnapshot(contribIDBase)
	contribIDConflict.Epochs[0].State.Receipts[0].ContribID = ContribID{97}

	otherPolicy := policy
	otherPolicy.Retention = 3 * time.Hour
	otherPolicyIntent := retiredTestIntent(t, policy.Epoch, 2, testStart.Add(time.Minute))
	policyConflict := retiredTestCatalogSnapshot(
		highWater,
		retiredTestMember(
			t,
			otherPolicy,
			highWater,
			[]Intent{otherPolicyIntent},
			[][]byte{[]byte("other policy")},
		),
	)

	groupLeft := retiredTestIntent(t, policy.Epoch, 3, testStart.Add(2*time.Minute))
	groupRight := retiredTestIntent(t, policy.Epoch, 4, testStart.Add(3*time.Minute))
	groupRight.Group = groupLeft.Group
	groupLeftState := retiredTestCatalogSnapshot(
		highWater,
		retiredTestMember(t, policy, highWater, []Intent{groupLeft}, [][]byte{[]byte("left")}),
	)
	groupRightState := retiredTestCatalogSnapshot(
		highWater,
		retiredTestMember(t, policy, highWater, []Intent{groupRight}, [][]byte{[]byte("right")}),
	)

	contribLeft := retiredTestIntent(t, policy.Epoch, 5, testStart.Add(4*time.Minute))
	contribLeft.Kind, contribLeft.HasContrib, contribLeft.ContribID = AddEdge, true, ContribID{42}
	contribRight := retiredTestIntent(t, policy.Epoch, 6, testStart.Add(5*time.Minute))
	contribRight.Kind, contribRight.HasContrib, contribRight.ContribID = AddEdge, true, ContribID{42}
	contribLeftState := retiredTestCatalogSnapshot(
		highWater,
		retiredTestMember(t, policy, highWater, []Intent{contribLeft}, [][]byte{[]byte("left")}),
	)
	contribRightState := retiredTestCatalogSnapshot(
		highWater,
		retiredTestMember(t, policy, highWater, []Intent{contribRight}, [][]byte{[]byte("right")}),
	)

	cases := []struct {
		name             string
		states           []RetiredCatalogSnapshot
		wantStoreInvalid bool
	}{
		{"same ID result", []RetiredCatalogSnapshot{base, resultConflict}, false},
		{"same ID intent", []RetiredCatalogSnapshot{base, intentConflict}, false},
		{"same ID deadline", []RetiredCatalogSnapshot{base, deadlineConflict}, true},
		{"same ID group", []RetiredCatalogSnapshot{base, groupConflict}, false},
		{"same ID index", []RetiredCatalogSnapshot{indexBase, indexConflict}, false},
		{"same ID count", []RetiredCatalogSnapshot{base, countConflict}, false},
		{"same ID contribution presence", []RetiredCatalogSnapshot{base, hasContribConflict}, false},
		{"same ID contribution ID", []RetiredCatalogSnapshot{contribIDBase, contribIDConflict}, false},
		{"same epoch policy", []RetiredCatalogSnapshot{base, policyConflict}, false},
		{"group index", []RetiredCatalogSnapshot{groupLeftState, groupRightState}, true},
		{"contribution ID", []RetiredCatalogSnapshot{contribLeftState, contribRightState}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			catalog, err := NewRetiredCatalogFromUnion(config, tc.states...)
			if catalog != nil || !errors.Is(err, ErrInvalidRetiredCatalogSnapshot) {
				t.Fatalf("union = %p, %v, want nil and invalid retired snapshot", catalog, err)
			}
			if tc.wantStoreInvalid && !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("union error = %v, want authoritative Store snapshot error", err)
			}
		})
	}
}

func TestRetiredCatalogUnionRejectsInvalidInputs(t *testing.T) {
	highWater := testStart.Add(10 * time.Minute)
	policy := Config{
		Epoch: Epoch{2}, Retention: 2 * time.Hour, MaxEntries: 4, MaxBytes: 4096,
	}
	first := retiredTestIntent(t, policy.Epoch, 1, testStart)
	second := retiredTestIntent(t, policy.Epoch, 2, testStart.Add(time.Minute))
	state := retiredTestCatalogSnapshot(
		highWater,
		retiredTestMember(
			t,
			policy,
			highWater,
			[]Intent{first, second},
			[][]byte{[]byte("first"), []byte("second")},
		),
	)
	config := RetiredCatalogConfig{
		ActiveEpoch: Epoch{9}, MaxEntries: 2, MaxBytes: 4096, ClockHighWater: highWater,
	}
	malformed := cloneRetiredCatalogSnapshot(state)
	malformed.Epochs[0].State.Version++
	active := config
	active.ActiveEpoch = policy.Epoch
	rollback := config
	rollback.ClockHighWater = highWater.Add(-time.Millisecond)
	overCapacity := config
	overCapacity.MaxEntries = 1

	cases := []struct {
		name   string
		config RetiredCatalogConfig
		state  RetiredCatalogSnapshot
		want   error
	}{
		{"active epoch", active, state, ErrActiveEpochReceipt},
		{"malformed snapshot", config, malformed, ErrInvalidRetiredCatalogSnapshot},
		{"clock rollback", rollback, state, ErrRetiredCatalogClockRollback},
		{"individual capacity", overCapacity, state, ErrRetiredCatalogCapacity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			catalog, err := NewRetiredCatalogFromUnion(tc.config, tc.state)
			if catalog != nil || !errors.Is(err, tc.want) {
				t.Fatalf("union = %p, %v, want nil and %v", catalog, err, tc.want)
			}
		})
	}
}

func TestRetiredCatalogUnionChecksRawCapacityBeforeExpiryPruning(t *testing.T) {
	inputClock := testStart.Add(30 * time.Minute)
	effectiveClock := testStart.Add(2 * time.Hour)
	policy := Config{
		Epoch: Epoch{2}, Retention: time.Hour, MaxEntries: 2, MaxBytes: 4096,
	}
	first := retiredTestIntent(t, policy.Epoch, 1, testStart)
	second := retiredTestIntent(t, policy.Epoch, 2, testStart.Add(time.Minute))
	firstState := retiredTestCatalogSnapshot(
		inputClock,
		retiredTestMember(t, policy, inputClock, []Intent{first}, [][]byte{[]byte("same")}),
	)
	secondState := retiredTestCatalogSnapshot(
		inputClock,
		retiredTestMember(t, policy, inputClock, []Intent{second}, [][]byte{[]byte("same")}),
	)
	firstCost := firstState.Epochs[0].State.Receipts[0].cost()
	secondCost := secondState.Epochs[0].State.Receipts[0].cost()

	entryLimited := RetiredCatalogConfig{
		ActiveEpoch:    Epoch{9},
		MaxEntries:     1,
		MaxBytes:       firstCost + secondCost,
		ClockHighWater: effectiveClock,
	}
	if catalog, err := NewRetiredCatalogFromUnion(entryLimited, firstState, secondState); catalog != nil ||
		!errors.Is(err, ErrRetiredCatalogCapacity) {
		t.Fatalf("raw entry overflow = %p, %v", catalog, err)
	}
	byteLimited := entryLimited
	byteLimited.MaxEntries = 2
	byteLimited.MaxBytes = firstCost
	if catalog, err := NewRetiredCatalogFromUnion(byteLimited, firstState, secondState); catalog != nil ||
		!errors.Is(err, ErrRetiredCatalogCapacity) {
		t.Fatalf("raw byte overflow = %p, %v", catalog, err)
	}

	roomy := byteLimited
	roomy.MaxBytes = firstCost + secondCost
	catalog, err := NewRetiredCatalogFromUnion(roomy, firstState, secondState)
	if err != nil {
		t.Fatal(err)
	}
	state, err := catalog.Snapshot(effectiveClock)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Epochs) != 0 {
		t.Fatalf("expired raw union was not pruned: %+v", state)
	}
}

func TestRetiredCatalogUnionUsesEffectiveClockFloor(t *testing.T) {
	policy := Config{
		Epoch: Epoch{2}, Retention: time.Hour, MaxEntries: 2, MaxBytes: 4096,
	}
	expired := retiredTestIntent(t, policy.Epoch, 1, testStart)
	live := retiredTestIntent(t, policy.Epoch, 2, testStart.Add(45*time.Minute))
	expiredClock := testStart.Add(10 * time.Minute)
	liveClock := testStart.Add(50 * time.Minute)
	expiredState := retiredTestCatalogSnapshot(
		expiredClock,
		retiredTestMember(t, policy, expiredClock, []Intent{expired}, [][]byte{[]byte("expired")}),
	)
	liveState := retiredTestCatalogSnapshot(
		liveClock,
		retiredTestMember(t, policy, liveClock, []Intent{live}, [][]byte{[]byte("live")}),
	)
	effectiveClock := testStart.Add(75 * time.Minute)
	config := RetiredCatalogConfig{
		ActiveEpoch: Epoch{9}, MaxEntries: 2, MaxBytes: 4096, ClockHighWater: effectiveClock,
	}
	catalog, err := NewRetiredCatalogFromUnion(config, expiredState, liveState)
	if err != nil {
		t.Fatal(err)
	}
	state, err := catalog.Snapshot(effectiveClock)
	if err != nil {
		t.Fatal(err)
	}
	if state.ClockHighWaterMillis != effectiveClock.UnixMilli() ||
		len(state.Epochs) != 1 ||
		state.Epochs[0].State.ClockHighWaterMillis != effectiveClock.UnixMilli() ||
		len(state.Epochs[0].State.Receipts) != 1 ||
		state.Epochs[0].State.Receipts[0].ID != live.ID {
		t.Fatalf("effective-clock union = %+v", state)
	}
	if status, _, err := catalog.Lookup(expired.ID, effectiveClock); err != nil ||
		status != NoLongerProvable {
		t.Fatalf("expired union lookup = %v, %v", status, err)
	}
	if status, receipt, err := catalog.Lookup(live.ID, effectiveClock); err != nil ||
		status != Confirmed || string(receipt.Result) != "live" {
		t.Fatalf("live union lookup = %v, %+v, %v", status, receipt, err)
	}
	if _, err := NewRetiredCatalogFromSnapshot(config, state); err != nil {
		t.Fatalf("effective-clock union did not round trip: %v", err)
	}
}

func TestRetiredCatalogUnionEmptyInputs(t *testing.T) {
	effectiveClock := testStart.Add(20 * time.Minute)
	config := RetiredCatalogConfig{
		ActiveEpoch: Epoch{9}, MaxEntries: 2, MaxBytes: 4096, ClockHighWater: effectiveClock,
	}
	withoutInputs, err := NewRetiredCatalogFromUnion(config)
	if err != nil {
		t.Fatal(err)
	}
	emptyInputs, err := NewRetiredCatalogFromUnion(
		config,
		RetiredCatalogSnapshot{Version: retiredCatalogSnapshotVersion},
		RetiredCatalogSnapshot{
			Version:              retiredCatalogSnapshotVersion,
			ClockHighWaterMillis: testStart.Add(10 * time.Minute).UnixMilli(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want, err := withoutInputs.Snapshot(effectiveClock)
	if err != nil {
		t.Fatal(err)
	}
	got, err := emptyInputs.Snapshot(effectiveClock)
	if err != nil {
		t.Fatal(err)
	}
	if want.ClockHighWaterMillis != effectiveClock.UnixMilli() ||
		len(want.Epochs) != 0 ||
		!reflect.DeepEqual(got, want) {
		t.Fatalf("empty union = %+v, want %+v", got, want)
	}
	if _, err := NewRetiredCatalogFromSnapshot(config, got); err != nil {
		t.Fatalf("empty union did not round trip: %v", err)
	}
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
