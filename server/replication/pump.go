// Package replication implements the outbound peer-replication pump (#185).
//
// One *Pump owns one goroutine per peer address in LANTERN_PEERS. Each
// goroutine maintains a long-lived Connect-Go HTTP/2 stream to its peer
// and, in order:
//
//  1. Verifies PeerStatus search-config compatibility, then opens
//     LanternReplicationService.Subscribe with an empty portable cursor.
//  2. If the server replies codes.FailedPrecondition (reason "gapped" —
//     the canonical bootstrap signal from #180), opens
//     LanternReplicationService.Snapshot, hands the complete stream to the
//     configured format-specific installer, then resumes against the same responder at
//     both header origin cutoffs + 1 and header.cutoff_local_seq + 1.
//  3. Applies every received Mutation via the local MutationApplier
//     (LanternService.ApplyMutation). Reading B appends a newly-observed remote
//     mutation to the local log so any replica can serve the full cluster
//     stream; the per-origin watermark prevents relay loops.
//  4. On any other error (connection drop, peer crash, transient
//     Unavailable) the goroutine reconnects with exponential backoff
//     capped at BackoffMax.
//
// Self-echo suppression: a Mutation whose Origin == local NodeID is
// dropped on receipt. The replication design is symmetric — every peer
// log carries entries from every cluster origin, so receivers must filter
// their own writes back out.
//
// LANTERN_PEERS="" (the default) yields a no-op pump: Run returns
// immediately and no goroutines are spawned. This is single-instance
// mode; the rest of the server behaves identically.
package replication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/prototime"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MutationApplier is the narrow surface the pump uses to replay a
// remote Mutation against the local cache. *service.LanternService
// satisfies it (its ApplyMutation deliberately bypasses logMutation so
// peer-applied writes are not re-broadcast).
type MutationApplier interface {
	ApplyMutation(ctx context.Context, m *pb.Mutation) error
}

// SnapshotApplier is the surface the default graph-only installer uses to
// replay frames into the local graph cache. *graphcache.GraphCache[string,
// *pb.Vertex] satisfies it directly via its HLC + ContribID seams added in
// #181.
type SnapshotApplier interface {
	PutVertexWithExpirationHLC(key string, value *pb.Vertex, exp time.Time, ts hlc.Timestamp) bool
	AddEdgeWithExpirationContribHLC(tail, head string, w float32, exp time.Time, cid graphcache.ContribID, ts hlc.Timestamp) bool
	PutEdgeWithExpirationHLC(tail, head string, w float32, exp time.Time, ts hlc.Timestamp) bool
	ApplyVertexCausalBarrierHLC(key string, ts hlc.Timestamp) bool
	ApplyEdgeCausalBarrierHLC(tail, head string, ts hlc.Timestamp) bool
	ApplySnapshotVertexTombstoneHLC(key string, ts hlc.Timestamp, expiration time.Time)
	ApplySnapshotEdgeTombstoneHLC(tail, head string, ts hlc.Timestamp, expiration time.Time)
}

// SnapshotStream is the transport-neutral receive side consumed by a
// SnapshotInstaller. Connect's client stream satisfies it directly; adapters
// in other server packages can implement the same narrow surface without
// importing Connect or creating a package cycle. The caller retains ownership
// of closing the underlying transport.
type SnapshotStream interface {
	Receive() bool
	Msg() *pb.SnapshotResponse
	Err() error
}

// SnapshotGraphCounts reports the verified graph frames installed from one
// complete Snapshot stream.
type SnapshotGraphCounts struct {
	Vertices             uint64
	Edges                uint64
	VertexCausalBarriers uint64
	EdgeCausalBarriers   uint64
	VertexTombstones     uint64
	EdgeTombstones       uint64
}

// SnapshotInstallResult is returned only after a complete verified install.
// Header is an owned clone, safe to retain for same-responder resume after the
// transport advances or closes.
type SnapshotInstallResult struct {
	Header *pb.SnapshotHeader
	Graph  SnapshotGraphCounts

	searchIndexErr error
}

// SnapshotInstaller owns format-specific Snapshot validation and publication.
// CompatibleFormat is consulted for both PeerStatus and the first wire header.
// RequiredFormat is sent on Snapshot requests and controls whether full
// Subscribe requests opt in to receipt-bearing mutation envelopes.
type SnapshotInstaller interface {
	RequiredFormat() pb.SnapshotFormat
	CompatibleFormat(pb.SnapshotFormat) bool
	Install(context.Context, SnapshotStream) (SnapshotInstallResult, error)
}

type searchIndexRecovery interface {
	BeginSearchIndexRecovery()
	CompleteSearchIndexRecovery() error
}

type snapshotWatermarkApplier interface {
	ApplySnapshotWatermarks(cutoffs map[string]uint64, ts hlc.Timestamp) error
}

// A peer Snapshot can change the graph without emitting each mutation into
// this replica's log. The service closes existing CDC streams before replay
// and admits new ones only after a complete, verified install.
type snapshotInstallGuard interface {
	BeginSnapshotInstall() (finish func(verified bool), err error)
}

