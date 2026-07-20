package replication

// Anti-entropy driver (#186).
//
// The Pump (#185) is the steady-state replication engine: each peer
// holds an open Subscribe stream and pushes deltas as they happen.
// In production every dropped or stalled stream is healed by the
// pump's reconnect loop, but the pump can only detect "my stream
// broke" — not "the peer applied a write but I never received the
// frame" (e.g. silent HTTP/2 keepalive miss, OS-level connection
// reset that the kernel hid from us).
//
// The AntiEntropy driver closes that hole. It periodically calls
// the unary LanternReplicationService.PeerStatus RPC on every
// configured peer and compares the peer's per-origin watermark to
// the local one. When the peer is ahead on its OWN origin (the
// only case anti-entropy can repair via this peer — remote-origin
// gaps must be healed by talking to the originating node, which is
// outside the scope of one peer's mutation log), the driver opens
// a bounded Subscribe(from_seq_per_origin = local_for_peer + 1) and applies
// up to the peer-reported target seq. FailedPrecondition triggers
// a Snapshot replay; later ticks retain that responder's local cutoff so
// Subscribe can resume the live tail without re-requesting an evicted prefix.
//
// LANTERN_ANTI_ENTROPY_INTERVAL controls the tick cadence. The
// default 30s is a deliberate compromise: short enough that a
// permanently-stuck pump heals before users notice divergence,
// long enough that the unary RPC load on healthy clusters is
// negligible. Set to 0 to disable.

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/prototime"

	"connectrpc.com/connect"
)

// LocalStateProvider is the surface the anti-entropy driver uses to
// read the local node's per-origin watermark. *service.LanternService
// satisfies it via OriginStates() — adapted here as a NodeID→seq map
// so the driver stays decoupled from the service package.
type LocalStateProvider interface {
	LocalSeq(origin hlc.NodeID) uint64
}

// AntiEntropyMetrics is the narrow surface the driver uses to publish
// per-tick counters. Wired by provider/metrics so the driver stays
// independent of prometheus.
//
// origin is the lowercase-hex encoding of the peer's self HLC NodeID
// (i.e. the origin row the driver is comparing against). peer is the
// peer's RPC address as configured in LANTERN_PEERS.
type AntiEntropyMetrics interface {
	OnAntiEntropyCycle()
	OnAntiEntropyTick(peer string)
	OnAntiEntropyBehind(peer, origin string, gap uint64)
	OnAntiEntropyCaughtUp(peer, origin string, applied uint64)
	OnAntiEntropyError(peer, reason string)
	OnSearchConfig(peer string, matched bool)
}

type nopAntiEntropyMetrics struct{}

func (nopAntiEntropyMetrics) OnAntiEntropyCycle()                          {}
func (nopAntiEntropyMetrics) OnAntiEntropyTick(string)                     {}
func (nopAntiEntropyMetrics) OnAntiEntropyBehind(string, string, uint64)   {}
func (nopAntiEntropyMetrics) OnAntiEntropyCaughtUp(string, string, uint64) {}
func (nopAntiEntropyMetrics) OnAntiEntropyError(string, string)            {}
func (nopAntiEntropyMetrics) OnSearchConfig(string, bool)                  {}

// AntiEntropyConfig groups the driver's inputs. All fields except
// Peers / Source / Interval are optional; NewAntiEntropy fills defaults.
type AntiEntropyConfig struct {
	// NodeID is the local node's HLC NodeID. Used to skip
	// PeerStatus rows where self_origin == local NodeID (the
	// peer should never be ahead of us on our OWN origin).
	NodeID hlc.NodeID

	// Peers is the static list of peer addresses to poll. Empty (or nil)
	// yields a no-op driver only when Source is also nil.
	Peers []string

	// Source, when non-nil, takes precedence over Peers and is resolved on
	// every anti-entropy cycle. This keeps periodic repair aligned with a
	// dynamic Pump topology such as Kubernetes headless-Service DNS.
	Source PeerSource

	// Interval is the tick cadence. 0 disables the driver
	// entirely (Run returns immediately). A negative value is
	// clamped to the default.
	Interval time.Duration

	// SubscribeTimeout caps the duration of a single
	// catch-up Subscribe stream. Defaults to 30s. Bounding the
	// stream avoids tying up resources when a peer reports a
	// huge gap that would take minutes to drain — the next
	// tick will resume from where we stopped.
	SubscribeTimeout time.Duration

	// GapWarnThreshold escalates the per-peer catch-up log from
	// info to an additional warn when (peer_seq - local_seq)
	// exceeds this many mutations. 0 disables the warn (the
	// info log still fires). The standard catch-up info log is
	// always emitted regardless of this knob.
	GapWarnThreshold uint64

	// AuthToken, when non-empty, is attached as "Authorization: Bearer"
	// to every outbound anti-entropy call so gap repair works against
	// peers running with LANTERN_AUTH_TOKENS (#850).
	AuthToken string

	// SearchConfigFingerprint is compared with every PeerStatus response.
	// Empty disables the comparison for narrow unit-test wiring; production
	// always supplies the LanternService fingerprint.
	SearchConfigFingerprint string

	// HTTPClient is the http.Client used to open Connect-Go streams
	// against each peer. Defaults to defaultH2CClient() (plaintext
	// HTTP/2 for the in-cluster HA topology).
	HTTPClient *http.Client

	// Logger receives lifecycle events. slog.Default() when nil.
	Logger *slog.Logger

	// Metrics receives per-tick events. nopAntiEntropyMetrics{} when nil.
	Metrics AntiEntropyMetrics
}

