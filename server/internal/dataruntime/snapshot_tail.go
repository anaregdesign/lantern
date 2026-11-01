package dataruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"math"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"google.golang.org/protobuf/proto"
)

// SnapshotTailHeader is a private native-peer continuity contract. A Snapshot
// requests "reserve" and returns a single-use token for the next full Subscribe.
// The token identifies storage, never authority: both RPC admissions are checked.
const SnapshotTailHeader = "Lantern-Snapshot-Tail"

type replicationTailMetadata struct {
	values []string
	header http.Header
}

type replicationTailMetadataKey struct{}

// WithReplicationTailMetadata carries transport metadata into the unchanged
// service API. Only native admitted contexts may reserve or claim a tail.
func WithReplicationTailMetadata(ctx context.Context, values []string, header http.Header) context.Context {
	return context.WithValue(ctx, replicationTailMetadataKey{}, replicationTailMetadata{
		values: append([]string(nil), values...), header: header,
	})
}

func tailMetadata(ctx context.Context) (replicationTailMetadata, error) {
	metadata, _ := ctx.Value(replicationTailMetadataKey{}).(replicationTailMetadata)
	if len(metadata.values) > 1 || len(metadata.values) == 1 && metadata.values[0] == "" {
		return metadata, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid Snapshot tail metadata"))
	}
	return metadata, nil
}

// Queue storage is encoded, owned bytes, not Entry.Op graphs/receipt envelopes.
// Counts bound channel slots and bookkeeping; byte credits cover every queued
// or handler-held encoded frame. No ring or configured subscriber size changes.
const (
	maxSnapshotTails        = 8
	maxSnapshotTailEntries  = 512
	maxSnapshotTailBytes    = 16 << 20
	maxSnapshotTotalEntries = 4096
	maxSnapshotTotalBytes   = 64 << 20
	maxSnapshotTailOrigins  = 4096
)

type snapshotTailManager struct {
	mu           sync.Mutex
	active       map[string]*snapshotTail
	totalBytes   int
	totalEntries int
	closed       bool
}

type snapshotTail struct {
	manager      *snapshotTailManager
	token        string
	admission    *peerauth.Admission
	deadline     time.Time
	requestDone  <-chan struct{}
	local        uint64
	origins      map[string]uint64
	fault        <-chan struct{}
	subscription *mutationlog.Subscription
	opened       chan struct{}
	stop         chan struct{}
	stopped      chan struct{}
	released     chan struct{}
	closeOnce    sync.Once
	releaseOnce  sync.Once
	// Serialize queue retirement, claim setup and teardown. Never hold manager.mu
	// across a core call: dispatch projection takes that lock under subsMu.
	transitionMu sync.Mutex
	updated      chan struct{}
	// All following state is protected by manager.mu.
	ready   bool
	claimed bool
	closed  bool
	bytes   int
	entries int
	// Fixed categories only. Preserve the first observed terminal cause while
	// this existing owner lives; cleanup still removes it without a tombstone.
	terminal             snapshotTailReason
	ringReplay           bool
	deliverySubscription *mutationlog.Subscription
}

type snapshotTailFrame []byte

func (f snapshotTailFrame) replicationFrameSize() (int, error) { return len(f), nil }

type snapshotTailReason string

type snapshotTailDiagnostic struct {
	stage, guard string
	terminal     snapshotTailReason
}

func (e *snapshotTailDiagnostic) Error() string {
	return "Snapshot tail unavailable; obtain a new Snapshot [stage=" + e.stage +
		" guard=" + e.guard + " terminal=" + string(e.terminal) + "]"
}

func snapshotTailError(stage, guard string, terminal snapshotTailReason) error {
	if terminal == "" {
		terminal = "none"
	}
	return connect.NewError(connect.CodeFailedPrecondition, &snapshotTailDiagnostic{stage: stage, guard: guard, terminal: terminal})
}

