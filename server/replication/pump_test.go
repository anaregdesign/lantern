package replication

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type receiptIncompatiblePeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	subscribes     atomic.Int32
	snapshots      atomic.Int32
	requiredFormat pb.SnapshotFormat
}

func (p *receiptIncompatiblePeer) PeerStatus(context.Context, *connect.Request[pb.PeerStatusRequest]) (*connect.Response[pb.PeerStatusResponse], error) {
	return connect.NewResponse(&pb.PeerStatusResponse{
		RequiredSnapshotFormat: p.requiredFormat,
	}), nil
}

func (p *receiptIncompatiblePeer) Subscribe(_ context.Context, req *connect.Request[pb.SubscribeRequest], _ *connect.ServerStream[pb.SubscribeResponse]) error {
	p.subscribes.Add(1)
	if req.Msg.GetAcceptReceiptEnvelopes() {
		return connect.NewError(connect.CodeInternal, errors.New("legacy Pump unexpectedly opted into receipts"))
	}
	return connect.NewError(connect.CodeInvalidArgument, errors.New("receipt envelope requires opt-in"))
}

func (p *receiptIncompatiblePeer) Snapshot(context.Context, *connect.Request[pb.SnapshotRequest], *connect.ServerStream[pb.SnapshotResponse]) error {
	p.snapshots.Add(1)
	return connect.NewError(connect.CodeInternal, errors.New("receipt mismatch must not trigger graph-only Snapshot"))
}

func TestPumpReceiptIncompatibilityDoesNotSnapshot(t *testing.T) {
	peer := &receiptIncompatiblePeer{}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
	srv := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = protocols
	srv.Start()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pump := NewPump(Config{HTTPClient: defaultH2CClient()}, nil, nil)
	if err := pump.session(ctx, srv.URL); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("incompatible receipt stream = %v, want InvalidArgument", err)
	}
	if got := peer.subscribes.Load(); got != 1 {
		t.Fatalf("Subscribe calls = %d, want 1", got)
	}
	if got := peer.snapshots.Load(); got != 0 {
		t.Fatalf("graph-only Snapshot calls = %d, want 0", got)
	}
}

func TestPumpRejectsReceiptSnapshotRequirementBeforeSubscribe(t *testing.T) {
	peer := &receiptIncompatiblePeer{requiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
	srv := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = protocols
	srv.Start()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pump := NewPump(Config{HTTPClient: defaultH2CClient()}, nil, nil)
	if err := pump.session(ctx, srv.URL); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("receipt Snapshot requirement = %v, want FailedPrecondition", err)
	}
	if got := peer.subscribes.Load(); got != 0 {
		t.Fatalf("Subscribe calls = %d, want 0", got)
	}
	if got := peer.snapshots.Load(); got != 0 {
		t.Fatalf("graph-only Snapshot calls = %d, want 0", got)
	}
}

func TestPumpUsesInjectedSnapshotInstaller(t *testing.T) {
	header := &pb.SnapshotHeader{
		Format:             pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
		CutoffSeqPerOrigin: map[string]uint64{"origin-a": 7},
		CutoffLocalSeq:     20,
	}
	peer := &installerTestPeer{
		requiredFormat:    pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
		header:            header,
		gapFirstSubscribe: true,
	}
	server := startInstallerTestPeer(t, peer)
	installer := &scriptedSnapshotInstaller{
		required: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
	}
	pump := NewPump(Config{
		HTTPClient:        defaultH2CClient(),
		SnapshotInstaller: installer,
	}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := pump.session(ctx, server.URL); err != nil {
		t.Fatalf("session with injected installer: %v", err)
	}
	if got := installer.installCount(); got != 1 {
		t.Fatalf("installer calls = %d, want 1", got)
	}
	subscribes, snapshots := peer.requests()
	if len(snapshots) != 1 ||
		snapshots[0].GetRequiredFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
		t.Fatalf("Snapshot requests = %+v, want one RECEIPT request", snapshots)
	}
	if len(subscribes) != 2 {
		t.Fatalf("Subscribe requests = %d, want 2", len(subscribes))
	}
	for index, request := range subscribes {
		if !request.GetAcceptReceiptEnvelopes() {
			t.Fatalf("Subscribe[%d] did not opt in to receipt envelopes", index)
		}
	}
	if got := subscribes[1].GetFromSeqPerOrigin()["origin-a"]; got != 8 {
		t.Fatalf("resumed origin cursor = %d, want 8", got)
	}
	if got := subscribes[1].GetFromLocalSeq(); got != 21 {
		t.Fatalf("resumed local cursor = %d, want 21", got)
	}
}

