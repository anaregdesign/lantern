// Package replication implements the outbound peer-replication pump (#185).
//
// One *Pump owns one goroutine per peer address in LANTERN_PEERS. Each
// goroutine maintains a long-lived gRPC connection to its peer and, in
// order:
//
//  1. Opens LanternReplicationService.Subscribe(from_seq=N), where N is
//     the next seq the local node expects from THAT peer (tracked
//     per-peer across reconnects, starts at 0 on first connect).
//  2. If the server replies codes.FailedPrecondition (reason "gapped" —
//     the canonical bootstrap signal from #180), opens
//     LanternReplicationService.Snapshot, replays the Header→Vertex→Edge
//     frames into the local cache, then resumes Subscribe at
//     header.cutoff_seq + 1.
//  3. Applies every received Mutation via the local MutationApplier
//     (LanternService.ApplyMutation, which bypasses the local mutation
//     log so replayed writes are NOT re-broadcast).
//  4. On any other error (connection drop, peer crash, transient
//     Unavailable) the goroutine reconnects with exponential backoff
//     capped at BackoffMax.
//
// Self-echo suppression: a Mutation whose Origin == local NodeID is
// dropped on receipt. The replication design is symmetric — every peer
// streams its OWN appended mutations to every subscriber, so receivers
// must filter their own writes back out.
//
// LANTERN_PEERS="" (the default) yields a no-op pump: Run returns
// immediately and no goroutines are spawned. This is the
// single-instance mode used on serverless PaaS (Cloud Run / ACA);
// the rest of the server behaves identically.
package replication

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// MutationApplier is the narrow surface the pump uses to replay a
// remote Mutation against the local cache. *service.LanternService
// satisfies it (its ApplyMutation deliberately bypasses logMutation so
// peer-applied writes are not re-broadcast).
type MutationApplier interface {
	ApplyMutation(ctx context.Context, m *pb.Mutation) error
}

// SnapshotApplier is the surface the pump uses to replay snapshot
// frames into the local graph cache. *cachegraph.GraphCache[string,
// *pb.Vertex] satisfies it directly via its HLC + ContribID seams added
// in #181.
type SnapshotApplier interface {
	PutVertexWithExpirationHLC(key string, value *pb.Vertex, exp time.Time, ts hlc.Timestamp) bool
	AddEdgeWithExpirationContribHLC(tail, head string, w float32, exp time.Time, cid cachegraph.ContribID, ts hlc.Timestamp) bool
}

// Metrics is the narrow surface the pump uses to publish per-peer
// counters. Wiring the prometheus collectors themselves lands in #187;
// for now we expose just the hook signatures so the pump compiles in
// isolation. Pass nopMetrics{} (the zero value via the default in
// NewPump) when no metrics handle is wired.
type Metrics interface {
	OnPumpConnect(peer string)
	OnPumpDisconnect(peer string, reason string)
	OnPumpApply(peer string)
	OnPumpDropSelfEcho(peer string)
	OnPumpSnapshotReplayed(peer string, vertices, edges uint64)
}

type nopMetrics struct{}

func (nopMetrics) OnPumpConnect(string)                          {}
func (nopMetrics) OnPumpDisconnect(string, string)               {}
func (nopMetrics) OnPumpApply(string)                            {}
func (nopMetrics) OnPumpDropSelfEcho(string)                     {}
func (nopMetrics) OnPumpSnapshotReplayed(string, uint64, uint64) {}

// Config groups the inputs every Pump goroutine needs. All fields
// except Peers are required to be valid; NewPump fills sensible
// defaults for the optional ones.
type Config struct {
	// NodeID is the local node's HLC NodeID. Mutations whose
	// Origin matches NodeID are dropped on receipt to suppress
	// self-echo. The zero value disables self-filtering entirely
	// (use only in tests where every node has a distinct NodeID).
	NodeID hlc.NodeID

	// Peers is the static list of peer addresses to subscribe to.
	// Each entry is consumed by grpc.NewClient verbatim
	// ("host:port"). Empty (or nil) yields a no-op pump.
	Peers []string

	// DialOptions are passed to grpc.NewClient for each peer
	// connection. When empty, insecure transport credentials are
	// used — sufficient for the in-cluster Tier-A topology where
	// peers are reachable only via the cluster network.
	DialOptions []grpc.DialOption

	// BackoffMin is the initial reconnect delay after a session
	// error. Doubles on each successive failure, capped at
	// BackoffMax. Reset to BackoffMin on every successful Subscribe
	// frame.
	BackoffMin time.Duration

	// BackoffMax is the upper cap on the reconnect delay.
	BackoffMax time.Duration

	// Logger is used for connection lifecycle and self-echo
	// warnings. slog.Default() is used when nil.
	Logger *slog.Logger

	// Metrics receives per-peer lifecycle events. nopMetrics{} is
	// used when nil.
	Metrics Metrics
}

// Pump is the long-running peer-replication driver. Construct with
// NewPump and start with Run(ctx). Run blocks until ctx is cancelled
// (or returns immediately when Peers is empty), so it is meant to be
// invoked from inside an errgroup alongside the gRPC server.
type Pump struct {
	cfg   Config
	apply MutationApplier
	snap  SnapshotApplier
}