// Classify only known sentinels. Neither raw error strings nor retained payload,
// token, membership, or native time evidence enters diagnostic output.
func snapshotTailCoreReason(err error) snapshotTailReason {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, mutationlog.ErrTailQueueFull):
		return "queue_full"
	case errors.Is(err, mutationlog.ErrSubscriptionLost):
		return "subscription_lost"
	case errors.Is(err, mutationlog.ErrClosed):
		return "log_closed"
	case errors.Is(err, mutationlog.ErrGapped):
		return "log_boundary"
	case errors.Is(err, mutationlog.ErrWALIndeterminate):
		return "wal_indeterminate"
	case errors.Is(err, mutationlog.ErrPublicationInterrupted):
		return "publication_interrupted"
	case errors.Is(err, context.Canceled):
		return "subscription_canceled"
	default:
		return "subscription_other"
	}
}

func (t *snapshotTail) rememberTerminalLocked(reason snapshotTailReason) {
	if t.terminal == "" {
		t.terminal = reason
	}
}

// Called only after opened closes, so subscription is immutable. The core's
// first terminal error must be sampled before owner cleanup calls Close.
func (t *snapshotTail) rememberCoreTerminalLocked() {
	if t.terminal == "" {
		subscription := t.subscription
		if t.ringReplay {
			subscription = t.deliverySubscription
		}
		if subscription != nil {
			t.rememberTerminalLocked(snapshotTailCoreReason(subscription.Err()))
		}
	}
}

func (t *snapshotTail) diagnosticLocked(stage, guard string, terminal snapshotTailReason) error {
	t.rememberCoreTerminalLocked()
	t.rememberTerminalLocked(terminal)
	return snapshotTailError(stage, guard, t.terminal)
}

func (t *snapshotTail) diagnostic(stage, guard string, terminal snapshotTailReason) error {
	t.manager.mu.Lock()
	defer t.manager.mu.Unlock()
	return t.diagnosticLocked(stage, guard, terminal)
}

func (t *snapshotTail) rememberTerminal(reason snapshotTailReason) {
	t.manager.mu.Lock()
	t.rememberTerminalLocked(reason)
	t.manager.mu.Unlock()
}

func (s *Replication) snapshotTailRequest(ctx context.Context) (bool, replicationTailMetadata, error) {
	metadata, err := tailMetadata(ctx)
	if err != nil || len(metadata.values) == 0 {
		return false, metadata, err
	}
	if metadata.values[0] != "reserve" || metadata.header == nil {
		return false, metadata, connect.NewError(connect.CodeInvalidArgument, errors.New("Snapshot requires tail reservation metadata"))
	}
	admission, ok := peerauth.AdmissionFromContext(ctx)
	_, finite := ctx.Deadline()
	if !ok || !finite || admission.Check(ctx) != nil {
		return false, metadata, connect.NewError(connect.CodeUnavailable, peerauth.ErrMembership)
	}
	owner, ok := s.origins.(*Executor)
	if !ok {
		return false, metadata, snapshotTailError("request", "origin_owner", "")
	}
	if s.log == nil {
		return false, metadata, snapshotTailError("request", "log_missing", "")
	}
	if owner.log != s.log {
		return false, metadata, snapshotTailError("request", "log_owner", "")
	}
	if s.snapshotTails == nil {
		return false, metadata, snapshotTailError("request", "manager_missing", "")
	}
	return true, metadata, nil
}