func TestPumpInjectedSnapshotInstallerRejectsIncompatibleFormats(t *testing.T) {
	for _, tc := range []struct {
		name           string
		statusFormat   pb.SnapshotFormat
		headerFormat   pb.SnapshotFormat
		wantSubscribes int
		wantSnapshots  int
	}{
		{
			name:         "legacy PeerStatus is not receipt compatible",
			statusFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_UNSPECIFIED,
		},
		{
			name:         "graph PeerStatus is not receipt compatible",
			statusFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		},
		{
			name:         "removed numeric PeerStatus is not receipt compatible",
			statusFormat: pb.SnapshotFormat(2),
		},
		{
			name:           "legacy header is rejected before installer",
			statusFormat:   pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
			headerFormat:   pb.SnapshotFormat_SNAPSHOT_FORMAT_UNSPECIFIED,
			wantSubscribes: 1,
			wantSnapshots:  1,
		},
		{
			name:           "graph header is rejected before installer",
			statusFormat:   pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
			headerFormat:   pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
			wantSubscribes: 1,
			wantSnapshots:  1,
		},
		{
			name:           "removed numeric header is rejected before installer",
			statusFormat:   pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
			headerFormat:   pb.SnapshotFormat(2),
			wantSubscribes: 1,
			wantSnapshots:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer := &scriptedSnapshotInstaller{
				required: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
				// Even a permissive implementation cannot weaken the driver's
				// exact RECEIPT downgrade boundary.
				compatible: func(pb.SnapshotFormat) bool { return true },
			}
			peer := &installerTestPeer{
				requiredFormat:    tc.statusFormat,
				header:            &pb.SnapshotHeader{Format: tc.headerFormat},
				gapFirstSubscribe: true,
			}
			server := startInstallerTestPeer(t, peer)
			pump := NewPump(Config{
				HTTPClient:        defaultH2CClient(),
				SnapshotInstaller: installer,
			}, nil, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			err := pump.session(ctx, server.URL)
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("incompatible format error = %v, want FailedPrecondition", err)
			}
			if got := installer.installCount(); got != 0 {
				t.Fatalf("installer calls = %d, want 0 before mutation", got)
			}
			subscribes, snapshots := peer.requests()
			if len(subscribes) != tc.wantSubscribes {
				t.Fatalf("Subscribe calls = %d, want %d", len(subscribes), tc.wantSubscribes)
			}
			if len(snapshots) != tc.wantSnapshots {
				t.Fatalf("Snapshot calls = %d, want %d", len(snapshots), tc.wantSnapshots)
			}
		})
	}
}

func TestPumpLogsNonfatalSearchIndexCompletionFailure(t *testing.T) {
	peer := &installerTestPeer{
		requiredFormat:    pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		header:            &pb.SnapshotHeader{Format: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1},
		gapFirstSubscribe: true,
	}
	server := startInstallerTestPeer(t, peer)
	installer := &scriptedSnapshotInstaller{
		required:       pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		searchIndexErr: errors.New("index rebuild failed"),
	}
	var logs bytes.Buffer
	pump := NewPump(Config{
		HTTPClient:        defaultH2CClient(),
		SnapshotInstaller: installer,
		Logger:            slog.New(slog.NewTextHandler(&logs, nil)),
	}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := pump.session(ctx, server.URL); err != nil {
		t.Fatalf("session with incomplete search index: %v", err)
	}
	if !strings.Contains(logs.String(), "snapshot rebuilt graph but search index remains incomplete") {
		t.Fatalf("nonfatal search-index failure was not logged: %s", logs.String())
	}
}

type incompatibleSnapshotHeaderPeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	format pb.SnapshotFormat
}

func (p *incompatibleSnapshotHeaderPeer) Snapshot(_ context.Context, _ *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	return stream.Send(&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Header{
		Header: &pb.SnapshotHeader{Format: p.format},
	}})
}

type recoveryRecordingApplier struct {
	recordingApplier
	begins int
}

func (r *recoveryRecordingApplier) BeginSearchIndexRecovery()          { r.begins++ }
func (r *recoveryRecordingApplier) CompleteSearchIndexRecovery() error { return nil }

func TestSnapshotFormatMismatchKeepsSearchIndexReady(t *testing.T) {
	for _, format := range []pb.SnapshotFormat{
		pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
		pb.SnapshotFormat(2),
		pb.SnapshotFormat(99),
	} {
		t.Run(format.String(), func(t *testing.T) {
			peer := &incompatibleSnapshotHeaderPeer{format: format}
			mux := http.NewServeMux()
			mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
			srv := httptest.NewUnstartedServer(mux)
			protocols := new(http.Protocols)
			protocols.SetUnencryptedHTTP2(true)
			srv.Config.Protocols = protocols
			srv.Start()
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			snap := &recoveryRecordingApplier{}
			pump := NewPump(Config{HTTPClient: defaultH2CClient()}, nil, snap)
			if _, err := pump.snapshot(ctx, srv.URL); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("pump Snapshot mismatch = %v", err)
			}
			if snap.begins != 0 {
				t.Fatalf("pump marked search index incomplete %d times", snap.begins)
			}
			anti := NewAntiEntropy(AntiEntropyConfig{HTTPClient: defaultH2CClient()}, nil, nil, snap)
			if err := anti.snapshotFrom(ctx, srv.URL); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("anti-entropy Snapshot mismatch = %v", err)
			}
			if snap.begins != 0 {
				t.Fatalf("anti-entropy marked search index incomplete %d times", snap.begins)
			}
		})
	}
}

type rawSnapshotEnvelope struct {
	payload    []byte
	compressed bool
}

