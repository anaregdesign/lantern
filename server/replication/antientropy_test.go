package replication

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
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