// AntiEntropy is the periodic convergence driver. Construct with
// NewAntiEntropy and start with Run(ctx). Run blocks until ctx is
// cancelled (or returns immediately when both Peers and Source are empty, or
// Interval is 0), so it is meant to run alongside the Connect listener and
// pump in an errgroup.
type AntiEntropy struct {
	cfg         AntiEntropyConfig
	local       LocalStateProvider
	apply       MutationApplier
	snap        SnapshotApplier
	resumeMu    sync.Mutex
	resumeLocal map[string]uint64
}

// NewAntiEntropy constructs the driver. local MUST be the same
// LanternService that backs ApplyMutation so the watermark observed
// at compare-time matches the state that newly-applied mutations
// will update. apply / snap are the same seams the pump uses.
func NewAntiEntropy(cfg AntiEntropyConfig, local LocalStateProvider, apply MutationApplier, snap SnapshotApplier) *AntiEntropy {
	if cfg.Interval < 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.SubscribeTimeout <= 0 {
		cfg.SubscribeTimeout = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = nopAntiEntropyMetrics{}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = defaultH2CClient()
	}
	cfg.HTTPClient = withAuthToken(cfg.HTTPClient, cfg.AuthToken)
	return &AntiEntropy{
		cfg: cfg, local: local, apply: apply, snap: snap,
		resumeLocal: make(map[string]uint64),
	}
}

// Run starts the periodic tick loop and blocks until ctx is
// cancelled. Returns nil after all in-flight ticks have completed.
// A driver with neither peers nor a Source, or with a zero interval, is a
// no-op.
func (a *AntiEntropy) Run(ctx context.Context) error {
	if (len(a.cfg.Peers) == 0 && a.cfg.Source == nil) || a.cfg.Interval == 0 {
		a.cfg.Logger.Info("anti-entropy: disabled",
			slog.Int("peers", len(a.cfg.Peers)),
			slog.Bool("dynamic_source", a.cfg.Source != nil),
			slog.Duration("interval", a.cfg.Interval))
		return nil
	}
	a.cfg.Logger.Info("anti-entropy: starting",
		slog.Int("peers", len(a.cfg.Peers)),
		slog.Bool("dynamic_source", a.cfg.Source != nil),
		slog.Duration("interval", a.cfg.Interval))

	t := time.NewTicker(a.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.cfg.Logger.Info("anti-entropy: stopped")
			return nil
		case <-t.C:
			a.tickAll(ctx)
		}
	}
}

// tickAll fans out one PeerStatus probe per peer in parallel.
// Per-peer errors are logged and metric'd but never propagated —
// anti-entropy is best-effort.
func (a *AntiEntropy) tickAll(ctx context.Context) {
	a.cfg.Metrics.OnAntiEntropyCycle()
	peers := a.cfg.Peers
	if a.cfg.Source != nil {
		resolved, err := a.cfg.Source.Resolve(ctx)
		if err != nil {
			a.cfg.Logger.Warn("anti-entropy: peer discovery failed", slog.Any("err", err))
			a.cfg.Metrics.OnAntiEntropyError("discovery", "discovery_failed")
			return
		}
		peers = resolved
	}
	var wg sync.WaitGroup
	for _, addr := range peers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			a.tickPeer(ctx, addr)
		}(addr)
	}
	wg.Wait()
}