func startRawSnapshotH2CServer(
	t *testing.T,
	envelopes []rawSnapshotEnvelope,
) *httptest.Server {
	t.Helper()
	var body bytes.Buffer
	compressed := false
	for _, envelope := range envelopes {
		payload := envelope.payload
		flag := byte(0)
		if envelope.compressed {
			compressed = true
			var zipped bytes.Buffer
			writer := gzip.NewWriter(&zipped)
			if _, err := writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			payload = zipped.Bytes()
			flag = 1
		}
		var prefix [5]byte
		prefix[0] = flag
		binary.BigEndian.PutUint32(prefix[1:], uint32(len(payload)))
		body.Write(prefix[:])
		body.Write(payload)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != graphv1connect.LanternReplicationServiceSnapshotProcedure {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/connect+proto")
		if compressed {
			w.Header().Set("Connect-Content-Encoding", "gzip")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body.Bytes())
	})
	server := httptest.NewUnstartedServer(handler)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func marshalSnapshotTransportFrame(t *testing.T, frame *pb.SnapshotResponse) []byte {
	t.Helper()
	payload, err := proto.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSnapshotTransportLimitsAreExplicitAndGraphOnlyPreserving(t *testing.T) {
	if limits, bounded, err := snapshotTransportLimitsFor(
		newGraphOnlySnapshotInstaller(nil, nil),
	); err != nil || bounded || limits != (SnapshotTransportLimits{}) {
		t.Fatalf("graph-only transport limits = (%+v, %t, %v), want unchanged", limits, bounded, err)
	}

	valid := &scriptedSnapshotInstaller{
		required: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
		transport: SnapshotTransportLimits{
			MaxFrameBytes:  8 << 20,
			MaxStreamBytes: 512 << 20,
		},
	}
	if limits, bounded, err := snapshotTransportLimitsFor(valid); err != nil ||
		!bounded || limits != valid.transport {
		t.Fatalf("receipt transport limits = (%+v, %t, %v), want %+v", limits, bounded, err, valid.transport)
	}

	for _, limits := range []SnapshotTransportLimits{
		{MaxFrameBytes: -1, MaxStreamBytes: 1},
		{MaxFrameBytes: 1, MaxStreamBytes: 0},
		{MaxFrameBytes: defaultSnapshotMaxFrameBytes + 1, MaxStreamBytes: 1},
		{MaxFrameBytes: 1, MaxStreamBytes: defaultSnapshotMaxStreamBytes + 1},
	} {
		installer := &scriptedSnapshotInstaller{
			required:  pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
			transport: limits,
		}
		if _, bounded, err := snapshotTransportLimitsFor(installer); err == nil || bounded {
			t.Fatalf("invalid receipt transport limits %+v = bounded %t, err %v", limits, bounded, err)
		}
	}
}

func TestSnapshotClientsEnforceTransportLimitsBeforeInstall(t *testing.T) {
	header := marshalSnapshotTransportFrame(t, &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
			Format: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
		}},
	})
	oversized := marshalSnapshotTransportFrame(t, &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
			Format: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
			CutoffSeqPerOrigin: map[string]uint64{
				strings.Repeat("x", 512): 1,
			},
		}},
	})
	duplicate := bytes.Repeat(header, 64)

	tests := []struct {
		name      string
		envelopes []rawSnapshotEnvelope
		limits    SnapshotTransportLimits
		wantCode  connect.Code
		wantText  string
	}{
		{
			name:      "oversized",
			envelopes: []rawSnapshotEnvelope{{payload: oversized}},
			limits: SnapshotTransportLimits{
				MaxFrameBytes: 64, MaxStreamBytes: 1 << 20,
			},
			wantCode: connect.CodeResourceExhausted,
			wantText: "larger than configured max",
		},
		{
			name:      "compressed oversized",
			envelopes: []rawSnapshotEnvelope{{payload: oversized, compressed: true}},
			limits: SnapshotTransportLimits{
				MaxFrameBytes: 64, MaxStreamBytes: 1 << 20,
			},
			wantCode: connect.CodeResourceExhausted,
			wantText: "larger than configured max",
		},
		{
			name: "duplicate known field transport budget",
			envelopes: []rawSnapshotEnvelope{
				{payload: header},
				{payload: duplicate},
			},
			limits: SnapshotTransportLimits{
				MaxFrameBytes:  len(duplicate),
				MaxStreamBytes: uint64(len(header) + len(duplicate) - 1),
			},
			wantCode: connect.CodeInvalidArgument,
			wantText: "exceeds remaining stream budget",
		},
	}
	for _, test := range tests {
		for _, driver := range []string{"pump", "anti-entropy"} {
			t.Run(test.name+"/"+driver, func(t *testing.T) {
				server := startRawSnapshotH2CServer(t, test.envelopes)
				installer := &scriptedSnapshotInstaller{
					required:  pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
					transport: test.limits,
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()

				var err error
				switch driver {
				case "pump":
					pump := NewPump(Config{
						HTTPClient:        defaultH2CClient(),
						SnapshotInstaller: installer,
					}, nil, nil)
					_, err = pump.snapshot(ctx, server.URL)
				case "anti-entropy":
					driver := NewAntiEntropy(AntiEntropyConfig{
						HTTPClient:        defaultH2CClient(),
						SnapshotInstaller: installer,
					}, nil, nil, nil)
					err = driver.snapshotFrom(ctx, server.URL)
				}
				if err == nil {
					t.Fatal("malicious Snapshot transport succeeded")
				}
				if connect.CodeOf(err) != test.wantCode ||
					!strings.Contains(err.Error(), test.wantText) {
					t.Fatalf(
						"malicious Snapshot transport = %v, want %v containing %q",
						err,
						test.wantCode,
						test.wantText,
					)
				}
				if got := installer.installCount(); got != 0 {
					t.Fatalf("installer published %d candidates after transport rejection", got)
				}
			})
		}
	}
}

