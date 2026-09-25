package replication

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type scriptedSnapshotInstaller struct {
	mu             sync.Mutex
	required       pb.SnapshotFormat
	compatible     func(pb.SnapshotFormat) bool
	installs       int
	searchIndexErr error
}

func (i *scriptedSnapshotInstaller) RequiredFormat() pb.SnapshotFormat {
	return i.required
}

func (i *scriptedSnapshotInstaller) CompatibleFormat(format pb.SnapshotFormat) bool {
	if i.compatible != nil {
		return i.compatible(format)
	}
	return format == i.required
}

func (i *scriptedSnapshotInstaller) Install(_ context.Context, stream SnapshotStream) (SnapshotInstallResult, error) {
	var (
		header *pb.SnapshotHeader
		counts SnapshotGraphCounts
	)
	for stream.Receive() {
		frame := stream.Msg()
		if frame == nil {
			continue
		}
		owned := proto.Clone(frame).(*pb.SnapshotResponse)
		if current := owned.GetHeader(); current != nil {
			header = proto.Clone(current).(*pb.SnapshotHeader)
		}
		if footer := owned.GetFooter(); footer != nil {
			counts = SnapshotGraphCounts{
				Vertices:             footer.GetVertexCount(),
				Edges:                footer.GetEdgeCount(),
				VertexCausalBarriers: footer.GetVertexCausalBarrierCount(),
				EdgeCausalBarriers:   footer.GetEdgeCausalBarrierCount(),
				VertexTombstones:     footer.GetVertexTombstoneCount(),
				EdgeTombstones:       footer.GetEdgeTombstoneCount(),
			}
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return SnapshotInstallResult{}, err
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	i.installs++
	return SnapshotInstallResult{
		Header:         header,
		Graph:          counts,
		searchIndexErr: i.searchIndexErr,
	}, nil
}

func (i *scriptedSnapshotInstaller) installCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.installs
}

type installerTestPeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler

	mu                sync.Mutex
	requiredFormat    pb.SnapshotFormat
	header            *pb.SnapshotHeader
	frames            []*pb.SnapshotResponse
	selfOrigin        hlc.NodeID
	originSeq         uint64
	gapFirstSubscribe bool
	subscribeRequests []*pb.SubscribeRequest
	snapshotRequests  []*pb.SnapshotRequest
}

func (p *installerTestPeer) PeerStatus(
	context.Context,
	*connect.Request[pb.PeerStatusRequest],
) (*connect.Response[pb.PeerStatusResponse], error) {
	response := &pb.PeerStatusResponse{
		RequiredSnapshotFormat: p.requiredFormat,
	}
	if p.selfOrigin != (hlc.NodeID{}) {
		response.SelfOrigin = append([]byte(nil), p.selfOrigin[:]...)
		if p.originSeq > 0 {
			response.Origins = []*pb.OriginState{{
				Origin:  append([]byte(nil), p.selfOrigin[:]...),
				LastSeq: p.originSeq,
			}}
		}
	}
	return connect.NewResponse(response), nil
}

func (p *installerTestPeer) Subscribe(
	_ context.Context,
	req *connect.Request[pb.SubscribeRequest],
	_ *connect.ServerStream[pb.SubscribeResponse],
) error {
	p.mu.Lock()
	p.subscribeRequests = append(
		p.subscribeRequests,
		proto.Clone(req.Msg).(*pb.SubscribeRequest),
	)
	call := len(p.subscribeRequests)
	gap := p.gapFirstSubscribe && call == 1
	p.mu.Unlock()
	if gap {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped"))
	}
	return nil
}

func (p *installerTestPeer) Snapshot(
	_ context.Context,
	req *connect.Request[pb.SnapshotRequest],
	stream *connect.ServerStream[pb.SnapshotResponse],
) error {
	p.mu.Lock()
	p.snapshotRequests = append(
		p.snapshotRequests,
		proto.Clone(req.Msg).(*pb.SnapshotRequest),
	)
	frames := make([]*pb.SnapshotResponse, 0, len(p.frames))
	for _, frame := range p.frames {
		frames = append(frames, proto.Clone(frame).(*pb.SnapshotResponse))
	}
	if p.frames == nil {
		header := p.header
		if header == nil {
			header = &pb.SnapshotHeader{Format: p.requiredFormat}
		}
		frames = []*pb.SnapshotResponse{
			{Entry: &pb.SnapshotResponse_Header{
				Header: proto.Clone(header).(*pb.SnapshotHeader),
			}},
			{Entry: &pb.SnapshotResponse_Footer{
				Footer: &pb.SnapshotFooter{},
			}},
		}
	}
	p.mu.Unlock()
	for _, frame := range frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

func (p *installerTestPeer) requests() ([]*pb.SubscribeRequest, []*pb.SnapshotRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	subscribes := make([]*pb.SubscribeRequest, 0, len(p.subscribeRequests))
	for _, req := range p.subscribeRequests {
		subscribes = append(subscribes, proto.Clone(req).(*pb.SubscribeRequest))
	}
	snapshots := make([]*pb.SnapshotRequest, 0, len(p.snapshotRequests))
	for _, req := range p.snapshotRequests {
		snapshots = append(snapshots, proto.Clone(req).(*pb.SnapshotRequest))
	}
	return subscribes, snapshots
}

func startInstallerTestPeer(t *testing.T, peer *installerTestPeer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
	server := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	return server
}

type fixedLocalState struct {
	seq uint64
}

func (s fixedLocalState) LocalSeq(hlc.NodeID) uint64 {
	return s.seq
}