// tickPeer runs one PeerStatus probe + optional catch-up against a
// single peer. Errors are logged and metric'd; the next tick will
// retry.
func (a *AntiEntropy) tickPeer(ctx context.Context, addr string) {
	a.cfg.Metrics.OnAntiEntropyTick(addr)
	log := a.cfg.Logger.With(slog.String("peer", addr))

	cli := graphv1connect.NewLanternReplicationServiceClient(
		a.cfg.HTTPClient, peerBaseURL(addr),
	)

	resp, err := cli.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
	if err != nil {
		log.Warn("anti-entropy: PeerStatus failed", slog.Any("err", err))
		a.cfg.Metrics.OnAntiEntropyError(addr, "peerstatus_failed")
		return
	}
	msg := resp.Msg
	if a.cfg.SearchConfigFingerprint != "" {
		remote := msg.GetSearchConfigFingerprint()
		matched := remote != "" && remote == a.cfg.SearchConfigFingerprint
		a.cfg.Metrics.OnSearchConfig(addr, matched)
		if !matched {
			log.Error("anti-entropy: search config mismatch",
				slog.String("local_fingerprint", a.cfg.SearchConfigFingerprint),
				slog.String("peer_fingerprint", remote))
		}
	}
	if len(msg.GetSelfOrigin()) == 0 {
		// Peer cannot identify itself — older binary or
		// replication unwired on the peer side. Nothing to do.
		return
	}
	var peerNID hlc.NodeID
	copy(peerNID[:], msg.GetSelfOrigin())
	// Self-skip: a configured peer pointing back to ourselves (e.g.
	// stale DNS in a single-node test) should be ignored.
	if peerNID == a.cfg.NodeID {
		return
	}

	// Find the peer's own row in its OriginStates list. That row
	// is the only one we can repair via this peer — non-self
	// origins represent mutations the peer learned from elsewhere,
	// and the peer's mutation log does not contain them (apply
	// bypasses logMutation), so Subscribe to this peer would not
	// replay them.
	var target uint64
	found := false
	for _, row := range msg.GetOrigins() {
		if len(row.GetOrigin()) != len(peerNID) {
			continue
		}
		match := true
		for i := range peerNID {
			if row.GetOrigin()[i] != peerNID[i] {
				match = false
				break
			}
		}
		if match {
			target = row.GetLastSeq()
			found = true
			break
		}
	}
	if !found || target == 0 {
		// Peer has never appended anything, or no row for self.
		return
	}

	localSeq := a.local.LocalSeq(peerNID)
	if localSeq >= target {
		return
	}
	gap := target - localSeq
	originHex := hex.EncodeToString(peerNID[:])
	a.cfg.Metrics.OnAntiEntropyBehind(addr, originHex, gap)
	log.Info("anti-entropy: peer ahead, catching up",
		slog.Uint64("local_seq", localSeq),
		slog.Uint64("peer_seq", target),
		slog.Uint64("gap", gap))
	if a.cfg.GapWarnThreshold > 0 && gap > a.cfg.GapWarnThreshold {
		log.Warn("anti-entropy: gap exceeds warn threshold",
			slog.String("origin", originHex),
			slog.Uint64("gap", gap),
			slog.Uint64("threshold", a.cfg.GapWarnThreshold))
	}

	applied, err := a.catchUp(ctx, addr, cli, peerNID, localSeq+1, target)
	if err != nil {
		log.Warn("anti-entropy: catch-up failed",
			slog.Any("err", err), slog.Uint64("applied", applied))
		a.cfg.Metrics.OnAntiEntropyError(addr, "catchup_failed")
		return
	}
	a.cfg.Metrics.OnAntiEntropyCaughtUp(addr, originHex, applied)
	log.Info("anti-entropy: caught up",
		slog.Uint64("applied", applied),
		slog.Uint64("target_seq", target))
}

