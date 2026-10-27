package replication

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/replicationstatus"
)

func TestAntiEntropyUsesInjectedSnapshotInstallerAndResumes(t *testing.T) {
	origin := hlc.NodeID{0x41}
	peer := &installerTestPeer{
		requiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
		header: &pb.SnapshotHeader{
			Format:             pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
			CutoffSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 7},
			CutoffLocalSeq:     20,
		},
		selfOrigin:        origin,
		originSeq:         1,
		gapFirstSubscribe: true,
	}
	server := startInstallerTestPeer(t, peer)
	installer := &scriptedSnapshotInstaller{
		required: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
	}
	driver := NewAntiEntropy(AntiEntropyConfig{
		HTTPClient:        defaultH2CClient(),
		SubscribeTimeout:  time.Second,
		SnapshotInstaller: installer,
	}, fixedLocalState{}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	driver.tickPeer(ctx, server.URL)
	if got := installer.installCount(); got != 1 {
		t.Fatalf("installer calls = %d, want 1", got)
	}
	if got := driver.snapshotResumeLocal(server.URL); got != 21 {
		t.Fatalf("remembered local resume = %d, want 21", got)
	}
	subscribes, snapshots := peer.requests()
	if len(subscribes) != 1 || !subscribes[0].GetAcceptReceiptEnvelopes() {
		t.Fatalf("initial Subscribe requests = %+v, want one receipt-aware request", subscribes)
	}
	if got := subscribes[0].GetFromSeqPerOrigin()[hex.EncodeToString(origin[:])]; got != 1 {
		t.Fatalf("initial origin cursor = %d, want 1", got)
	}
	if len(snapshots) != 1 ||
		snapshots[0].GetRequiredFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
		t.Fatalf("Snapshot requests = %+v, want one RECEIPT request", snapshots)
	}

	driver.tickPeer(ctx, server.URL)
	subscribes, snapshots = peer.requests()
	if len(subscribes) != 2 {
		t.Fatalf("Subscribe requests after resume = %d, want 2", len(subscribes))
	}
	if got := subscribes[1].GetFromLocalSeq(); got != 21 {
		t.Fatalf("same-responder local resume = %d, want 21", got)
	}
	if !subscribes[1].GetAcceptReceiptEnvelopes() {
		t.Fatal("resumed full Subscribe did not accept receipt envelopes")
	}
	if len(snapshots) != 1 || installer.installCount() != 1 {
		t.Fatalf("resume unexpectedly repeated Snapshot: snapshots=%d installs=%d", len(snapshots), installer.installCount())
	}
}