// NewPump constructs the pump. apply MUST be the local
// LanternService instance so replayed mutations are not re-broadcast;
// snap MUST be the same underlying graph cache the read RPCs serve so
// snapshot replays converge with subsequent Subscribe deltas.
func NewPump(cfg Config, apply MutationApplier, snap SnapshotApplier) *Pump {
	if cfg.BackoffMin <= 0 {
		cfg.BackoffMin = 250 * time.Millisecond
	}
	if cfg.BackoffMax <= 0 || cfg.BackoffMax < cfg.BackoffMin {
		cfg.BackoffMax = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = nopMetrics{}
	}
	if len(cfg.DialOptions) == 0 {
		cfg.DialOptions = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	return &Pump{cfg: cfg, apply: apply, snap: snap}
}

// Run starts one goroutine per peer and blocks until ctx is
// cancelled. Returns nil after all goroutines have exited. A pump
// with no configured peers is a no-op and returns immediately.
func (p *Pump) Run(ctx context.Context) error {
	if len(p.cfg.Peers) == 0 {
		p.cfg.Logger.Info("replication pump: no peers configured, running in single-instance mode")
		return nil
	}
	p.cfg.Logger.Info("replication pump: starting", slog.Int("peers", len(p.cfg.Peers)))

	var wg sync.WaitGroup
	for _, addr := range p.cfg.Peers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			p.runPeer(ctx, addr)
		}(addr)
	}
	wg.Wait()
	p.cfg.Logger.Info("replication pump: stopped")
	return nil
}

// runPeer is the per-peer reconnect loop. Each iteration represents
// one Subscribe (and optional Snapshot) session. On session error,
// the loop sleeps for the current backoff (doubling, capped at
// BackoffMax) and retries until ctx is cancelled.
func (p *Pump) runPeer(ctx context.Context, addr string) {
	log := p.cfg.Logger.With(slog.String("peer", addr))
	backoff := p.cfg.BackoffMin
	// fromSeq is the next seq we expect from THIS peer. Survives
	// across reconnects so transient disconnects don't trigger a
	// full snapshot — Subscribe(fromSeq) just resumes.
	var fromSeq uint64

	for ctx.Err() == nil {
		nextSeq, err := p.session(ctx, addr, fromSeq)
		fromSeq = nextSeq
		if err == nil {
			// session ended cleanly (ctx cancelled mid-stream).
			return
		}
		if ctx.Err() != nil {
			return
		}
		log.Warn("replication pump: peer session error",
			slog.Any("err", err), slog.Duration("backoff", backoff))

		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		backoff *= 2
		if backoff > p.cfg.BackoffMax {
			backoff = p.cfg.BackoffMax
		}
	}
}

// session runs one Subscribe attempt against addr, falling back to a
// Snapshot+Subscribe bootstrap when the server reports the requested
// from_seq as gapped. Returns the next from_seq to resume at on the
// next attempt, plus any non-nil error (nil means clean ctx-cancel
// exit).
func (p *Pump) session(ctx context.Context, addr string, fromSeq uint64) (uint64, error) {
	conn, err := grpc.NewClient(addr, p.cfg.DialOptions...)
	if err != nil {
		return fromSeq, err
	}
	defer func() { _ = conn.Close() }()
	cli := pb.NewLanternReplicationServiceClient(conn)

	p.cfg.Metrics.OnPumpConnect(addr)

	next, err := p.subscribe(ctx, cli, addr, fromSeq)
	if err == nil {
		p.cfg.Metrics.OnPumpDisconnect(addr, "clean")
		return next, nil
	}
	if ctx.Err() != nil {
		p.cfg.Metrics.OnPumpDisconnect(addr, "ctx_cancel")
		return next, nil
	}
	if status.Code(err) == codes.FailedPrecondition {
		// gapped: snapshot, then resume from cutoff+1.
		cutoff, sErr := p.snapshot(ctx, cli, addr)
		if sErr != nil {
			p.cfg.Metrics.OnPumpDisconnect(addr, "snapshot_failed")
			return next, sErr
		}
		next, err = p.subscribe(ctx, cli, addr, cutoff+1)
		if err == nil {
			p.cfg.Metrics.OnPumpDisconnect(addr, "clean")
			return next, nil
		}
	}
	p.cfg.Metrics.OnPumpDisconnect(addr, "subscribe_failed")
	return next, err
}