// catchUp opens a bounded Subscribe stream and applies mutations
// until the local tracker for peerNID has reached or exceeded
// target. FailedPrecondition triggers a Snapshot replay (which
// itself advances local watermarks via the SnapshotApplier seams)
// after which catchUp returns — the next tick will re-probe.
//
// Under the leaderless Subscribe contract (#415), the peer's log
// carries entries from every cluster origin. We narrow the request to
// peerNID's own origin via a per-origin cursor so the server only
// streams the entries we actually care about, avoiding wasted
// bandwidth on cross-origin entries that this catch-up call would
// drop anyway.
//
// Returns the number of mutations applied during this call.
func (a *AntiEntropy) catchUp(ctx context.Context, addr string, cli graphv1connect.LanternReplicationServiceClient, peerNID hlc.NodeID, fromSeq, target uint64) (uint64, error) {
	tctx, cancel := context.WithTimeout(ctx, a.cfg.SubscribeTimeout)
	defer cancel()

	cursor := map[string]uint64{hex.EncodeToString(peerNID[:]): fromSeq}
	stream, err := cli.Subscribe(tctx, connect.NewRequest(&pb.SubscribeRequest{
		FromSeqPerOrigin: cursor,
		FromLocalSeq:     a.snapshotResumeLocal(addr),
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			return 0, a.snapshotFrom(ctx, addr, cli)
		}
		return 0, err
	}
	defer func() { _ = stream.Close() }()
	var applied uint64
	for stream.Receive() {
		resp := stream.Msg()
		mu := resp.GetMutation()
		if mu == nil {
			continue
		}
		if err := a.apply.ApplyMutation(ctx, mu); err != nil {
			return applied, err
		}
		applied++
		// Stop once we've reached the target watermark for the
		// peer's own origin. We compare via the local provider
		// rather than mu.Seq because Subscribe streams all of
		// the peer's appended mutations (which are by definition
		// peer-origin), but the watermark check stays robust if
		// the peer ever starts forwarding additional origins on
		// the same stream in a future revision.
		if a.local.LocalSeq(peerNID) >= target {
			return applied, nil
		}
	}
	recvErr := stream.Err()
	if recvErr == nil || errors.Is(recvErr, io.EOF) {
		return applied, nil
	}
	if connect.CodeOf(recvErr) == connect.CodeFailedPrecondition {
		return applied, a.snapshotFrom(ctx, addr, cli)
	}
	// Deadline-exceeded from the subscribe timeout is expected
	// when the catch-up window is shorter than the gap — surface as
	// nil so the tick is accounted as a partial success, the next
	// tick will resume.
	if errors.Is(recvErr, context.DeadlineExceeded) || connect.CodeOf(recvErr) == connect.CodeDeadlineExceeded {
		return applied, nil
	}
	return applied, recvErr
}

// snapshotFrom replays a full Snapshot from the peer into the local
// cache. Triggered by FailedPrecondition on Subscribe. After this
// returns, the next anti-entropy tick will re-probe PeerStatus and
// resume normal catch-up.
func (a *AntiEntropy) snapshotFrom(ctx context.Context, addr string, cli graphv1connect.LanternReplicationServiceClient) error {
	stream, err := cli.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	var recovery searchIndexRecovery
	if candidate, ok := a.snap.(searchIndexRecovery); ok {
		recovery = candidate
		recovery.BeginSearchIndexRecovery()
	}
	var header *pb.SnapshotHeader
	for stream.Receive() {
		resp := stream.Msg()
		switch e := resp.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			header = e.Header
		case *pb.SnapshotResponse_Vertex:
			sv := e.Vertex
			v := sv.GetVertex()
			if v == nil {
				continue
			}
			a.snap.PutVertexWithExpirationHLC(
				v.GetKey(), v, prototime.Expiration(v.GetExpiration()),
				snapshotHLC(sv.GetHlc()),
			)
		case *pb.SnapshotResponse_Edge:
			se := e.Edge
			edgeHLC := snapshotHLC(se.GetHlc())
			for _, c := range se.GetContributions() {
				var cid graphcache.ContribID
				copy(cid[:], c.GetContribId())
				applySnapshotEdge(
					a.snap, se.GetTail(), se.GetHead(), c.GetWeight(),
					prototime.Expiration(c.GetExpiration()), cid, edgeHLC,
				)
			}
		case *pb.SnapshotResponse_Footer:
			// no-op; footer counts are validated by the pump
			// path. Anti-entropy is best-effort.
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if recovery != nil {
		if err := recovery.CompleteSearchIndexRecovery(); err != nil {
			a.cfg.Logger.Warn("anti-entropy: snapshot rebuilt graph but search index remains incomplete",
				slog.Any("err", err))
		}
	}
	if marks, ok := a.apply.(snapshotWatermarkApplier); ok && header != nil {
		if err := marks.ApplySnapshotWatermarks(header.GetCutoffSeqPerOrigin(), snapshotHLC(header.GetCutoffHlc())); err != nil {
			return err
		}
	}
	if header != nil {
		resume := resumeAfterSnapshot(header)
		a.resumeMu.Lock()
		a.resumeLocal[addr] = resume.local
		a.resumeMu.Unlock()
	}
	return nil
}

func (a *AntiEntropy) snapshotResumeLocal(addr string) uint64 {
	a.resumeMu.Lock()
	defer a.resumeMu.Unlock()
	return a.resumeLocal[addr]
}