// recordingApplier records which SnapshotApplier seam each snapshot edge
// re-apply routed to, so applySnapshotEdge's LWW-vs-G-Set routing can be
// asserted in isolation.
type recordingApplier struct {
	putEdges      []recordedEdge
	addEdges      []recordedEdge
	vertexBarrier []recordedVertexBarrier
	edgeBarrier   []recordedEdge
}

type recordedVertexBarrier struct {
	key string
	ts  hlc.Timestamp
}

type recordedEdge struct {
	tail, head string
	cid        graphcache.ContribID
	expiration time.Time
	ts         hlc.Timestamp
}

func (r *recordingApplier) PutVertexWithExpirationHLC(string, *pb.Vertex, time.Time, hlc.Timestamp) bool {
	return true
}

func (r *recordingApplier) AddEdgeWithExpirationContribHLC(tail, head string, _ float32, _ time.Time, cid graphcache.ContribID, _ hlc.Timestamp) bool {
	r.addEdges = append(r.addEdges, recordedEdge{tail: tail, head: head, cid: cid})
	return true
}

func (r *recordingApplier) PutEdgeWithExpirationHLC(tail, head string, _ float32, expiration time.Time, ts hlc.Timestamp) bool {
	r.putEdges = append(r.putEdges, recordedEdge{tail: tail, head: head, expiration: expiration, ts: ts})
	return true
}

func (r *recordingApplier) ApplyVertexCausalBarrierHLC(key string, ts hlc.Timestamp) bool {
	r.vertexBarrier = append(r.vertexBarrier, recordedVertexBarrier{key: key, ts: ts})
	return true
}

func (r *recordingApplier) ApplyEdgeCausalBarrierHLC(tail, head string, ts hlc.Timestamp) bool {
	r.edgeBarrier = append(r.edgeBarrier, recordedEdge{tail: tail, head: head, ts: ts})
	return true
}

func (r *recordingApplier) ApplySnapshotVertexTombstoneHLC(string, hlc.Timestamp, time.Time)       {}
func (r *recordingApplier) ApplySnapshotEdgeTombstoneHLC(string, string, hlc.Timestamp, time.Time) {}

type snapshotLifecycleMutationApplier struct {
	events       *[]string
	beginErr     error
	watermarkErr error
	finishes     []bool
	cutoffs      map[string]uint64
	watermarkHLC hlc.Timestamp
}

func (*snapshotLifecycleMutationApplier) ApplyMutation(context.Context, *pb.Mutation) error {
	return nil
}

func (a *snapshotLifecycleMutationApplier) BeginSnapshotInstall() (func(bool), error) {
	*a.events = append(*a.events, "begin")
	if a.beginErr != nil {
		return nil, a.beginErr
	}
	return func(verified bool) {
		a.finishes = append(a.finishes, verified)
		if verified {
			*a.events = append(*a.events, "finish:true")
			return
		}
		*a.events = append(*a.events, "finish:false")
	}, nil
}

func (a *snapshotLifecycleMutationApplier) ApplySnapshotWatermarks(
	cutoffs map[string]uint64,
	ts hlc.Timestamp,
) error {
	*a.events = append(*a.events, "watermark")
	a.cutoffs = make(map[string]uint64, len(cutoffs))
	for origin, seq := range cutoffs {
		a.cutoffs[origin] = seq
	}
	a.watermarkHLC = ts
	return a.watermarkErr
}

type snapshotLifecycleGraph struct {
	recordingApplier
	events      *[]string
	completeErr error
}

func (g *snapshotLifecycleGraph) PutVertexWithExpirationHLC(
	string,
	*pb.Vertex,
	time.Time,
	hlc.Timestamp,
) bool {
	*g.events = append(*g.events, "vertex")
	return true
}

func (g *snapshotLifecycleGraph) BeginSearchIndexRecovery() {
	*g.events = append(*g.events, "search-begin")
}

func (g *snapshotLifecycleGraph) CompleteSearchIndexRecovery() error {
	*g.events = append(*g.events, "search-complete")
	return g.completeErr
}

type snapshotSliceStream struct {
	frames  []*pb.SnapshotResponse
	next    int
	current *pb.SnapshotResponse
	err     error
}

func (s *snapshotSliceStream) Receive() bool {
	if s.next == len(s.frames) {
		s.current = nil
		return false
	}
	s.current = s.frames[s.next]
	s.next++
	return true
}