func beginSnapshotInstall(applier MutationApplier) (func(bool), error) {
	if guard, ok := applier.(snapshotInstallGuard); ok {
		return guard.BeginSnapshotInstall()
	}
	return nil, nil
}

type graphOnlySnapshotInstaller struct {
	apply MutationApplier
	snap  SnapshotApplier
	marks snapshotWatermarkApplier
}

func newGraphOnlySnapshotInstaller(apply MutationApplier, snap SnapshotApplier) SnapshotInstaller {
	installer := &graphOnlySnapshotInstaller{
		apply: apply, snap: snap,
	}
	if marks, ok := apply.(snapshotWatermarkApplier); ok {
		installer.marks = marks
	}
	return installer
}

func (*graphOnlySnapshotInstaller) RequiredFormat() pb.SnapshotFormat {
	return pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
}

func (*graphOnlySnapshotInstaller) CompatibleFormat(format pb.SnapshotFormat) bool {
	return graphOnlySnapshotFormat(format)
}

type snapshotFramePhase uint8

const (
	snapshotPhaseVertexBarrier snapshotFramePhase = iota + 1
	snapshotPhaseEdgeBarrier
	snapshotPhaseVertexTombstone
	snapshotPhaseEdgeTombstone
	snapshotPhaseVertex
	snapshotPhaseEdge
	snapshotPhaseFooter
)

type snapshotReplayCounts struct {
	vertices        uint64
	edges           uint64
	vertexBarrier   uint64
	edgeBarrier     uint64
	vertexTombstone uint64
	edgeTombstone   uint64
}

func (c snapshotReplayCounts) graphCounts() SnapshotGraphCounts {
	return SnapshotGraphCounts{
		Vertices:             c.vertices,
		Edges:                c.edges,
		VertexCausalBarriers: c.vertexBarrier,
		EdgeCausalBarriers:   c.edgeBarrier,
		VertexTombstones:     c.vertexTombstone,
		EdgeTombstones:       c.edgeTombstone,
	}
}

// snapshotReplayState validates the default graph-only install. It fail-closes
// on truncation, duplicate framing, and body reordering before the installer
// advances durable resume watermarks.
type snapshotReplayState struct {
	gotHeader bool
	sawFooter bool
	phase     snapshotFramePhase
	header    *pb.SnapshotHeader
	counts    snapshotReplayCounts
	want      snapshotReplayCounts
}

func snapshotProtocolError(format string, args ...any) error {
	return connect.NewError(connect.CodeInternal, fmt.Errorf("snapshot: "+format, args...))
}

func graphOnlySnapshotFormat(format pb.SnapshotFormat) bool {
	return format == pb.SnapshotFormat_SNAPSHOT_FORMAT_UNSPECIFIED ||
		format == pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
}

func snapshotInstallerCompatible(installer SnapshotInstaller, format pb.SnapshotFormat) bool {
	if installer == nil {
		return false
	}
	switch installer.RequiredFormat() {
	case pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1:
		if !graphOnlySnapshotFormat(format) {
			return false
		}
	case pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V2:
		if format != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V2 {
			return false
		}
	default:
		return false
	}
	return installer.CompatibleFormat(format)
}

func snapshotAcceptsReceiptEnvelopes(installer SnapshotInstaller) bool {
	return installer != nil &&
		installer.RequiredFormat() == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V2
}

type prefetchedSnapshotStream struct {
	stream  SnapshotStream
	first   *pb.SnapshotResponse
	current *pb.SnapshotResponse
	pending bool
}

func (s *prefetchedSnapshotStream) Receive() bool {
	if s.pending {
		s.pending = false
		s.current = s.first
		return true
	}
	if !s.stream.Receive() {
		s.current = nil
		return false
	}
	s.current = s.stream.Msg()
	return true
}

func (s *prefetchedSnapshotStream) Msg() *pb.SnapshotResponse {
	return s.current
}

func (s *prefetchedSnapshotStream) Err() error {
	return s.stream.Err()
}

// installSnapshot rejects a downgrade or incompatible first header before the
// selected installer can mutate state, then replays that header through the
// transport-neutral stream so format-specific installers still validate the
// complete framing themselves.
func installSnapshot(
	ctx context.Context,
	installer SnapshotInstaller,
	stream SnapshotStream,
) (SnapshotInstallResult, error) {
	if installer == nil {
		return SnapshotInstallResult{}, snapshotProtocolError("no installer configured")
	}
	if !stream.Receive() {
		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			return SnapshotInstallResult{}, err
		}
		return SnapshotInstallResult{}, snapshotProtocolError("stream ended before header")
	}
	first := stream.Msg()
	header := first.GetHeader()
	if header == nil {
		return SnapshotInstallResult{}, snapshotProtocolError("first frame is not a header")
	}
	if !snapshotInstallerCompatible(installer, header.GetFormat()) {
		return SnapshotInstallResult{}, connect.NewError(
			connect.CodeFailedPrecondition,
			fmt.Errorf(
				"snapshot: installer requires %s, peer sent %s",
				installer.RequiredFormat(), header.GetFormat(),
			),
		)
	}
	ownedHeader := proto.Clone(header).(*pb.SnapshotHeader)
	result, err := installer.Install(ctx, &prefetchedSnapshotStream{
		stream: stream, first: first, pending: true,
	})
	if err != nil {
		return SnapshotInstallResult{}, err
	}
	result.Header = ownedHeader
	return result, nil
}