// Called inside the same graph/receipt publication cut that captures Snapshot.
func (s *Replication) openSnapshotTail(ctx context.Context, local uint64, origins map[string]uint64, fault <-chan struct{}) (*snapshotTail, error) {
	if len(origins) > maxSnapshotTailOrigins {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("Snapshot tail origin limit"))
	}
	admission, ok := peerauth.AdmissionFromContext(ctx)
	deadline, finite := ctx.Deadline()
	if !ok {
		return nil, snapshotTailError("open", "admission_missing", "")
	}
	if !finite {
		return nil, snapshotTailError("open", "deadline_missing", "")
	}
	if !time.Now().Before(deadline) {
		return nil, snapshotTailError("open", "deadline_expired", "")
	}
	if admission.Check(ctx) != nil {
		return nil, snapshotTailError("open", "admission_rejected", "")
	}
	if fault == nil {
		return nil, snapshotTailError("open", "generation_missing", "")
	}
	if local == math.MaxUint64 {
		return nil, snapshotTailError("open", "sequence_exhausted", "")
	}
	select {
	case <-fault:
		return nil, publicationGapError()
	default:
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	m := s.snapshotTails
	tail := &snapshotTail{manager: m, token: hex.EncodeToString(random[:]), admission: admission,
		deadline: deadline, requestDone: ctx.Done(), local: local, origins: maps.Clone(origins), fault: fault,
		opened: make(chan struct{}), stop: make(chan struct{}), stopped: make(chan struct{}), released: make(chan struct{})}
	tail.updated = make(chan struct{}, 1)
	m.mu.Lock()
	if m.closed || len(m.active) >= maxSnapshotTails {
		m.mu.Unlock()
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("Snapshot tail reservation limit"))
	}
	for _, current := range m.active {
		if current.admission.Member().Identity == admission.Member().Identity {
			m.mu.Unlock()
			return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("peer already owns a Snapshot tail"))
		}
	}
	m.active[tail.token] = tail
	m.mu.Unlock()
	// No manager lock across core calls: the dispatcher projects under subsMu.
	subscription, err := s.log.SubscribeTail(local+1, maxSnapshotTailEntries, tail.project)
	tail.subscription = subscription
	close(tail.opened)
	go tail.watch()
	if err != nil {
		tail.Close()
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return tail, nil
}

func (t *snapshotTail) project(entry mutationlog.Entry) (mutationlog.Entry, error) {
	charge, err := snapshotTailProjectionBudget(entry.Op)
	if err != nil {
		t.rememberTerminal("projection_budget")
		return mutationlog.Entry{}, err
	}
	m := t.manager
	m.mu.Lock()
	var reason snapshotTailReason
	switch {
	case t.closed:
		reason = "owner_closed"
	case charge > maxSnapshotTailBytes-t.bytes:
		reason = "tail_bytes"
	case charge > maxSnapshotTotalBytes-m.totalBytes:
		reason = "total_bytes"
	case t.entries >= maxSnapshotTailEntries:
		reason = "tail_entries"
	case m.totalEntries >= maxSnapshotTotalEntries:
		reason = "total_entries"
	}
	if reason != "" {
		t.rememberTerminalLocked(reason)
		m.mu.Unlock()
		return mutationlog.Entry{}, mutationlog.ErrSubscriptionLost
	}
	t.bytes += charge
	t.entries++
	m.totalBytes += charge
	m.totalEntries++
	m.mu.Unlock()
	// Graph projections are sized without cloning. Rich receipt projections
	// reserve their intrinsic maximum before construction, then refund slack.
	frame, err := subscribeMutationFrame(entry.Op)
	if err != nil {
		t.rememberTerminal("projection_build")
		return mutationlog.Entry{}, err // terminal owner cleanup refunds the charge
	}
	size := proto.Size(frame)
	if size > charge {
		t.rememberTerminal("projection_size")
		return mutationlog.Entry{}, mutationlog.ErrSubscriptionLost
	}
	m.mu.Lock()
	t.bytes -= charge - size
	m.totalBytes -= charge - size
	m.mu.Unlock()
	// Exact byte credits precede allocation of the retained serialized payload.
	data, err := proto.MarshalOptions{}.MarshalAppend(make([]byte, 0, size), frame)
	if err != nil {
		t.rememberTerminal("projection_encode")
		return mutationlog.Entry{}, err // watcher reclaims all credits on failure
	}
	return mutationlog.Entry{Seq: entry.Seq, HLC: entry.HLC, Op: snapshotTailFrame(data)}, nil
}