func (s *snapshotSliceStream) Msg() *pb.SnapshotResponse {
	return s.current
}

func (s *snapshotSliceStream) Err() error {
	return s.err
}

func graphInstallerLifecycleFrames() []*pb.SnapshotResponse {
	return []*pb.SnapshotResponse{
		{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
			Format:             pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
			CutoffSeqPerOrigin: map[string]uint64{"origin-a": 7},
			CutoffLocalSeq:     20,
			CutoffHlc: &pb.HLCTimestamp{
				WallNs: 30,
				NodeId: append([]byte{1}, make([]byte, 15)...),
			},
		}}},
		{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{
			Vertex: &pb.Vertex{Key: "installed"},
		}}},
		{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{
			VertexCount: 1,
		}}},
	}
}

func TestGraphOnlySnapshotInstallerLifecycle(t *testing.T) {
	t.Run("complete publishes watermarks before verified finish", func(t *testing.T) {
		var events []string
		apply := &snapshotLifecycleMutationApplier{events: &events}
		graph := &snapshotLifecycleGraph{events: &events}
		frames := graphInstallerLifecycleFrames()

		result, err := installSnapshot(
			context.Background(),
			newGraphOnlySnapshotInstaller(apply, graph),
			&snapshotSliceStream{frames: frames},
		)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Join(events, ","), "begin,search-begin,vertex,search-complete,watermark,finish:true"; got != want {
			t.Fatalf("lifecycle = %q, want %q", got, want)
		}
		if len(apply.finishes) != 1 || !apply.finishes[0] {
			t.Fatalf("finish calls = %v, want [true]", apply.finishes)
		}
		if apply.cutoffs["origin-a"] != 7 || apply.watermarkHLC.WallNs != 30 {
			t.Fatalf("published watermark = (%v, %+v)", apply.cutoffs, apply.watermarkHLC)
		}
		if result.Graph.Vertices != 1 || result.Graph.Edges != 0 {
			t.Fatalf("graph counts = %+v", result.Graph)
		}
		if result.Header == frames[0].GetHeader() || result.Header.GetCutoffLocalSeq() != 20 {
			t.Fatalf("result header is not an owned clone: %+v", result.Header)
		}
		frames[0].GetHeader().CutoffLocalSeq = 99
		if result.Header.GetCutoffLocalSeq() != 20 {
			t.Fatalf("result header changed with stream header: %+v", result.Header)
		}
	})

	t.Run("search completion failure is nonfatal", func(t *testing.T) {
		var events []string
		indexErr := errors.New("index incomplete")
		apply := &snapshotLifecycleMutationApplier{events: &events}
		graph := &snapshotLifecycleGraph{events: &events, completeErr: indexErr}

		result, err := installSnapshot(
			context.Background(),
			newGraphOnlySnapshotInstaller(apply, graph),
			&snapshotSliceStream{frames: graphInstallerLifecycleFrames()},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !errors.Is(result.searchIndexErr, indexErr) {
			t.Fatalf("search completion result = %v, want %v", result.searchIndexErr, indexErr)
		}
		if len(apply.finishes) != 1 || !apply.finishes[0] {
			t.Fatalf("finish calls = %v, want [true]", apply.finishes)
		}
		if got := strings.Join(events, ","); !strings.HasSuffix(got, "search-complete,watermark,finish:true") {
			t.Fatalf("nonfatal lifecycle = %q", got)
		}
	})

	for _, tc := range []struct {
		name         string
		change       func([]*pb.SnapshotResponse) []*pb.SnapshotResponse
		streamErr    error
		watermarkErr error
	}{
		{
			name: "missing footer",
			change: func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
				return frames[:len(frames)-1]
			},
		},
		{
			name: "footer count mismatch",
			change: func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
				frames[len(frames)-1].GetFooter().VertexCount = 2
				return frames
			},
		},
		{
			name:      "receive failure",
			change:    func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse { return frames },
			streamErr: errors.New("receive failed"),
		},
		{
			name:         "watermark publication failure",
			change:       func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse { return frames },
			watermarkErr: errors.New("watermark failed"),
		},
	} {
		t.Run(tc.name+" finishes incomplete", func(t *testing.T) {
			var events []string
			apply := &snapshotLifecycleMutationApplier{
				events: &events, watermarkErr: tc.watermarkErr,
			}
			graph := &snapshotLifecycleGraph{events: &events}
			_, err := installSnapshot(
				context.Background(),
				newGraphOnlySnapshotInstaller(apply, graph),
				&snapshotSliceStream{
					frames: tc.change(graphInstallerLifecycleFrames()),
					err:    tc.streamErr,
				},
			)
			if err == nil {
				t.Fatal("incomplete install succeeded")
			}
			if len(apply.finishes) != 1 || apply.finishes[0] {
				t.Fatalf("finish calls = %v, want [false]", apply.finishes)
			}
			if got := events[len(events)-1]; got != "finish:false" {
				t.Fatalf("final lifecycle event = %q, want finish:false; all=%v", got, events)
			}
		})
	}

	t.Run("admission failure does not start index recovery", func(t *testing.T) {
		var events []string
		apply := &snapshotLifecycleMutationApplier{
			events: &events, beginErr: errors.New("install busy"),
		}
		graph := &snapshotLifecycleGraph{events: &events}
		_, err := installSnapshot(
			context.Background(),
			newGraphOnlySnapshotInstaller(apply, graph),
			&snapshotSliceStream{frames: graphInstallerLifecycleFrames()},
		)
		if err == nil {
			t.Fatal("admission failure succeeded")
		}
		if got := strings.Join(events, ","); got != "begin" {
			t.Fatalf("admission lifecycle = %q, want begin only", got)
		}
		if len(apply.finishes) != 0 {
			t.Fatalf("finish called without admission: %v", apply.finishes)
		}
	})
}