func (s *snapshotReplayState) acceptHeader(header *pb.SnapshotHeader) error {
	if header == nil {
		return snapshotProtocolError("nil header frame")
	}
	if s.gotHeader || s.phase != 0 || s.sawFooter {
		return snapshotProtocolError("duplicate or out-of-order header frame")
	}
	// This receiver installs only graph state. Receipt format is handled by
	// the staged graph+receipt installer, never by this path.
	if !graphOnlySnapshotFormat(header.GetFormat()) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("graph-only receiver cannot install this Snapshot format"))
	}
	s.gotHeader = true
	s.header = header
	return nil
}

func (s *snapshotReplayState) acceptBody(kind string, next snapshotFramePhase) error {
	if !s.gotHeader {
		return snapshotProtocolError("%s frame before header", kind)
	}
	if s.sawFooter {
		return snapshotProtocolError("%s frame after footer", kind)
	}
	if next < s.phase {
		return snapshotProtocolError("out-of-order %s frame", kind)
	}
	s.phase = next
	return nil
}

func (s *snapshotReplayState) acceptFooter(footer *pb.SnapshotFooter) error {
	if footer == nil {
		return snapshotProtocolError("nil footer frame")
	}
	if !s.gotHeader {
		return snapshotProtocolError("footer frame before header")
	}
	if s.sawFooter {
		return snapshotProtocolError("duplicate footer frame")
	}
	s.sawFooter = true
	s.phase = snapshotPhaseFooter
	s.want = snapshotReplayCounts{
		vertices:        footer.GetVertexCount(),
		edges:           footer.GetEdgeCount(),
		vertexBarrier:   footer.GetVertexCausalBarrierCount(),
		edgeBarrier:     footer.GetEdgeCausalBarrierCount(),
		vertexTombstone: footer.GetVertexTombstoneCount(),
		edgeTombstone:   footer.GetEdgeTombstoneCount(),
	}
	return nil
}

func (s *snapshotReplayState) validateComplete() error {
	if !s.gotHeader {
		return snapshotProtocolError("stream ended before header")
	}
	if !s.sawFooter {
		return snapshotProtocolError("stream ended before footer")
	}
	if s.counts != s.want {
		return snapshotProtocolError(
			"footer count mismatch: applied vertices=%d edges=%d vertex_barriers=%d edge_barriers=%d vertex_tombstones=%d edge_tombstones=%d; footer vertices=%d edges=%d vertex_barriers=%d edge_barriers=%d vertex_tombstones=%d edge_tombstones=%d",
			s.counts.vertices, s.counts.edges, s.counts.vertexBarrier, s.counts.edgeBarrier, s.counts.vertexTombstone, s.counts.edgeTombstone,
			s.want.vertices, s.want.edges, s.want.vertexBarrier, s.want.edgeBarrier, s.want.vertexTombstone, s.want.edgeTombstone,
		)
	}
	return nil
}

func snapshotFloorHLC(stamp *pb.HLCTimestamp) (hlc.Timestamp, error) {
	if stamp == nil || stamp.GetWallNs() <= 0 || len(stamp.GetNodeId()) != len(hlc.NodeID{}) {
		return hlc.Timestamp{}, snapshotProtocolError("invalid causal floor HLC")
	}
	ts := snapshotHLC(stamp)
	if ts.NodeID == (hlc.NodeID{}) {
		return hlc.Timestamp{}, snapshotProtocolError("zero causal floor NodeID")
	}
	return ts, nil
}

func snapshotTombstoneFields(stamp *pb.HLCTimestamp, expiration *timestamppb.Timestamp) (hlc.Timestamp, time.Time, error) {
	ts, err := snapshotFloorHLC(stamp)
	if err != nil {
		return hlc.Timestamp{}, time.Time{}, err
	}
	if expiration == nil || expiration.CheckValid() != nil {
		return hlc.Timestamp{}, time.Time{}, snapshotProtocolError("invalid Delete tombstone expiration")
	}
	return ts, expiration.AsTime(), nil
}