// All production log envelope types are explicit here. A full-graph projection
// can be arbitrarily large with an unlimited send cap, so inspect its existing
// message before any projector can clone it. Rich receipt wire mutations have
// the existing intrinsic 8 MiB inner-message ceiling, plus outer framing.
func snapshotTailProjectionBudget(op mutationlog.MutationOp) (int, error) {
	var mutation *pb.Mutation
	switch value := op.(type) {
	case *pb.Mutation:
		mutation = value
	case *graphMutationEnvelope:
		if value != nil {
			mutation = value.GraphMutation()
		}
	case *graphAddEffectEnvelope:
		mutation = value.GraphMutation()
	case *graphPutEffectEnvelope:
		mutation = value.GraphMutation()
	case *graphDeleteEffectEnvelope:
		mutation = value.GraphMutation()
	case *edgeCreateEnvelope:
		mutation = value.GraphMutation()
	case *edgeDeleteReceiptEnvelope, *edgeContributionDeleteReceiptEnvelope, *vertexPutReceiptEnvelope, *vertexDeleteReceiptEnvelope:
		return receiptVertexWALMaxBytes + 16, nil
	default:
		return 0, errors.New("unsupported Snapshot tail projection")
	}
	if mutation == nil {
		return 0, errors.New("empty Snapshot tail projection")
	}
	return proto.Size(&pb.SubscribeResponse{Event: &pb.SubscribeResponse_Mutation{Mutation: mutation}}), nil
}

func (t *snapshotTail) release(size int) {
	m := t.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.closed {
		t.bytes -= size
		t.entries--
		m.totalBytes -= size
		m.totalEntries--
	}
}

func (t *snapshotTail) finish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		guard := "context_canceled"
		if errors.Is(err, context.DeadlineExceeded) {
			guard = "context_deadline"
		}
		return t.diagnostic("finish", guard, "request_done")
	}
	if t.admission.Check(ctx) != nil {
		return t.diagnostic("finish", "admission_rejected", "admission_rejected")
	}
	if err := t.prepareRingReplay(); err != nil {
		return t.diagnostic("finish", "subscription_terminal", "")
	}
	if t.ringReplayRequired() {
		if err := t.subscription.ValidateReplay(t.local + 1); err != nil {
			return t.diagnostic("finish", "replay_unavailable", snapshotTailCoreReason(err))
		}
	}
	// Retirement/replay validation may wait on core locks. Do not publish ready
	// from the admission/context observation made before that wait.
	if err := ctx.Err(); err != nil {
		guard := "context_canceled"
		if errors.Is(err, context.DeadlineExceeded) {
			guard = "context_deadline"
		}
		return t.diagnostic("finish", guard, "request_done")
	}
	if t.admission.Check(ctx) != nil {
		return t.diagnostic("finish", "admission_rejected", "admission_rejected")
	}
	m := t.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-t.fault:
		return t.diagnosticLocked("finish", "generation_closed", "publication_fault")
	default:
	}
	if t.closed {
		return t.diagnosticLocked("finish", "owner_closed", "")
	}
	if !time.Now().Before(t.deadline) {
		return t.diagnosticLocked("finish", "deadline_expired", "deadline_expired")
	}
	t.ready = true
	return nil
}

