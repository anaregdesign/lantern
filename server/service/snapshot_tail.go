package service

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
	// All following state is protected by manager.mu.
	ready   bool
	claimed bool
	closed  bool
	bytes   int
	entries int
}

type snapshotTailFrame []byte

func (f snapshotTailFrame) replicationFrameSize() (int, error) { return len(f), nil }

func snapshotTailError() error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("Snapshot tail unavailable; obtain a new Snapshot"))
}

func (s *LanternReplicationService) snapshotTailRequest(ctx context.Context) (bool, replicationTailMetadata, error) {
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
	owner, ok := s.origins.(*LanternService)
	if !ok || s.log == nil || owner.log != s.log || s.snapshotTails == nil {
		return false, metadata, snapshotTailError()
	}
	return true, metadata, nil
}

// Called inside the same graph/receipt publication cut that captures Snapshot.
func (s *LanternReplicationService) openSnapshotTail(ctx context.Context, local uint64, origins map[string]uint64, fault <-chan struct{}) (*snapshotTail, error) {
	if len(origins) > maxSnapshotTailOrigins {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("Snapshot tail origin limit"))
	}
	admission, ok := peerauth.AdmissionFromContext(ctx)
	deadline, finite := ctx.Deadline()
	if !ok || !finite || !time.Now().Before(deadline) || admission.Check(ctx) != nil || fault == nil || local == math.MaxUint64 {
		return nil, snapshotTailError()
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
		return mutationlog.Entry{}, err
	}
	m := t.manager
	m.mu.Lock()
	if t.closed || charge > maxSnapshotTailBytes-t.bytes || charge > maxSnapshotTotalBytes-m.totalBytes ||
		t.entries >= maxSnapshotTailEntries || m.totalEntries >= maxSnapshotTotalEntries {
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
		return mutationlog.Entry{}, err // terminal owner cleanup refunds the charge
	}
	size := proto.Size(frame)
	if size > charge {
		return mutationlog.Entry{}, mutationlog.ErrSubscriptionLost
	}
	m.mu.Lock()
	t.bytes -= charge - size
	m.totalBytes -= charge - size
	m.mu.Unlock()
	// Exact byte credits precede allocation of the retained serialized payload.
	data, err := proto.MarshalOptions{}.MarshalAppend(make([]byte, 0, size), frame)
	if err != nil {
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
	if ctx.Err() != nil || t.admission.Check(ctx) != nil || t.subscription.Err() != nil {
		return snapshotTailError()
	}
	m := t.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-t.fault:
		return snapshotTailError()
	default:
	}
	if t.closed || !time.Now().Before(t.deadline) {
		return snapshotTailError()
	}
	t.ready = true
	return nil
}

// claim runs inside the current publication cut, and bypasses only ring replay
// admission. It never bypasses per-frame validation or receiver ApplyMutation.
func (s *LanternReplicationService) claimSnapshotTail(ctx context.Context, token string, req *pb.SubscribeRequest, cursor map[string]uint64, fault <-chan struct{}) (*snapshotTail, error) {
	current, ok := peerauth.AdmissionFromContext(ctx)
	if !ok || current.Check(ctx) != nil {
		return nil, connect.NewError(connect.CodeUnavailable, peerauth.ErrMembership)
	}
	m := s.snapshotTails
	m.mu.Lock()
	tail := m.active[token]
	m.mu.Unlock()
	if tail == nil || !tail.admission.SamePeer(current) || tail.admission.Check(ctx) != nil {
		return nil, snapshotTailError()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tail.closed || !tail.ready || tail.claimed || tail.subscription.Err() != nil || tail.fault != fault || !time.Now().Before(tail.deadline) || req.GetFromLocalSeq() != tail.local+1 {
		return nil, snapshotTailError()
	}
	select {
	case <-tail.fault:
		return nil, snapshotTailError()
	default:
	}
	for origin, cutoff := range tail.origins {
		if cutoff == math.MaxUint64 || cursor[origin] < cutoff+1 {
			return nil, snapshotTailError()
		}
	}
	tail.claimed = true
	return tail, nil
}

func (t *snapshotTail) watch() {
	defer close(t.stopped)
	<-t.opened
	if t.subscription == nil {
		t.closeResources()
		return
	}
	timer := time.NewTimer(time.Until(t.deadline))
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	requestDone := t.requestDone
	for {
		select {
		case <-t.stop:
			return
		case <-t.fault:
			t.closeResources()
			return
		case <-t.subscription.Done():
			t.closeResources()
			return
		case <-requestDone:
			t.manager.mu.Lock()
			ready := t.ready
			t.manager.mu.Unlock()
			if !ready {
				t.closeResources()
				return
			}
			requestDone = nil // successful Snapshot handler cancellation is expected
		case <-timer.C:
			t.manager.mu.Lock()
			claimed := t.claimed
			t.manager.mu.Unlock()
			if !claimed {
				t.closeResources()
				return
			}
		case <-ticker.C:
			t.manager.mu.Lock()
			claimed := t.claimed
			t.manager.mu.Unlock()
			if !claimed && t.admission.Check(context.Background()) != nil {
				t.closeResources()
				return
			}
		}
	}
}

func (t *snapshotTail) closeResources() {
	t.closeOnce.Do(func() {
		m := t.manager
		m.mu.Lock()
		t.closed = true
		claimed := t.claimed
		close(t.stop)
		m.mu.Unlock()
		<-t.opened
		if t.subscription != nil {
			_ = t.subscription.Close() // joins project and releases every queued reference
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
func (s *LanternReplicationService) CloseSnapshotTails() {
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
		tail.closeResources()
	}
	for _, tail := range tails {
		<-tail.stopped
		<-tail.released
	}
}
