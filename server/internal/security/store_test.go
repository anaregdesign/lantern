package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
)

func TestStoreCASAndIdempotency(t *testing.T) {
	store, sink, _ := testStore(t, true)
	image := testImage()
	ctx := context.Background()
	result, err := store.ReconcileBootstrap(ctx, 0, [16]byte{1}, image)
	if err != nil || result.Revision != 1 || sink.calls != 1 {
		t.Fatalf("bootstrap: %v, %v", result, err)
	}
	replay, err := store.ReconcileBootstrap(ctx, 0, [16]byte{1}, image)
	if err != nil || !replay.Replayed || replay.Digest != result.Digest || sink.calls != 1 {
		t.Fatal("replay performed another commit")
	}
	changed := testImage()
	changed.Principals = append(changed.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "reader"}, State: Active})
	if _, err := store.Commit(ctx, 0, [16]byte{1}, changed); !errors.Is(err, ErrChangeConflict) {
		t.Fatalf("ID collision: %v", err)
	}
	if _, err := store.Commit(ctx, 0, [16]byte{2}, changed); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	// Two writers of the same expected revision cannot both win.
	var group sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for _, id := range [][16]byte{{2}, {3}} {
		group.Go(func() {
			_, err := store.Commit(ctx, 1, id, changed)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
			} else if !errors.Is(err, ErrRevisionConflict) {
				t.Errorf("unexpected CAS result: %v", err)
			}
		})
	}
	group.Wait()
	if successes != 1 || sink.calls != 2 {
		t.Fatal("atomic CAS violated")
	}
	current, healthy := store.Current()
	if !healthy || current.Sequence() != 2 || len(current.Snapshot().Image().Principals) != 2 {
		t.Fatal("partial or stale revision published")
	}
}

func TestStoreEnvLocksAndFault(t *testing.T) {
	store, sink, _ := testStore(t, true)
	ctx := context.Background()
	if _, err := store.Commit(ctx, 0, [16]byte{1}, testImage()); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("unreconciled initial bootstrap")
	}
	if _, err := store.ReconcileBootstrap(ctx, 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	changed := testImage()
	changed.Principals[0].Assignments[0].EnvOwned = false
	if _, err := store.Commit(ctx, 1, [16]byte{2}, changed); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("env assignment unlocked")
	}
	changed = testImage()
	changed.Roles[0].Rules = append(changed.Roles[0].Rules, dataRule(Allow, VertexRead, ""))
	if _, err := store.Commit(ctx, 1, [16]byte{2}, changed); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("env Role expanded")
	}
	if _, err := store.ReconcileBootstrap(ctx, 1, [16]byte{2}, testImage()); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("old pod recreated bootstrap")
	}
	ctxCanceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Commit(ctxCanceled, 1, [16]byte{2}, testImage()); !errors.Is(err, context.Canceled) || sink.calls != 1 {
		t.Fatal("canceled commit performed I/O")
	}
	sink.err = errors.New("indeterminate sync")
	if _, err := store.Commit(ctx, 1, [16]byte{2}, testImage()); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatal("fault not fenced")
	}
	if _, healthy := store.Current(); healthy {
		t.Fatal("old grants served after indeterminate commit")
	}
	sink.err = nil
	if _, err := store.Commit(ctx, 1, [16]byte{3}, testImage()); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatal("poisoned store resumed")
	}
}