// applySnapshotEdge re-applies one snapshot edge contribution into the local
// cache via snap. A Put-origin (LWW-Register) contribution carries a zero
// ContribID: re-applying it through AddEdgeWithExpirationContribHLC hits the
// dedup-disabled branch of addWithExpirationContrib (a zero cid disables dedup)
// and G-Set-APPENDs a fresh weight value on every snapshot pass, so a node that
// keeps re-snapshotting — e.g. anti-entropy re-syncs under sustained write load
// — accumulates unbounded duplicate contributions and leaks heap (#735). Routing
// zero-cid contributions through the LWW PutEdgeWithExpirationHLC makes the
// re-apply an idempotent overwrite; non-zero (AddEdge-origin) contributions keep
// their ContribID and the dedup-aware AddEdge path.
func applySnapshotEdge(snap SnapshotApplier, tail, head string, weight float32, exp time.Time, cid graphcache.ContribID, ts hlc.Timestamp) {
	if cid.IsZero() {
		snap.PutEdgeWithExpirationHLC(tail, head, weight, exp, ts)
		return
	}
	snap.AddEdgeWithExpirationContribHLC(tail, head, weight, exp, cid, ts)
}

type snapshotEdgeRow struct {
	weight     float32
	expiration time.Time
	contribID  graphcache.ContribID
	hlc        hlc.Timestamp
}