// subscribe opens a Subscribe stream at fromSeq and applies every
// received Mutation. Self-origin mutations are dropped before
// dispatch. Returns the seq of the last successfully-applied mutation
// + 1 (i.e. the next seq to resume at) and any error from Recv/Apply.
// io.EOF is treated as a normal end-of-stream (returns nil).
func (p *Pump) subscribe(ctx context.Context, cli pb.LanternReplicationServiceClient, addr string, fromSeq uint64) (uint64, error) {
	stream, err := cli.Subscribe(ctx, &pb.SubscribeRequest{FromSeq: fromSeq})
	if err != nil {
		return fromSeq, err
	}
	next := fromSeq
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return next, nil
		}
		if err != nil {
			return next, err
		}
		mu := resp.GetMutation()
		if mu == nil {
			continue
		}
		if p.isSelfEcho(mu) {
			p.cfg.Metrics.OnPumpDropSelfEcho(addr)
			next = mu.GetSeq() + 1
			continue
		}
		if err := p.apply.ApplyMutation(ctx, mu); err != nil {
			return next, err
		}
		p.cfg.Metrics.OnPumpApply(addr)
		next = mu.GetSeq() + 1
	}
}

// snapshot opens a Snapshot stream and replays every Header / Vertex
// / Edge frame into the local cache via the SnapshotApplier seams.
// Returns the cutoff_seq from the Header (the value the caller
// should pass as fromSeq-1 on the subsequent Subscribe).
func (p *Pump) snapshot(ctx context.Context, cli pb.LanternReplicationServiceClient, addr string) (uint64, error) {
	stream, err := cli.Snapshot(ctx, &pb.SnapshotRequest{})
	if err != nil {
		return 0, err
	}
	var (
		cutoff       uint64
		gotHeader    bool
		vertexCount  uint64
		edgeCount    uint64
		sawFooter    bool
		footerCounts struct{ v, e uint64 }
	)
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return cutoff, err
		}
		switch e := resp.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			if gotHeader {
				return cutoff, status.Error(codes.Internal, "snapshot: duplicate header frame")
			}
			cutoff = e.Header.GetCutoffSeq()
			gotHeader = true
		case *pb.SnapshotResponse_Vertex:
			if !gotHeader {
				return cutoff, status.Error(codes.Internal, "snapshot: vertex frame before header")
			}
			if sawFooter {
				return cutoff, status.Error(codes.Internal, "snapshot: vertex frame after footer")
			}
			sv := e.Vertex
			v := sv.GetVertex()
			if v != nil {
				p.snap.PutVertexWithExpirationHLC(
					v.GetKey(), v, v.GetExpiration().AsTime(),
					snapshotHLC(sv.GetHlc()),
				)
				vertexCount++
			}
		case *pb.SnapshotResponse_Edge:
			if !gotHeader {
				return cutoff, status.Error(codes.Internal, "snapshot: edge frame before header")
			}
			if sawFooter {
				return cutoff, status.Error(codes.Internal, "snapshot: edge frame after footer")
			}
			se := e.Edge
			edgeHLC := snapshotHLC(se.GetHlc())
			for _, c := range se.GetContributions() {
				var cid cachegraph.ContribID
				copy(cid[:], c.GetContribId())
				p.snap.AddEdgeWithExpirationContribHLC(
					se.GetTail(), se.GetHead(), c.GetWeight(),
					c.GetExpiration().AsTime(), cid, edgeHLC,
				)
			}
			edgeCount++
		case *pb.SnapshotResponse_Footer:
			sawFooter = true
			footerCounts.v = e.Footer.GetVertexCount()
			footerCounts.e = e.Footer.GetEdgeCount()
		}
	}
	if !gotHeader {
		return cutoff, status.Error(codes.Internal, "snapshot: stream ended before header")
	}
	if !sawFooter {
		return cutoff, status.Error(codes.Internal, "snapshot: stream ended before footer")
	}
	if footerCounts.v != vertexCount || footerCounts.e != edgeCount {
		// Count mismatch is a soft warning, not a hard error — the
		// applied data still converges via HLC/ContribID. Surface it
		// so operators notice corrupt snapshots.
		p.cfg.Logger.Warn("replication pump: snapshot count mismatch",
			slog.String("peer", addr),
			slog.Uint64("vertices_applied", vertexCount),
			slog.Uint64("vertices_footer", footerCounts.v),
			slog.Uint64("edges_applied", edgeCount),
			slog.Uint64("edges_footer", footerCounts.e))
	}
	p.cfg.Metrics.OnPumpSnapshotReplayed(addr, vertexCount, edgeCount)
	return cutoff, nil
}

// isSelfEcho returns true when mu was originated by the local node.
// The zero NodeID disables filtering entirely (test-only path —
// production always sets a non-zero NodeID, either from
// LANTERN_NODE_ID or the crypto/rand fallback).
func (p *Pump) isSelfEcho(mu *pb.Mutation) bool {
	var zero hlc.NodeID
	if p.cfg.NodeID == zero {
		return false
	}
	return bytes.Equal(mu.GetOrigin(), p.cfg.NodeID[:])
}

// snapshotHLC converts the wire HLCTimestamp into an in-process
// hlc.Timestamp. Mirrors server/service.hlcFromProto (which is
// package-internal there).
func snapshotHLC(p *pb.HLCTimestamp) hlc.Timestamp {
	if p == nil {
		return hlc.Timestamp{}
	}
	var nid hlc.NodeID
	copy(nid[:], p.GetNodeId())
	return hlc.Timestamp{
		WallNs:  p.GetWallNs(),
		Logical: p.GetLogical(),
		NodeID:  nid,
	}
}