func (t *snapshotTail) watch() {
	defer close(t.stopped)
	<-t.opened
	if t.subscription == nil {
		t.closeResourcesReason("subscription_missing")
		return
	}
	timer := time.NewTimer(time.Until(t.deadline))
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	requestDone := t.requestDone
	for {
		t.manager.mu.Lock()
		watched := t.subscription
		if t.ringReplay {
			watched = t.deliverySubscription
		}
		var subDone <-chan struct{}
		if watched != nil {
			subDone = watched.Done()
		}
		t.manager.mu.Unlock()
		select {
		case <-t.stop:
			return
		case <-t.fault:
			t.closeResourcesReason("publication_fault")
			return
		case <-t.updated:
			continue
		case <-subDone:
			if t.observeSubscriptionEnd(watched) {
				continue
			}
			t.closeResourcesReason("subscription_other")
			return
		case <-requestDone:
			t.manager.mu.Lock()
			ready := t.ready
			t.manager.mu.Unlock()
			if !ready {
				t.closeResourcesReason("request_done")
				return
			}
			requestDone = nil // successful Snapshot handler cancellation is expected
		case <-timer.C:
			t.manager.mu.Lock()
			claimed := t.claimed
			t.manager.mu.Unlock()
			if !claimed {
				if t.closeUnclaimedResources("deadline_expired") {
					return
				}
			}
		case <-ticker.C:
			t.manager.mu.Lock()
			claimed := t.claimed
			t.manager.mu.Unlock()
			if !claimed && t.admission.Check(context.Background()) != nil {
				if t.closeUnclaimedResources("admission_rejected") {
					return
				}
			}
			if t.unclaimedReplayLost() {
				t.closeResourcesReason("replay_unavailable")
				return
			}
		}
	}
}

func (t *snapshotTail) closeResources() {
	t.closeResourcesReason("owner_close")
}

func (t *snapshotTail) closeResourcesReason(reason snapshotTailReason) {
	t.transitionMu.Lock()
	defer t.transitionMu.Unlock()
	t.closeResourcesLocked(reason)
}

// A watcher may have sampled an unclaimed owner immediately before claim
// completed. Such a stale original-admission decision must not close delivery
// now owned by the fresh Subscribe admission.
func (t *snapshotTail) closeUnclaimedResources(reason snapshotTailReason) bool {
	t.transitionMu.Lock()
	defer t.transitionMu.Unlock()
	t.manager.mu.Lock()
	claimed := t.claimed
	t.manager.mu.Unlock()
	if claimed {
		return false
	}
	t.closeResourcesLocked(reason)
	return true
}

func (t *snapshotTail) closeResourcesLocked(reason snapshotTailReason) {
	t.closeOnce.Do(func() {
		m := t.manager
		m.mu.Lock()
		select {
		case <-t.opened:
			t.rememberCoreTerminalLocked()
			t.rememberTerminalLocked(reason)
		default:
			// Opening still owns the subscription pointer. Sample it only
			// after its existing completion barrier below.
		}
		t.closed = true
		claimed := t.claimed
		close(t.stop)
		m.mu.Unlock()
		<-t.opened
		m.mu.Lock()
		t.rememberCoreTerminalLocked()
		t.rememberTerminalLocked(reason)
		m.mu.Unlock()
		if t.subscription != nil {
			_ = t.subscription.Close() // joins project and releases every queued reference
		}
		if t.deliverySubscription != nil {
			_ = t.deliverySubscription.Close()
		}
		if !claimed {
			t.finalize()
		}
	})
}

// Claimed storage includes the synchronous HTTP holder, even after core Close
// discards the unread queue. Refund it only when that handler acknowledges exit.
func (t *snapshotTail) finalize() {
	t.releaseOnce.Do(func() {
		m := t.manager
		m.mu.Lock()
		delete(m.active, t.token)
		m.totalBytes -= t.bytes
		m.totalEntries -= t.entries
		t.bytes, t.entries = 0, 0
		close(t.released)
		m.mu.Unlock()
	})
}

func (t *snapshotTail) Close() {
	t.closeResources()
	<-t.stopped
	t.finalize()
}

// CloseSnapshotTails stops admission, closes every detached/claimed owner and
// joins its watcher. PeerPlaneServer calls it before HTTP shutdown, not after
// waiting for the later runtime Log.Close.
func (s *Replication) CloseSnapshotTails() {
	m := s.snapshotTails
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	tails := make([]*snapshotTail, 0, len(m.active))
	for _, tail := range m.active {
		tails = append(tails, tail)
	}
	m.mu.Unlock()
	for _, tail := range tails {
		tail.closeResourcesReason("shutdown")
	}
	for _, tail := range tails {
		<-tail.stopped
		<-tail.released
	}
}