func TestAntiEntropyCatchUpUsesCommittedOriginVector(t *testing.T) {
	peerOrigin := hlc.NodeID{0x42}
	peerKey := hex.EncodeToString(peerOrigin[:])
	otherOrigin := hlc.NodeID{0x43}
	otherKey := hex.EncodeToString(otherOrigin[:])
	state := &cursorTestState{
		fixedLocalState: fixedLocalState{seq: 2},
		cursor:          map[string]uint64{peerKey: 2, otherKey: 8},
	}
	peer := &installerTestPeer{
		requiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		selfOrigin:     peerOrigin,
		originSeq:      5,
	}
	server := startInstallerTestPeer(t, peer)
	driver := NewAntiEntropy(AntiEntropyConfig{
		HTTPClient: defaultH2CClient(), SubscribeTimeout: time.Second,
	}, state, state, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	driver.tickPeer(ctx, server.URL)
	subscribes, snapshots := peer.requests()
	want := map[string]uint64{peerKey: 3, otherKey: 8}
	if len(subscribes) != 1 || len(snapshots) != 0 ||
		!maps.Equal(subscribes[0].GetFromSeqPerOrigin(), want) {
		t.Fatalf("catch-up = (%+v, %+v), want complete vector %v", subscribes, snapshots, want)
	}
	if state.cursor[peerKey] != 2 {
		t.Fatalf("catch-up mutated the committed vector: %v", state.cursor)
	}

	state.err = errors.New("committed cursor unavailable")
	cli := graphv1connect.NewLanternReplicationServiceClient(defaultH2CClient(), server.URL)
	if _, err := driver.catchUp(ctx, server.URL, cli, peerOrigin, 3, 5); !errors.Is(err, state.err) {
		t.Fatalf("cursor capture error = %v, want %v", err, state.err)
	}
	subscribes, snapshots = peer.requests()
	if len(subscribes) != 1 || len(snapshots) != 0 {
		t.Fatalf("cursor capture failure opened a stream or Snapshot: (%d, %d)", len(subscribes), len(snapshots))
	}
}

func TestAntiEntropyTransientPeerGapWaitsForCommittedRetry(t *testing.T) {
	origin := hlc.NodeID{0x47}
	key := hex.EncodeToString(origin[:])
	for _, reason := range []string{
		replicationstatus.ReasonSubscriberStreamClosed,
		replicationstatus.ReasonPublicationFault,
	} {
		t.Run(reason, func(t *testing.T) {
			peer := &installerTestPeer{
				selfOrigin: origin, originSeq: 5,
				firstSubscribeErr: replicationstatus.TransientGap(reason, errors.New("gapped")),
			}
			server := startInstallerTestPeer(t, peer)
			state := &cursorTestState{fixedLocalState: fixedLocalState{seq: 2}, cursor: map[string]uint64{key: 2}}
			driver := NewAntiEntropy(AntiEntropyConfig{
				HTTPClient: defaultH2CClient(), SubscribeTimeout: time.Second,
			}, state, state, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			driver.tickPeer(ctx, server.URL)
			subscribes, snapshots := peer.requests()
			if len(subscribes) != 1 || len(snapshots) != 0 || subscribes[0].GetFromSeqPerOrigin()[key] != 3 {
				t.Fatalf("transient tick: subscribes=%+v snapshots=%+v", subscribes, snapshots)
			}
			state.cursor = map[string]uint64{key: 4}
			driver.tickPeer(ctx, server.URL)
			subscribes, snapshots = peer.requests()
			if len(subscribes) != 2 || len(snapshots) != 0 ||
				subscribes[1].GetFromSeqPerOrigin()[key] != 4 {
				t.Fatalf("retry: subscribes=%+v snapshots=%+v, want origin 4 without Snapshot", subscribes, snapshots)
			}
		})
	}
}

type advancingLocalState struct{ seq atomic.Uint64 }

func (s *advancingLocalState) LocalSeq(hlc.NodeID) uint64 { return s.seq.Load() }

func TestAntiEntropyDefersToAdvancingPumpButRepairsStalledOrigin(t *testing.T) {
	origin := hlc.NodeID{0x52}
	peer := &installerTestPeer{
		requiredFormat:    pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		selfOrigin:        origin,
		originSeq:         10,
		gapFirstSubscribe: true,
	}
	server := startInstallerTestPeer(t, peer)
	pump := NewPump(Config{}, nil, nil)
	pump.tracker.setState(server.URL, PeerStateStreaming)
	pump.tracker.recordEvent(server.URL, 2, time.Now())
	local := &advancingLocalState{}
	local.seq.Store(2)
	installer := &scriptedSnapshotInstaller{required: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1}
	driver := NewAntiEntropy(AntiEntropyConfig{
		HTTPClient: defaultH2CClient(), SubscribeTimeout: time.Second,
		Pump: pump, SnapshotInstaller: installer,
	}, local, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	driver.tickPeer(ctx, server.URL)
	local.seq.Store(3)
	pump.tracker.recordEvent(server.URL, 3, time.Now())
	driver.tickPeer(ctx, server.URL)
	subscribes, snapshots := peer.requests()
	if len(subscribes) != 0 || len(snapshots) != 0 {
		t.Fatalf("advancing pump triggered redundant repair: subscribes=%d snapshots=%d",
			len(subscribes), len(snapshots))
	}

	pump.tracker.recordEvent(server.URL, 3, time.Now())
	driver.tickPeer(ctx, server.URL)
	subscribes, snapshots = peer.requests()
	if len(subscribes) != 1 || len(snapshots) != 1 || installer.installCount() != 1 {
		t.Fatalf("stalled origin did not Snapshot on proven gap: subscribes=%d snapshots=%d installs=%d",
			len(subscribes), len(snapshots), installer.installCount())
	}
}

func TestAntiEntropyStalePumpDoesNotDeferRepair(t *testing.T) {
	origin := hlc.NodeID{0x53}
	peer := &installerTestPeer{
		requiredFormat:    pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		selfOrigin:        origin,
		originSeq:         10,
		gapFirstSubscribe: true,
	}
	server := startInstallerTestPeer(t, peer)
	pump := NewPump(Config{}, nil, nil)
	pump.tracker.recordEvent(server.URL, 2, time.Now().Add(-time.Minute))
	local := &advancingLocalState{}
	local.seq.Store(2)
	installer := &scriptedSnapshotInstaller{required: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1}
	driver := NewAntiEntropy(AntiEntropyConfig{
		HTTPClient: defaultH2CClient(), SubscribeTimeout: time.Second,
		Pump: pump, SnapshotInstaller: installer,
	}, local, nil, nil)

	driver.tickPeer(t.Context(), server.URL)
	subscribes, snapshots := peer.requests()
	if len(subscribes) != 1 || len(snapshots) != 1 || installer.installCount() != 1 {
		t.Fatalf("stale pump skipped required repair: subscribes=%d snapshots=%d installs=%d",
			len(subscribes), len(snapshots), installer.installCount())
	}
}

func TestAntiEntropyInjectedSnapshotInstallerRejectsIncompatibleFormats(t *testing.T) {
	origin := hlc.NodeID{0x51}
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
			peer := &installerTestPeer{
				requiredFormat:    tc.statusFormat,
				header:            &pb.SnapshotHeader{Format: tc.headerFormat},
				selfOrigin:        origin,
				originSeq:         1,
				gapFirstSubscribe: true,
			}
			server := startInstallerTestPeer(t, peer)
			installer := &scriptedSnapshotInstaller{
				required:   pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
				compatible: func(pb.SnapshotFormat) bool { return true },
			}
			driver := NewAntiEntropy(AntiEntropyConfig{
				HTTPClient:        defaultH2CClient(),
				SubscribeTimeout:  time.Second,
				SnapshotInstaller: installer,
			}, fixedLocalState{}, nil, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			driver.tickPeer(ctx, server.URL)
			if got := installer.installCount(); got != 0 {
				t.Fatalf("installer calls = %d, want 0 before mutation", got)
			}
			if got := driver.snapshotResumeLocal(server.URL); got != 0 {
				t.Fatalf("resume advanced after rejected format to %d", got)
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

func TestAntiEntropySnapshotTailClaimedInSameTick(t *testing.T) {
	origin := hlc.NodeID{0x65}
	key := hex.EncodeToString(origin[:])
	token := strings.Repeat("cd", 32)
	for _, format := range []pb.SnapshotFormat{pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT} {
		t.Run(format.String(), func(t *testing.T) {
			peer := &installerTestPeer{
				requiredFormat: format, gapFirstSubscribe: true, selfOrigin: origin, originSeq: 8,
				header:             &pb.SnapshotHeader{Format: format, CutoffSeqPerOrigin: map[string]uint64{key: 7}, CutoffLocalSeq: 20},
				snapshotTailValues: []string{token},
				subscribeMutation:  &pb.Mutation{Origin: append([]byte(nil), origin[:]...), Seq: 8},
			}
			server, transport := startTailMetadataPeer(t, peer)
			installer := &scriptedSnapshotInstaller{required: format}
			state := &tailAppliedState{origin: origin}
			driver := NewAntiEntropy(AntiEntropyConfig{PeerTransport: transport, SnapshotInstaller: installer, SubscribeTimeout: time.Second}, state, state, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			driver.tickPeer(ctx, server.URL)
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if len(peer.snapshotTailMetadata) != 1 || len(peer.snapshotTailMetadata[0]) != 1 || peer.snapshotTailMetadata[0][0] != "reserve" {
				t.Fatalf("Snapshot tail request = %v", peer.snapshotTailMetadata)
			}
			if len(peer.subscribeTailMetadata) != 2 || len(peer.subscribeTailMetadata[1]) != 1 || peer.subscribeTailMetadata[1][0] != token {
				t.Fatalf("same-tick Subscribe metadata = %v", peer.subscribeTailMetadata)
			}
			resume := peer.subscribeRequests[1]
			if resume.GetFromLocalSeq() != 21 || resume.GetFromSeqPerOrigin()[key] != 8 || state.seq.Load() != 8 || installer.installCount() != 1 {
				t.Fatalf("tail resume=%v seq=%d installs=%d", resume, state.seq.Load(), installer.installCount())
			}
		})
	}
}

type tailVectorState struct {
	mu      sync.Mutex
	seqs    map[hlc.NodeID]uint64
	applied []*pb.Mutation
}

func (s *tailVectorState) LocalSeq(origin hlc.NodeID) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seqs[origin]
}

func (s *tailVectorState) SubscribeResumeCursor() (map[string]uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor := make(map[string]uint64, len(s.seqs))
	for origin, seq := range s.seqs {
		cursor[hex.EncodeToString(origin[:])] = seq + 1
	}
	return cursor, nil
}

func (s *tailVectorState) ApplyMutation(_ context.Context, mutation *pb.Mutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var origin hlc.NodeID
	copy(origin[:], mutation.GetOrigin())
	if mutation.GetSeq() != s.seqs[origin]+1 {
		return errors.New("noncontiguous test tail")
	}
	s.seqs[origin] = mutation.GetSeq()
	s.applied = append(s.applied, proto.Clone(mutation).(*pb.Mutation))
	return nil
}

func TestAntiEntropySnapshotTailDrainsForeignOriginVector(t *testing.T) {
	peerOrigin := hlc.NodeID{0x67}
	first := hlc.NodeID{0x68}
	second := hlc.NodeID{0x69}
	peerKey := hex.EncodeToString(peerOrigin[:])
	firstKey := hex.EncodeToString(first[:])
	secondKey := hex.EncodeToString(second[:])
	for _, format := range []pb.SnapshotFormat{pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT} {
		for _, ownRow := range []bool{false, true} {
			name := format.String() + "/no_own_writes"
			if ownRow {
				name = format.String() + "/own_already_covered"
			}
			t.Run(name, func(t *testing.T) {
				cut := map[string]uint64{firstKey: 7, secondKey: 4}
				state := &tailVectorState{seqs: map[hlc.NodeID]uint64{first: 7, second: 4}}
				peer := &installerTestPeer{
					requiredFormat: format, selfOrigin: peerOrigin,
					extraOrigins:       []*pb.OriginState{{Origin: first[:], LastSeq: 9}, {Origin: second[:], LastSeq: 6}},
					snapshotTailValues: []string{strings.Repeat("ef", 32)},
				}
				if ownRow {
					peer.originSeq, cut[peerKey], state.seqs[peerOrigin] = 3, 3, 3
				}
				peer.header = &pb.SnapshotHeader{Format: format, CutoffSeqPerOrigin: cut, CutoffLocalSeq: 20}
				for _, item := range []struct {
					origin hlc.NodeID
					seq    uint64
				}{{first, 8}, {second, 5}, {first, 9}, {second, 6}} {
					mutation := &pb.Mutation{Origin: append([]byte(nil), item.origin[:]...), Seq: item.seq}
					if format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
						mutation.Op = &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptVertexPut{
							ReplicatedReceiptVertexPut: &pb.ReplicatedReceiptVertexPut{DeploymentEpoch: []byte("epoch"), PolicyFingerprint: []byte("policy")},
						}}
					} else {
						mutation.Op = &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "foreign"}}}}
					}
					peer.subscribeMutations = append(peer.subscribeMutations, mutation)
				}
				server, transport := startTailMetadataPeer(t, peer)
				installer := &scriptedSnapshotInstaller{required: format}
				driver := NewAntiEntropy(AntiEntropyConfig{PeerTransport: transport, SnapshotInstaller: installer, SubscribeTimeout: time.Second}, state, state, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := driver.snapshotFrom(ctx, server.URL); err != nil {
					t.Fatalf("foreign tail catch-up: %v", err)
				}
				state.mu.Lock()
				defer state.mu.Unlock()
				if len(state.applied) != 4 || state.seqs[first] != 9 || state.seqs[second] != 6 {
					t.Fatalf("foreign tail discarded: applied=%d frontier=%v", len(state.applied), state.seqs)
				}
				for index, expected := range peer.subscribeMutations {
					if !proto.Equal(state.applied[index], expected) {
						t.Fatalf("foreign mutation %d changed in transit", index)
					}
				}
				peer.mu.Lock()
				defer peer.mu.Unlock()
				if len(peer.subscribeRequests) != 1 || len(peer.subscribeTailMetadata[0]) != 1 || peer.subscribeTailMetadata[0][0] != strings.Repeat("ef", 32) {
					t.Fatalf("foreign tail claim metadata = %v", peer.subscribeTailMetadata)
				}
				request := peer.subscribeRequests[0]
				if request.GetFromLocalSeq() != 21 || request.GetFromSeqPerOrigin()[firstKey] != 8 || request.GetFromSeqPerOrigin()[secondKey] != 5 {
					t.Fatalf("foreign tail claim cursor = %v", request)
				}
			})
		}
	}
}

func TestAntiEntropySnapshotTailRejectsMalformedTargetVector(t *testing.T) {
	origin := hlc.NodeID{0x6a}
	foreign := hlc.NodeID{0x6b}
	for _, tc := range []struct {
		name string
		rows []*pb.OriginState
	}{
		{"short", []*pb.OriginState{{Origin: []byte{1}, LastSeq: 1}}},
		{"zero_origin", []*pb.OriginState{{Origin: make([]byte, 16), LastSeq: 1}}},
		{"zero_seq", []*pb.OriginState{{Origin: foreign[:], LastSeq: 0}}},
		{"duplicate", []*pb.OriginState{{Origin: foreign[:], LastSeq: 1}, {Origin: foreign[:], LastSeq: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &installerTestPeer{selfOrigin: origin, extraOrigins: tc.rows, requiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1}
			server := startInstallerTestPeer(t, peer)
			state := &tailVectorState{seqs: make(map[hlc.NodeID]uint64)}
			driver := NewAntiEntropy(AntiEntropyConfig{HTTPClient: defaultH2CClient()}, state, state, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := driver.catchUpSnapshotTail(ctx, server.URL, server.URL, snapshotResumeCursor{}, strings.Repeat("ab", 32)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("malformed vector = %v", err)
			}
			subscribes, _ := peer.requests()
			if len(subscribes) != 0 || len(state.applied) != 0 {
				t.Fatal("malformed target vector opened or applied a tail")
			}
		})
	}
}

func TestAntiEntropySnapshotTailRequiresCompleteTargetVector(t *testing.T) {
	peerOrigin := hlc.NodeID{0x6c}
	foreign := hlc.NodeID{0x6d}
	peer := &installerTestPeer{
		selfOrigin: peerOrigin, requiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		extraOrigins:      []*pb.OriginState{{Origin: foreign[:], LastSeq: 9}},
		subscribeMutation: &pb.Mutation{Origin: foreign[:], Seq: 8},
	}
	server := startInstallerTestPeer(t, peer)
	state := &tailVectorState{seqs: map[hlc.NodeID]uint64{foreign: 7}}
	driver := NewAntiEntropy(AntiEntropyConfig{HTTPClient: defaultH2CClient()}, state, state, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := driver.catchUpSnapshotTail(ctx, server.URL, server.URL, snapshotResumeCursor{origins: map[string]uint64{hex.EncodeToString(foreign[:]): 8}}, strings.Repeat("ab", 32))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || state.LocalSeq(foreign) != 8 {
		t.Fatalf("partial tail = %v frontier=%d", err, state.LocalSeq(foreign))
	}
}

func TestAntiEntropySnapshotFailureLogsOneLocalDiagnosticOutcome(t *testing.T) {
	origin := hlc.NodeID{0x41}
	peer := &installerTestPeer{requiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		frames: graphInstallerLifecycleFrames(), selfOrigin: origin, originSeq: 1, gapFirstSubscribe: true}
	server := startInstallerTestPeer(t, peer)
	var events []string
	cause := connect.NewError(connect.CodeUnavailable, errors.New("controlled live Begin refusal"))
	apply := &snapshotLifecycleMutationApplier{events: &events, beginErr: cause}
	graph := &snapshotLifecycleGraph{events: &events}
	var logs bytes.Buffer
	driver := NewAntiEntropy(AntiEntropyConfig{HTTPClient: defaultH2CClient(),
		SubscribeTimeout: time.Second, SnapshotInstaller: testGraphOnlySnapshotInstaller(t, apply, graph),
		Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))}, fixedLocalState{}, apply, graph)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	driver.tickPeer(ctx, server.URL)
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"msg":"anti-entropy: catch-up failed"`) || !strings.Contains(lines[0], `"snapshot_phase":"live_begin"`) || !strings.Contains(lines[0], `"snapshot_live_started":false`) || !strings.Contains(lines[0], `"snapshot_staged_frames":3`) {
		t.Fatalf("expected one attributed failure outcome: %s", &logs)
	}
}