func TestApplySnapshotCausalBarriers(t *testing.T) {
	r := &recordingApplier{}
	ts := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0x20}}
	r.ApplyVertexCausalBarrierHLC("barrier-vertex", ts)
	r.ApplyEdgeCausalBarrierHLC("barrier-tail", "barrier-head", ts)
	if len(r.addEdges) != 0 || len(r.putEdges) != 0 {
		t.Fatalf("barrier leaked into live apply seams: add=%d put=%d", len(r.addEdges), len(r.putEdges))
	}
	if len(r.vertexBarrier) != 1 || r.vertexBarrier[0].key != "barrier-vertex" || r.vertexBarrier[0].ts != ts {
		t.Fatalf("vertex barrier = %+v", r.vertexBarrier)
	}
	if len(r.edgeBarrier) != 1 {
		t.Fatalf("edge barrier count = %d, want 1", len(r.edgeBarrier))
	}
	got := r.edgeBarrier[0]
	if got.tail != "barrier-tail" || got.head != "barrier-head" || got.ts != ts {
		t.Fatalf("edge barrier = %+v", got)
	}
}

// TestApplySnapshotEdge pins the #735 fix: a Put-origin (LWW, zero ContribID)
// snapshot edge must be re-applied via PutEdgeWithExpirationHLC (idempotent
// overwrite), NOT AddEdgeWithExpirationContribHLC (which G-Set-appends and, for
// a zero cid, skips dedup — accumulating a fresh contribution on every repeated
// snapshot / anti-entropy re-sync and leaking heap). AddEdge-origin edges
// (non-zero ContribID) must keep the dedup-aware AddEdge path.
func TestApplySnapshotEdge(t *testing.T) {
	now := time.Now().Add(time.Hour)
	var ts hlc.Timestamp

	t.Run("zero contribID routes to Put across repeated re-applies", func(t *testing.T) {
		r := &recordingApplier{}
		var zero graphcache.ContribID
		// Re-apply the SAME zero-cid edge three times, simulating repeated
		// full snapshots (the anti-entropy storm in #735). Every pass must
		// route to PutEdge so the edge is overwritten, never appended.
		for i := 0; i < 3; i++ {
			applySnapshotEdge(r, "a", "b", 1.5, now, zero, ts)
		}
		if len(r.addEdges) != 0 {
			t.Fatalf("zero-cid edge routed to AddEdge %d time(s); want 0 (must use Put to stay idempotent)", len(r.addEdges))
		}
		if len(r.putEdges) != 3 {
			t.Fatalf("zero-cid edge routed to Put %d time(s); want 3", len(r.putEdges))
		}
	})

	t.Run("non-zero contribID routes to AddEdge", func(t *testing.T) {
		r := &recordingApplier{}
		var cid graphcache.ContribID
		cid[0] = 0x01 // non-zero origin byte => G-Set (AddEdge) contribution
		applySnapshotEdge(r, "a", "b", 1.5, now, cid, ts)
		if len(r.putEdges) != 0 {
			t.Fatalf("non-zero-cid edge routed to Put %d time(s); want 0", len(r.putEdges))
		}
		if len(r.addEdges) != 1 {
			t.Fatalf("non-zero-cid edge routed to AddEdge %d time(s); want 1", len(r.addEdges))
		}
		if r.addEdges[0].cid != cid {
			t.Fatalf("AddEdge got cid %v; want %v", r.addEdges[0].cid, cid)
		}
	})
}

func TestSnapshotEdgeRows(t *testing.T) {
	stamp := func(wall int64) *pb.HLCTimestamp {
		return &pb.HLCTimestamp{WallNs: wall, NodeId: []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	}
	id := make([]byte, len(graphcache.ContribID{}))
	id[0] = 1
	frame := func(addStamp *pb.HLCTimestamp) *pb.SnapshotEdge {
		return &pb.SnapshotEdge{
			Tail: "tail", Head: "head", Hlc: stamp(20),
			Contributions: []*pb.SnapshotEdgeContribution{
				{Weight: 5, Hlc: stamp(20)},
				{Weight: 3, ContribId: id, Hlc: addStamp},
			},
		}
	}
	rows, err := snapshotEdgeRows(frame(stamp(30)))
	if err != nil || len(rows) != 2 || rows[0].hlc.WallNs != 20 || rows[1].hlc.WallNs != 30 {
		t.Fatalf("valid mixed rows=%+v err=%v", rows, err)
	}
	for _, tc := range []struct {
		name  string
		frame *pb.SnapshotEdge
	}{
		{"missing Add HLC", frame(nil)},
		{"Add below reset", frame(stamp(10))},
		{"Add equal to reset", frame(stamp(20))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := snapshotEdgeRows(tc.frame); err == nil {
				t.Fatal("invalid mixed Snapshot frame accepted")
			}
		})
	}
	duplicate := frame(stamp(30))
	duplicate.Contributions = append(duplicate.Contributions, duplicate.Contributions[1])
	if _, err := snapshotEdgeRows(duplicate); err == nil {
		t.Fatal("duplicate Add identity accepted")
	}
}