func TestStoreSignedReplicaApply(t *testing.T) {
	writer, _, options := testStore(t, true)
	ctx := context.Background()
	if _, err := writer.ReconcileBootstrap(ctx, 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	one, _ := writer.Current()
	options.PrivateKey = nil
	replicaSink := &fakeCommitter{}
	options.Committer = replicaSink
	replica, err := NewStore(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replica.Commit(ctx, 0, [16]byte{2}, testImage()); !errors.Is(err, ErrReadOnlyWriter) {
		t.Fatal("replica issued grant")
	}
	if err := replica.Apply(ctx, one.Encode()); err != nil {
		t.Fatal(err)
	}
	if err := replica.Apply(ctx, one.Encode()); err != nil || replicaSink.calls != 1 {
		t.Fatal("duplicate persisted")
	}
	if _, err := writer.Commit(ctx, 1, [16]byte{2}, testImage()); err != nil {
		t.Fatal(err)
	}
	two, _ := writer.Current()
	if err := replica.Apply(ctx, two.Encode()); err != nil {
		t.Fatal(err)
	}
	if err := replica.Apply(ctx, one.Encode()); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("rollback accepted")
	}
	image, _ := CompileImage(testImage(), DefaultPolicyLimits())
	fork, _ := SignRevision(options.Generation, 3, one.Digest(), [16]byte{3}, image, optionsPrivateKey(writer))
	if err := replica.Apply(ctx, fork.Encode()); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("fork accepted")
	}
	foreign, _ := SignRevision([16]byte{9}, 1, [32]byte{}, [16]byte{9}, image, optionsPrivateKey(writer))
	if err := replica.Apply(ctx, foreign.Encode()); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("generation change accepted")
	}
	current, healthy := replica.Current()
	if !healthy || current.Digest() != two.Digest() || replicaSink.calls != 2 {
		t.Fatal("rejected input changed authority")
	}
}

func optionsPrivateKey(store *Store) ed25519.PrivateKey { return store.privateKey }

func TestStoreSignedApplyPreservesBootstrapLocks(t *testing.T) {
	writer, _, options := testStore(t, true)
	if _, err := writer.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	one, _ := writer.Current()
	options.PrivateKey = nil
	options.Committer = &fakeCommitter{}
	replica, err := NewStore(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.Apply(t.Context(), one.Encode()); err != nil {
		t.Fatal(err)
	}
	changed := testImage()
	changed.Roles[0].Rules = append(changed.Roles[0].Rules, dataRule(Allow, VertexRead, ""))
	snapshot, err := CompileImage(changed, options.Limits)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignRevision(options.Generation, 2, one.Digest(), [16]byte{2}, snapshot, writer.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.Apply(t.Context(), signed.Encode()); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("signed apply bypassed env-owned Role lock", err)
	}
	current, healthy := replica.Current()
	if !healthy || current.Digest() != one.Digest() {
		t.Fatal("invalid signed transaction changed replica authority")
	}
}

func TestStorePublicationAfterPersistence(t *testing.T) {
	_, _, options := testStore(t, true)
	sink := &blockingCommitter{entered: make(chan struct{}), release: make(chan struct{})}
	options.Committer = sink
	store, err := NewStore(options)
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		_, err := store.ReconcileBootstrap(context.Background(), 0, [16]byte{1}, testImage())
		completed <- err
	}()
	<-sink.entered
	if _, healthy := store.Current(); healthy {
		t.Fatal("revision published before persistence completed")
	}
	close(sink.release)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if _, healthy := store.Current(); !healthy {
		t.Fatal("persisted revision not published")
	}
}

type blockingCommitter struct{ entered, release chan struct{} }

func (c *blockingCommitter) CommitRevision(context.Context, *Revision) error {
	close(c.entered)
	<-c.release
	return nil
}

func TestStoreIndeterminatePanic(t *testing.T) {
	_, _, options := testStore(t, true)
	options.Committer = panicCommitter{}
	store, err := NewStore(options)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("committer did not panic")
			}
		}()
		_, _ = store.ReconcileBootstrap(context.Background(), 0, [16]byte{1}, testImage())
	}()
	if _, healthy := store.Current(); healthy {
		t.Fatal("old authority healthy after indeterminate panic")
	}
	if _, err := store.ReconcileBootstrap(context.Background(), 0, [16]byte{2}, testImage()); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatal("panic-poisoned store resumed")
	}
}

type panicCommitter struct{}

func (panicCommitter) CommitRevision(context.Context, *Revision) error {
	panic("indeterminate durable write")
}

func testStore(t *testing.T, writer bool) (*Store, *fakeCommitter, StoreOptions) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !writer {
		privateKey = nil
	}
	sink := &fakeCommitter{}
	options := StoreOptions{Generation: [16]byte{1}, PublicKey: publicKey, PrivateKey: privateKey,
		Committer: sink, Limits: DefaultPolicyLimits()}
	store, err := NewStore(options)
	if err != nil {
		t.Fatal(err)
	}
	return store, sink, options
}