// snapshotEdgeRows validates a complete edge frame before applying any row.
// Each Add must retain its own causal HLC: the edge-level HLC is only the
// winning Put floor and cannot replace the Add's position across a reset.
func snapshotEdgeRows(edge *pb.SnapshotEdge) ([]snapshotEdgeRow, error) {
	if edge == nil || edge.GetTail() == "" || edge.GetHead() == "" || len(edge.GetContributions()) == 0 {
		return nil, snapshotProtocolError("nil or empty live edge payload")
	}
	var putHLC hlc.Timestamp
	if edge.GetHlc() != nil {
		var err error
		putHLC, err = snapshotFloorHLC(edge.GetHlc())
		if err != nil {
			return nil, snapshotProtocolError("invalid live edge Put floor")
		}
	}
	rows := make([]snapshotEdgeRow, 0, len(edge.GetContributions()))
	seenPut := false
	seenAdds := make(map[graphcache.ContribID]struct{}, len(edge.GetContributions()))
	for _, contribution := range edge.GetContributions() {
		if contribution == nil {
			return nil, snapshotProtocolError("nil live edge contribution")
		}
		if exp := contribution.GetExpiration(); exp != nil && exp.CheckValid() != nil {
			return nil, snapshotProtocolError("invalid live edge contribution expiration")
		}
		var id graphcache.ContribID
		rawID := contribution.GetContribId()
		if len(rawID) != 0 && len(rawID) != len(id) {
			return nil, snapshotProtocolError("invalid live edge ContribID length")
		}
		copy(id[:], rawID)
		row := snapshotEdgeRow{
			weight: contribution.GetWeight(), expiration: prototime.Expiration(contribution.GetExpiration()),
			contribID: id,
		}
		if id.IsZero() {
			if seenPut || len(rawID) != 0 {
				return nil, snapshotProtocolError("duplicate or zero live edge Put identity")
			}
			if stamp := contribution.GetHlc(); stamp != nil && snapshotHLC(stamp) != putHLC {
				return nil, snapshotProtocolError("live edge Put HLC differs from floor")
			}
			seenPut = true
			row.hlc = putHLC
		} else {
			if _, duplicate := seenAdds[id]; duplicate {
				return nil, snapshotProtocolError("duplicate live edge ContribID")
			}
			seenAdds[id] = struct{}{}
			addHLC, err := snapshotFloorHLC(contribution.GetHlc())
			if err != nil {
				return nil, snapshotProtocolError("invalid live edge Add HLC")
			}
			if putHLC != (hlc.Timestamp{}) && !putHLC.Less(addHLC) {
				return nil, snapshotProtocolError("live edge Add is not newer than the Put floor")
			}
			row.hlc = addHLC
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (i *graphOnlySnapshotInstaller) Install(_ context.Context, stream SnapshotStream) (SnapshotInstallResult, error) {
	var finishInstall func(bool)
	defer func() {
		if finishInstall != nil {
			finishInstall(false)
		}
	}()
	var recovery searchIndexRecovery
	var searchIndexErr error
	var replay snapshotReplayState
	for stream.Receive() {
		resp := stream.Msg()
		switch e := resp.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			if err := replay.acceptHeader(e.Header); err != nil {
				return SnapshotInstallResult{}, err
			}
			var err error
			finishInstall, err = beginSnapshotInstall(i.apply)
			if err != nil {
				return SnapshotInstallResult{}, err
			}
			// A mismatched or malformed first header must not mark an intact
			// search index incomplete. Begin recovery only after format and
			// install admission have both succeeded.
			if candidate, ok := i.snap.(searchIndexRecovery); ok {
				recovery = candidate
				recovery.BeginSearchIndexRecovery()
			}
		case *pb.SnapshotResponse_VertexCausalBarrier:
			if err := replay.acceptBody("vertex causal barrier", snapshotPhaseVertexBarrier); err != nil {
				return SnapshotInstallResult{}, err
			}
			barrier := e.VertexCausalBarrier
			if barrier == nil {
				return SnapshotInstallResult{}, snapshotProtocolError("nil vertex causal barrier")
			}
			ts, err := snapshotFloorHLC(barrier.GetHlc())
			if err != nil || barrier.GetKey() == "" {
				return SnapshotInstallResult{}, snapshotProtocolError("invalid vertex causal barrier")
			}
			i.snap.ApplyVertexCausalBarrierHLC(barrier.GetKey(), ts)
			replay.counts.vertexBarrier++
		case *pb.SnapshotResponse_EdgeCausalBarrier:
			if err := replay.acceptBody("edge causal barrier", snapshotPhaseEdgeBarrier); err != nil {
				return SnapshotInstallResult{}, err
			}
			barrier := e.EdgeCausalBarrier
			if barrier == nil {
				return SnapshotInstallResult{}, snapshotProtocolError("nil edge causal barrier")
			}
			ts, err := snapshotFloorHLC(barrier.GetHlc())
			if err != nil || barrier.GetTail() == "" || barrier.GetHead() == "" {
				return SnapshotInstallResult{}, snapshotProtocolError("invalid edge causal barrier")
			}
			i.snap.ApplyEdgeCausalBarrierHLC(barrier.GetTail(), barrier.GetHead(), ts)
			replay.counts.edgeBarrier++
		case *pb.SnapshotResponse_VertexTombstone:
			if err := replay.acceptBody("vertex tombstone", snapshotPhaseVertexTombstone); err != nil {
				return SnapshotInstallResult{}, err
			}
			marker := e.VertexTombstone
			if marker == nil || marker.GetKey() == "" {
				return SnapshotInstallResult{}, snapshotProtocolError("nil or empty vertex tombstone")
			}
			ts, exp, err := snapshotTombstoneFields(marker.GetHlc(), marker.GetExpiration())
			if err != nil {
				return SnapshotInstallResult{}, err
			}
			i.snap.ApplySnapshotVertexTombstoneHLC(marker.GetKey(), ts, exp)
			replay.counts.vertexTombstone++
		case *pb.SnapshotResponse_EdgeTombstone:
			if err := replay.acceptBody("edge tombstone", snapshotPhaseEdgeTombstone); err != nil {
				return SnapshotInstallResult{}, err
			}
			marker := e.EdgeTombstone
			if marker == nil || marker.GetTail() == "" || marker.GetHead() == "" {
				return SnapshotInstallResult{}, snapshotProtocolError("nil or empty edge tombstone")
			}
			ts, exp, err := snapshotTombstoneFields(marker.GetHlc(), marker.GetExpiration())
			if err != nil {
				return SnapshotInstallResult{}, err
			}
			i.snap.ApplySnapshotEdgeTombstoneHLC(marker.GetTail(), marker.GetHead(), ts, exp)
			replay.counts.edgeTombstone++
		case *pb.SnapshotResponse_Vertex:
			if err := replay.acceptBody("vertex", snapshotPhaseVertex); err != nil {
				return SnapshotInstallResult{}, err
			}
			sv := e.Vertex
			if sv == nil || sv.GetVertex() == nil {
				return SnapshotInstallResult{}, snapshotProtocolError("nil vertex payload")
			}
			v := sv.GetVertex()
			i.snap.PutVertexWithExpirationHLC(
				v.GetKey(), v, prototime.Expiration(v.GetExpiration()),
				snapshotHLC(sv.GetHlc()),
			)
			replay.counts.vertices++
		case *pb.SnapshotResponse_Edge:
			if err := replay.acceptBody("edge", snapshotPhaseEdge); err != nil {
				return SnapshotInstallResult{}, err
			}
			se := e.Edge
			rows, err := snapshotEdgeRows(se)
			if err != nil {
				return SnapshotInstallResult{}, err
			}
			for _, row := range rows {
				applySnapshotEdge(i.snap, se.GetTail(), se.GetHead(), row.weight,
					row.expiration, row.contribID, row.hlc)
			}
			replay.counts.edges++
		case *pb.SnapshotResponse_Footer:
			if err := replay.acceptFooter(e.Footer); err != nil {
				return SnapshotInstallResult{}, err
			}
		default:
			return SnapshotInstallResult{}, snapshotProtocolError("unknown or empty response frame %T", e)
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return SnapshotInstallResult{}, err
	}
	if err := replay.validateComplete(); err != nil {
		return SnapshotInstallResult{}, err
	}
	if recovery != nil {
		if err := recovery.CompleteSearchIndexRecovery(); err != nil {
			searchIndexErr = err
		}
	}
	if i.marks != nil {
		if err := i.marks.ApplySnapshotWatermarks(
			replay.header.GetCutoffSeqPerOrigin(),
			snapshotHLC(replay.header.GetCutoffHlc()),
		); err != nil {
			return SnapshotInstallResult{}, err
		}
	}
	if finishInstall != nil {
		finishInstall(true)
		finishInstall = nil
	}
	return SnapshotInstallResult{
		Header:         proto.Clone(replay.header).(*pb.SnapshotHeader),
		Graph:          replay.counts.graphCounts(),
		searchIndexErr: searchIndexErr,
	}, nil
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
	OnPumpSnapshotReplayed(peer string, vertices, edges uint64, duration time.Duration)
	OnSearchConfig(peer string, matched bool)
}

type nopMetrics struct{}

func (nopMetrics) OnPumpConnect(string)                                         {}
func (nopMetrics) OnPumpDisconnect(string, string)                              {}
func (nopMetrics) OnPumpApply(string)                                           {}
func (nopMetrics) OnPumpDropSelfEcho(string)                                    {}
func (nopMetrics) OnPumpSnapshotReplayed(string, uint64, uint64, time.Duration) {}
func (nopMetrics) OnSearchConfig(string, bool)                                  {}

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
	// Each entry must be a bare "host:port" (e.g. "lantern-0:6380").
	// The pump prepends "http://" to build the Connect baseURL.
	// Empty (or nil) is valid when Source is set; otherwise yields
	// a no-op pump.
	Peers []string

	// Source, when non-nil, takes precedence over Peers and is the
	// dynamic peer-set resolver consulted at startup and (when
	// DiscoveryInterval > 0) on every tick. The pump reconciles
	// added / removed addresses by spawning / cancelling per-peer
	// goroutines. nil means "use StaticSource{Peers}", which
	// preserves the historical LANTERN_PEERS contract.
	Source PeerSource

	// DiscoveryInterval is the polling cadence for Source. Zero
	// means "resolve once at startup and never re-poll" — the
	// static behaviour. A positive value enables dynamic peer
	// discovery (e.g. periodic DNS lookups against a k8s headless
	// Service per #190). Resolution errors are logged and the
	// previously-active peer set is retained so a transient DNS
	// failure does not tear down established subscriptions.
	DiscoveryInterval time.Duration

	// AuthToken, when non-empty, is attached as "Authorization: Bearer"
	// to every outbound Subscribe/Snapshot call so the pump can replicate
	// against peers running with LANTERN_AUTH_TOKENS (#850).
	AuthToken string

	// SearchConfigFingerprint is the local search capability fingerprint.
	// When non-empty, every peer session verifies PeerStatus before opening
	// Subscribe. A mismatch does not block graph replication, but readiness
	// and metrics remain degraded until every observed peer matches.
	SearchConfigFingerprint string

	// HTTPClient is the http.Client used to open Connect-Go streams
	// against each peer. When nil, defaultH2CClient() is used so the
	// pump talks plain HTTP/2 over the cluster network — sufficient
	// for the HA topology where peers are only reachable via the
	// cluster network. For TLS, supply an http.Client backed by an
	// HTTP/2-enabled http.Transport with a real *tls.Config.
	HTTPClient *http.Client

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

	// SnapshotInstaller overrides the graph-only in-place installer. nil keeps
	// the existing GRAPH_ONLY_V1 behavior using apply and snap passed to
	// NewPump. A durable receipt installer requires RECEIPT_V2 without
	// changing the Pump transport or retry driver.
	SnapshotInstaller SnapshotInstaller
}

// Pump is the long-running peer-replication driver. Construct with
// NewPump and start with Run(ctx). Run blocks until ctx is cancelled
// (or returns immediately when Peers is empty), so it is meant to be
// invoked from inside an errgroup alongside the Connect listener.
type Pump struct {
	cfg       Config
	apply     MutationApplier
	installer SnapshotInstaller
	tracker   *peerTracker
}

// NewPump constructs the pump. apply MUST be the local LanternService instance
// so replayed mutations are not re-broadcast. When Config.SnapshotInstaller is
// nil, snap MUST be the same underlying graph cache the read RPCs serve so the
// default graph-only install converges with subsequent Subscribe deltas.
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
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = defaultH2CClient()
	}
	cfg.HTTPClient = withAuthToken(cfg.HTTPClient, cfg.AuthToken)
	installer := cfg.SnapshotInstaller
	if installer == nil {
		installer = newGraphOnlySnapshotInstaller(apply, snap)
	}
	return &Pump{
		cfg: cfg, apply: apply, installer: installer, tracker: newPeerTracker(),
	}
}