// TestApplySnapshotEdgeIdempotent is the end-to-end #735 regression against a
// real GraphCache: replaying the SAME zero-cid (LWW/Put-origin) snapshot edge
// many times must NOT accumulate weight. Before the fix, applySnapshotEdge
// routed through AddEdgeWithExpirationContribHLC, whose zero-cid path skips
// dedup and G-Set-appends a fresh contribution on every pass, so the edge
// weight grew without bound on each repeated snapshot / anti-entropy re-sync.
func TestApplySnapshotEdgeIdempotent(t *testing.T) {
	c := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	exp := time.Now().Add(time.Minute)

	t.Run("zero-cid edge does not accumulate across repeated snapshots", func(t *testing.T) {
		var zero graphcache.ContribID
		for i := 0; i < 5; i++ {
			applySnapshotEdge(c, "a", "b", 2.5, exp, zero, hlc.Timestamp{})
		}
		got, ok := c.GetWeight("a", "b")
		if !ok {
			t.Fatalf("edge a->b missing after re-applies")
		}
		if got != 2.5 {
			t.Fatalf("edge weight = %v after 5 zero-cid re-applies; want 2.5 (LWW overwrite, no accumulation)", got)
		}
	})

	t.Run("non-zero-cid edge dedups on ContribID", func(t *testing.T) {
		var cid graphcache.ContribID
		cid[0] = 0x07
		for i := 0; i < 5; i++ {
			applySnapshotEdge(c, "x", "y", 3.0, exp, cid, hlc.Timestamp{})
		}
		got, ok := c.GetWeight("x", "y")
		if !ok {
			t.Fatalf("edge x->y missing after re-applies")
		}
		if got != 3.0 {
			t.Fatalf("edge weight = %v after 5 same-cid re-applies; want 3.0 (G-Set dedup)", got)
		}
	})
}

func TestResumeAfterSnapshot(t *testing.T) {
	t.Run("empty header has no cursor", func(t *testing.T) {
		if got := resumeAfterSnapshot(nil); got.origins != nil || got.local != 0 {
			t.Fatalf("resumeAfterSnapshot(nil) = %+v, want zero cursor", got)
		}
	})

	t.Run("cutoffs advance to the next origin sequence", func(t *testing.T) {
		header := &pb.SnapshotHeader{
			CutoffSeqPerOrigin: map[string]uint64{"origin-a": 7, "origin-b": 12},
			CutoffLocalSeq:     20,
		}
		got := resumeAfterSnapshot(header)
		if got.origins["origin-a"] != 8 || got.origins["origin-b"] != 13 || got.local != 21 {
			t.Fatalf("resume cursor = %+v, want origin-a=8 origin-b=13 local=21", got)
		}
	})

	t.Run("maximum cutoff does not wrap", func(t *testing.T) {
		header := &pb.SnapshotHeader{
			CutoffSeqPerOrigin: map[string]uint64{"origin-max": ^uint64(0)},
			CutoffLocalSeq:     ^uint64(0),
		}
		got := resumeAfterSnapshot(header)
		if got.origins["origin-max"] != ^uint64(0) || got.local != ^uint64(0) {
			t.Fatalf("maximum cutoffs resumed at %+v, want no wrap", got)
		}
	})
}

