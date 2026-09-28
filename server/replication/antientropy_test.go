package replication

import (
	"context"
	"encoding/hex"
	"errors"
	"maps"
	"testing"
	"time"

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