// Run starts one goroutine per peer and blocks until ctx is
// cancelled. Returns nil after all goroutines have exited. A pump
// with no configured peers AND no dynamic Source is a no-op and
// returns immediately.
//
// When Config.Source is set (or Config.Peers is non-empty — which
// is wrapped in a StaticSource), Run:
//
//  1. Resolves the initial peer set and spawns one goroutine per
//     address.
//  2. If Config.DiscoveryInterval > 0, polls Source on every tick
//     and reconciles add/remove against the active goroutine set.
//     A resolution error logs and preserves the previous set
//     (defensive: a transient DNS failure must not silently drop
//     established peer streams).
//  3. On ctx cancellation, cancels every per-peer goroutine and
//     waits for them to exit before returning.
func (p *Pump) Run(ctx context.Context) error {
	source := p.cfg.Source
	if source == nil {
		if len(p.cfg.Peers) == 0 {
			p.cfg.Logger.Info("replication pump: no peers configured, running in single-instance mode")
			return nil
		}
		source = StaticSource{Peers: p.cfg.Peers}
	}

	sup := newPeerSupervisor(p.runPeer)
	defer sup.shutdown()

	initial, err := source.Resolve(ctx)
	if err != nil {
		p.cfg.Logger.Warn("replication pump: initial peer resolution failed",
			slog.Any("err", err))
	}
	if len(initial) == 0 && p.cfg.DiscoveryInterval == 0 {
		p.cfg.Logger.Info("replication pump: no peers resolved and no discovery interval, running in single-instance mode")
		return nil
	}
	sup.reconcile(ctx, initial)
	p.cfg.Logger.Info("replication pump: starting",
		slog.Int("peers", len(initial)),
		slog.Duration("discovery_interval", p.cfg.DiscoveryInterval))

	if p.cfg.DiscoveryInterval <= 0 {
		<-ctx.Done()
		p.cfg.Logger.Info("replication pump: stopped")
		return nil
	}

	ticker := time.NewTicker(p.cfg.DiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.cfg.Logger.Info("replication pump: stopped")
			return nil
		case <-ticker.C:
			peers, err := source.Resolve(ctx)
			if err != nil {
				p.cfg.Logger.Warn("replication pump: peer resolution failed (keeping previous set)",
					slog.Any("err", err))
				continue
			}
			sup.reconcile(ctx, peers)
		}
	}
}