func TestSnapshotReplayStateFailClosed(t *testing.T) {
	header := &pb.SnapshotHeader{}
	footer := func(v, e, vb, eb uint64, tombstones ...uint64) *pb.SnapshotFooter {
		f := &pb.SnapshotFooter{
			VertexCount: v, EdgeCount: e,
			VertexCausalBarrierCount: vb, EdgeCausalBarrierCount: eb,
		}
		if len(tombstones) == 2 {
			f.VertexTombstoneCount, f.EdgeTombstoneCount = tombstones[0], tombstones[1]
		}
		return f
	}

	t.Run("complete ordered stream", func(t *testing.T) {
		var state snapshotReplayState
		if err := state.acceptHeader(header); err != nil {
			t.Fatal(err)
		}
		for _, step := range []struct {
			kind  string
			phase snapshotFramePhase
		}{
			{"vertex causal barrier", snapshotPhaseVertexBarrier},
			{"edge causal barrier", snapshotPhaseEdgeBarrier},
			{"vertex tombstone", snapshotPhaseVertexTombstone},
			{"edge tombstone", snapshotPhaseEdgeTombstone},
			{"vertex", snapshotPhaseVertex},
			{"edge", snapshotPhaseEdge},
		} {
			if err := state.acceptBody(step.kind, step.phase); err != nil {
				t.Fatal(err)
			}
		}
		state.counts = snapshotReplayCounts{vertices: 1, edges: 2, vertexBarrier: 3, edgeBarrier: 4, vertexTombstone: 5, edgeTombstone: 6}
		if err := state.acceptFooter(footer(1, 2, 3, 4, 5, 6)); err != nil {
			t.Fatal(err)
		}
		if err := state.validateComplete(); err != nil {
			t.Fatal(err)
		}
	})

	for _, tc := range []struct {
		name string
		run  func(*snapshotReplayState) error
	}{
		{"body before header", func(s *snapshotReplayState) error { return s.acceptBody("vertex", snapshotPhaseVertex) }},
		{"missing header", func(s *snapshotReplayState) error { return s.validateComplete() }},
		{"missing footer", func(s *snapshotReplayState) error { _ = s.acceptHeader(header); return s.validateComplete() }},
		{"duplicate header", func(s *snapshotReplayState) error { _ = s.acceptHeader(header); return s.acceptHeader(header) }},
		{"duplicate footer", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptFooter(footer(0, 0, 0, 0))
			return s.acceptFooter(footer(0, 0, 0, 0))
		}},
		{"body after footer", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptFooter(footer(0, 0, 0, 0))
			return s.acceptBody("edge", snapshotPhaseEdge)
		}},
		{"barrier after live vertex", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("vertex", snapshotPhaseVertex)
			return s.acceptBody("edge causal barrier", snapshotPhaseEdgeBarrier)
		}},
		{"tombstone after live vertex", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("vertex", snapshotPhaseVertex)
			return s.acceptBody("vertex tombstone", snapshotPhaseVertexTombstone)
		}},
		{"edge tombstone before vertex tombstone", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("edge tombstone", snapshotPhaseEdgeTombstone)
			return s.acceptBody("vertex tombstone", snapshotPhaseVertexTombstone)
		}},
		{"vertex after live edge", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("edge", snapshotPhaseEdge)
			return s.acceptBody("vertex", snapshotPhaseVertex)
		}},
		{"footer count mismatch", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			s.counts.vertices = 1
			_ = s.acceptFooter(footer(0, 0, 0, 0))
			return s.validateComplete()
		}},
		{"truncated tombstone count", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("vertex tombstone", snapshotPhaseVertexTombstone)
			s.counts.vertexTombstone = 1
			_ = s.acceptFooter(footer(0, 0, 0, 0, 2, 0))
			return s.validateComplete()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(&snapshotReplayState{}); err == nil {
				t.Fatal("protocol violation was accepted")
			}
		})
	}
}

func TestSnapshotReplayStateFormatBeforeBody(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header pb.SnapshotFormat
		want   connect.Code
	}{
		{"legacy graph header", pb.SnapshotFormat_SNAPSHOT_FORMAT_UNSPECIFIED, connect.Code(0)},
		{"versioned graph header", pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, connect.Code(0)},
		{"graph receiver rejects receipt header", pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT, connect.CodeFailedPrecondition},
		{"unknown header", pb.SnapshotFormat(99), connect.CodeFailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := snapshotReplayState{}
			err := state.acceptHeader(&pb.SnapshotHeader{Format: tc.header})
			if tc.want == 0 {
				if err != nil || !state.gotHeader {
					t.Fatalf("matching header = (%v, got=%v)", err, state.gotHeader)
				}
				return
			}
			if connect.CodeOf(err) != tc.want || state.gotHeader {
				t.Fatalf("mismatched header = (%v, got=%v), want %v and no install", err, state.gotHeader, tc.want)
			}
		})
	}
}

func TestSnapshotTombstoneFields(t *testing.T) {
	stamp := &pb.HLCTimestamp{WallNs: 10, NodeId: append([]byte{1}, make([]byte, 15)...)}
	deadline := time.Now().Add(time.Minute).UTC().Round(0)
	gotTS, gotDeadline, err := snapshotTombstoneFields(stamp, timestamppb.New(deadline))
	if err != nil || gotTS.WallNs != 10 || !gotDeadline.Equal(deadline) {
		t.Fatalf("valid tombstone = (%+v,%v,%v)", gotTS, gotDeadline, err)
	}
	for _, tc := range []struct {
		name       string
		stamp      *pb.HLCTimestamp
		expiration *timestamppb.Timestamp
	}{
		{"missing HLC", nil, timestamppb.New(deadline)},
		{"zero HLC", &pb.HLCTimestamp{NodeId: make([]byte, 16)}, timestamppb.New(deadline)},
		{"short NodeID", &pb.HLCTimestamp{WallNs: 10, NodeId: []byte{1}}, timestamppb.New(deadline)},
		{"zero NodeID", &pb.HLCTimestamp{WallNs: 10, NodeId: make([]byte, 16)}, timestamppb.New(deadline)},
		{"missing expiration", stamp, nil},
		{"invalid expiration", stamp, &timestamppb.Timestamp{Seconds: 253402300800}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := snapshotTombstoneFields(tc.stamp, tc.expiration); err == nil {
				t.Fatal("malformed tombstone frame accepted")
			}
		})
	}
}
