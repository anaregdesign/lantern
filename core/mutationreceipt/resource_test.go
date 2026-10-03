package mutationreceipt

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestResourceIdentityBoundsAndKind(t *testing.T) {
	for _, test := range []struct {
		resource ResourceIdentity
		kind     Kind
		valid    bool
	}{
		{ResourceIdentity{}, PutVertex, true},
		{ResourceIdentity{Key: "sys:public-logical-key"}, PutVertex, true},
		{ResourceIdentity{Key: "a", Head: "b"}, DeleteEdgeContribution, true},
		{ResourceIdentity{Head: "b"}, AddEdge, false},
		{ResourceIdentity{Key: "a", Head: "b"}, PutVertex, false},
		{ResourceIdentity{Key: "a"}, DeleteEdge, false},
		{ResourceIdentity{Key: "\xff"}, PutVertex, false},
		{ResourceIdentity{Key: strings.Repeat("x", MaxResourceIdentityBytes), Head: "b"}, AddEdge, false},
	} {
		if got := test.resource.valid(test.kind); got != test.valid {
			t.Fatal(test.kind, got, test.valid)
		}
	}
}

func TestResourceProvenanceCapacityDuplicateAndRecovery(t *testing.T) {
	intent := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	intent.Resource = ResourceIdentity{Key: "orders:public:1"}
	row := committedTestReceipts([]Intent{intent}, [][]byte{{1}}, 0)[0]
	store := testStore(t, 2, row.cost())
	commitTestBatch(t, store, testStart, []Intent{intent}, [][]byte{{1}})
	if store.Stats().Bytes != row.cost() {
		t.Fatal("resource bytes escaped capacity accounting")
	}
	changed := intent
	changed.Resource.Key = "orders:private:1"
	tx, err := store.Begin(testStart)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = tx.Classify([]Intent{changed})
	tx.Abort()
	if !errors.Is(err, ErrIntentConflict) {
		t.Fatal("same digest replaced provenance", err)
	}
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	state.Receipts[0].Resource.Key = "tampered"
	_, retained, err := store.Lookup(intent.ID, testStart)
	if err != nil || retained.Resource != intent.Resource {
		t.Fatal("snapshot aliased provenance", err)
	}
	state, err = store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 2, MaxBytes: row.cost()}
	restored, err := NewFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	_, receipt, err := restored.Lookup(intent.ID, testStart)
	if err != nil || receipt.Resource != intent.Resource || restored.Stats().Bytes != row.cost() {
		t.Fatal("recovery dropped provenance", err)
	}
	undersized := config
	undersized.MaxBytes--
	if _, err := NewFromSnapshot(undersized, state); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatal("restore exceeded byte capacity", err)
	}
	state.Receipts[0].Resource.Head = "not-a-vertex"
	if _, err := NewFromSnapshot(config, state); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatal("invalid recovered resource accepted", err)
	}
}