// runPeer is the per-peer reconnect loop. Each iteration represents
// one Subscribe (and optional Snapshot) session. On session error,
// the loop sleeps for the current backoff (doubling, capped at
// BackoffMax) and retries until ctx is cancelled.
//
// Under the leaderless Subscribe contract (#415, B-2/B-3/B-4), ordinary
// reconnects send an empty per-origin cursor and the local ApplyMutation
// watermark CAS dedups anything already seen via this peer or any other.
// A gapped session is different: after replaying a snapshot, the pump resumes
// from the snapshot header's cutoff so it does not request the same unavailable
// log prefix again.
func (p *Pump) runPeer(ctx context.Context, addr string) {
	log := p.cfg.Logger.With(slog.String("peer", addr))
	defer p.tracker.removePeer(addr)
	backoff := p.cfg.BackoffMin

	for ctx.Err() == nil {
		p.tracker.setState(addr, PeerStateConnecting)
		err := p.session(ctx, addr)
		if err == nil {
			// session ended cleanly (ctx cancelled mid-stream).
			return
		}
		if ctx.Err() != nil {
			return
		}
		p.tracker.recordError(addr, err)
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
// Snapshot+Subscribe bootstrap when the server reports the request as
// gapped. Returns nil on clean ctx-cancel exit; otherwise the
// non-nil error from the Subscribe / Snapshot RPC.
func (p *Pump) session(ctx context.Context, addr string) error {
	log := p.cfg.Logger.With(slog.String("peer", addr))
	// Connect-Go clients are cheap to construct and own no
	// connection state of their own — the underlying http.Client
	// pools connections internally. No defer-close needed.
	//
	// The default Connect-Go wire protocol is used. Both ends of the
	// replication channel are Connect handlers in the same process
	// family, so the gRPC binary framing pin from earlier cutover
	// builds (§A of #393) is no longer needed.
	cli := graphv1connect.NewLanternReplicationServiceClient(
		p.cfg.HTTPClient, peerBaseURL(addr),
	)
	status, err := cli.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
	if err != nil {
		return fmt.Errorf("peer capability status: %w", err)
	}
	if !snapshotInstallerCompatible(p.installer, status.Msg.GetRequiredSnapshotFormat()) {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"peer Snapshot format %s is incompatible with installer requiring %s",
			status.Msg.GetRequiredSnapshotFormat(), p.installer.RequiredFormat(),
		))
	}
	if p.cfg.SearchConfigFingerprint != "" {
		remote := status.Msg.GetSearchConfigFingerprint()
		matched := remote != "" && remote == p.cfg.SearchConfigFingerprint
		p.cfg.Metrics.OnSearchConfig(addr, matched)
		if !matched {
			log.Error("replication pump: search config mismatch",
				slog.String("local_fingerprint", p.cfg.SearchConfigFingerprint),
				slog.String("peer_fingerprint", remote))
		}
	}

	p.cfg.Metrics.OnPumpConnect(addr)
	log.Info("replication pump: peer transition",
		slog.String("transition", "connect"))

	err = p.subscribe(ctx, cli, addr, nil, 0)
	if err == nil {
		p.cfg.Metrics.OnPumpDisconnect(addr, "clean")
		log.Info("replication pump: peer transition",
			slog.String("transition", "disconnect"),
			slog.String("reason", "clean"))
		return nil
	}
	if ctx.Err() != nil {
		p.cfg.Metrics.OnPumpDisconnect(addr, "ctx_cancel")
		log.Info("replication pump: peer transition",
			slog.String("transition", "disconnect"),
			slog.String("reason", "ctx_cancel"))
		return nil
	}
	if connect.CodeOf(err) == connect.CodeFailedPrecondition {
		// Gapped: snapshot, then resume after the snapshot cutoffs. Sending
		// an empty cursor here would request the unavailable log prefix again
		// and loop through snapshots indefinitely.
		log.Info("replication pump: peer transition",
			slog.String("transition", "snapshot_start"),
			slog.String("reason", "gapped"))
		header, sErr := p.snapshot(ctx, cli, addr)
		if sErr != nil {
			p.cfg.Metrics.OnPumpDisconnect(addr, "snapshot_failed")
			log.Warn("replication pump: peer transition",
				slog.String("transition", "snapshot_finish"),
				slog.String("reason", "snapshot_failed"),
				slog.Any("err", sErr))
			return sErr
		}
		log.Info("replication pump: peer transition",
			slog.String("transition", "snapshot_finish"),
			slog.String("reason", "applied"))
		resume := resumeAfterSnapshot(header)
		err = p.subscribe(ctx, cli, addr, resume.origins, resume.local)
		if err == nil {
			p.cfg.Metrics.OnPumpDisconnect(addr, "clean")
			log.Info("replication pump: peer transition",
				slog.String("transition", "disconnect"),
				slog.String("reason", "clean"))
			return nil
		}
	}
	p.cfg.Metrics.OnPumpDisconnect(addr, "subscribe_failed")
	log.Warn("replication pump: peer transition",
		slog.String("transition", "disconnect"),
		slog.String("reason", "subscribe_failed"),
		slog.Any("err", err))
	return err
}

// subscribe opens a Subscribe stream and applies every received
// Mutation. Self-origin mutations are dropped before dispatch.
// Returns any error from Receive / Apply; a clean stream end
// returns nil.
//
// Under the leaderless Subscribe contract (#415), the peer's local log carries
// mutations from every cluster origin, not just the peer's own writes. Ordinary
// reconnects pass an empty cursor (= deliver every retained entry) and rely on
// LanternService.ApplyMutation's per-origin watermark CAS for deduplication.
// Snapshot recovery instead passes the header-derived cursor supplied by the
// caller so the live tail starts after the point-in-time cut.
func (p *Pump) subscribe(ctx context.Context, cli graphv1connect.LanternReplicationServiceClient, addr string, cursor map[string]uint64, fromLocalSeq uint64) error {
	stream, err := cli.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		FromSeqPerOrigin:       cursor,
		FromLocalSeq:           fromLocalSeq,
		AcceptReceiptEnvelopes: snapshotAcceptsReceiptEnvelopes(p.installer),
	}))
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	for stream.Receive() {
		resp := stream.Msg()
		mu := resp.GetMutation()
		if mu == nil {
			continue
		}
		if p.isSelfEcho(mu) {
			p.cfg.Metrics.OnPumpDropSelfEcho(addr)
			continue
		}
		if err := p.apply.ApplyMutation(ctx, mu); err != nil {
			return err
		}
		p.cfg.Metrics.OnPumpApply(addr)
		p.tracker.recordEvent(addr, mu.GetSeq(), time.Now())
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// snapshot opens a Snapshot stream and delegates its complete receive/install
// lifecycle to the selected format strategy.
//
// The returned header supplies the exact per-origin resume cursor and local
// watermark cut for the live tail. Replaying those cutoffs before Subscribe
// prevents both duplicate application and an infinite gapped-snapshot loop.
func (p *Pump) snapshot(ctx context.Context, cli graphv1connect.LanternReplicationServiceClient, addr string) (*pb.SnapshotHeader, error) {
	stream, err := cli.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: p.installer.RequiredFormat(),
	}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	start := time.Now()
	result, err := installSnapshot(ctx, p.installer, stream)
	if err != nil {
		return nil, err
	}
	if result.searchIndexErr != nil {
		p.cfg.Logger.Warn("replication pump: snapshot rebuilt graph but search index remains incomplete",
			slog.String("peer", addr), slog.Any("err", result.searchIndexErr))
	}
	p.cfg.Metrics.OnPumpSnapshotReplayed(
		addr, result.Graph.Vertices, result.Graph.Edges, time.Since(start),
	)
	return result.Header, nil
}

type snapshotResumeCursor struct {
	origins map[string]uint64
	local   uint64
}

func resumeAfterSnapshot(header *pb.SnapshotHeader) snapshotResumeCursor {
	if header == nil {
		return snapshotResumeCursor{}
	}
	resume := snapshotResumeCursor{
		origins: make(map[string]uint64, len(header.GetCutoffSeqPerOrigin())),
	}
	for origin, cutoff := range header.GetCutoffSeqPerOrigin() {
		if cutoff == ^uint64(0) {
			resume.origins[origin] = cutoff
			continue
		}
		resume.origins[origin] = cutoff + 1
	}
	if cutoff := header.GetCutoffLocalSeq(); cutoff == ^uint64(0) {
		resume.local = cutoff
	} else {
		resume.local = cutoff + 1
	}
	return resume
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
