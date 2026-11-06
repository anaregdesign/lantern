package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"google.golang.org/protobuf/proto"
)

// This is a protocol-only HTTP/2 Connect mount over the certified receipt
// runtime, not a production public listener or authentication fixture.
func newCompressedReceiptProtocolWireServer(t *testing.T, nodeID hlc.NodeID, netCfg provider.NetConfig) receiptProtocolWireServer {
	t.Helper()
	wire := newReceiptProtocolWireServerWithNet(t, nodeID, 32, netCfg)
	server := newConnectTestServerWithOptions(t, wire.server.svc, wire.server.rep,
		connect.WithReadMaxBytes(netCfg.MaxRecvMsgBytes),
		connect.WithSendMaxBytes(netCfg.MaxSendMsgBytes),
		connect.WithCompressMinBytes(1024),
		connect.WithInterceptors(provider.NewValidationInterceptor(defaultIntegrationValidationLimits()).ConnectInterceptor()))
	wire.server = server
	wire.raw = graphv1connect.NewLanternServiceClient(h2cClient(), server.url)
	return wire
}

func TestCompressionPreservesReceiptProtocolAndPeerRelay_RealWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	netCfg := provider.NetConfig{MaxRecvMsgBytes: 8 << 10, MaxSendMsgBytes: 4 << 10}
	origin := newCompressedReceiptProtocolWireServer(t, hlc.NodeID{0xa1}, netCfg)
	follower := newCompressedReceiptProtocolWireServer(t, hlc.NodeID{0xb2}, netCfg)

	capability := receiptProtocolCapability(t, origin)
	receiptContext := receiptProtocolContext(t, capability, 0xc3, 1,
		time.UnixMilli(int64(capability.GetServerNowUnixMs())).Add(-time.Second))
	small := &pb.Vertex{Key: "compression/small", Value: &pb.Vertex_String_{String_: "s"}}
	if result, err := origin.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: small})); err != nil || result.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("small PutVertex = (%v, %v)", result, err)
	}
	large := &pb.Vertex{Key: "compression/large", Value: &pb.Vertex_Bytes{
		Bytes: bytes.Repeat([]byte{'a'}, 1500),
	}}
	if result, err := origin.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: large, ReceiptContext: receiptContext})); err != nil || result.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("receipt PutVertex = (%v, %v)", result, err)
	}

	statuses := receiptProtocolStatuses(t, ctx, origin, receiptContext.GetOperationIds())
	original := statuses[0].GetReceipt()
	if statuses[0].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		original == nil ||
		!bytes.Equal(original.GetOperationId(), receiptContext.GetOperationIds()[0]) ||
		!bytes.Equal(original.GetLogicalCallId(), receiptContext.GetLogicalCallId()) ||
		len(original.GetIntentSha256()) != sha256.Size ||
		original.GetOriginalResult().GetPutVertexOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("origin receipt status = %+v", statuses[0])
	}

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	httpClient := &http.Client{Transport: &http.Transport{Protocols: protocols, DisableCompression: true}}
	t.Cleanup(httpClient.CloseIdleConnections)
	payload, err := proto.Marshal(&pb.GetReceiptStatusesRequest{OperationIds: receiptContext.GetOperationIds()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		origin.server.url+graphv1connect.LanternServiceGetReceiptStatusesProcedure, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close receipt status response: %v", err)
		}
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" {
		t.Fatalf("small protocol status HTTP %d, encoding %q: %s",
			response.StatusCode, response.Header.Get("Content-Encoding"), body)
	}
	var rawStatus pb.GetReceiptStatusesResponse
	if err := proto.Unmarshal(body, &rawStatus); err != nil {
		t.Fatal(err)
	}
	if proto.Size(&rawStatus) >= 1024 ||
		!proto.Equal(&rawStatus, &pb.GetReceiptStatusesResponse{Statuses: statuses}) {
		t.Fatalf("small protocol status = %v, want original receipt under 1024 bytes", &rawStatus)
	}

	replicationClient := graphv1connect.NewLanternReplicationServiceClient(h2cClient(), origin.server.url)
	subscribe := &pb.SubscribeRequest{FromLocalSeq: 1, AcceptReceiptEnvelopes: true}
	stream, err := replicationClient.Subscribe(ctx, connect.NewRequest(subscribe))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stream.Close(); err != nil {
			t.Errorf("close receipt Subscribe stream: %v", err)
		}
	}()
	for i, wantKey := range []string{small.GetKey(), large.GetKey()} {
		if !stream.Receive() {
			t.Fatalf("Subscribe[%d]: %v", i, stream.Err())
		}
		frame := stream.Msg()
		if (proto.Size(frame) < 1024) != (i == 0) {
			t.Fatalf("Subscribe[%d] size %d does not straddle 1024 bytes", i, proto.Size(frame))
		}
		if i == 0 {
			items := frame.GetMutation().GetOp().GetReplicatedPutVertices().GetEntries()
			if len(items) != 1 || items[0].GetLive().GetKey() != wantKey {
				t.Fatalf("small Subscribe = %v", frame)
			}
		} else {
			items := frame.GetMutation().GetOp().GetReplicatedReceiptVertexPut().GetItems()
			if len(items) != 1 || items[0].GetOriginal().GetKey() != wantKey ||
				!proto.Equal(items[0].GetReceipt(), original) {
				t.Fatalf("receipt Subscribe = %v", frame)
			}
		}
	}

	stopPump := startReceiptProtocolPump(t, ctx, "compressed protocol relay", follower, origin)
	defer stopPump()
	wantCut := map[string]uint64{hex.EncodeToString(origin.config.NodeID[:]): 2}
	requireExactReceiptCut(t, "compressed protocol relay", waitForReceiptProtocolCut(t, ctx, "compressed protocol relay", follower, wantCut, 5*time.Second), wantCut)
	relayed := receiptProtocolStatuses(t, ctx, follower, receiptContext.GetOperationIds())
	if relayed[0].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		!bytes.Equal(relayed[0].GetOperationId(), original.GetOperationId()) ||
		!bytes.Equal(relayed[0].GetReceipt().GetIntentSha256(), original.GetIntentSha256()) ||
		!proto.Equal(relayed[0].GetReceipt(), original) {
		t.Fatalf("relayed receipt = %+v, want %+v", relayed[0], original)
	}
	readBack, err := follower.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: large.GetKey()}))
	if err != nil || !bytes.Equal(readBack.Msg.GetVertex().GetBytes(), large.GetBytes()) {
		t.Fatalf("relayed large vertex = (%v, %v)", readBack, err)
	}

	boundedCfg := provider.NetConfig{MaxRecvMsgBytes: 8 << 10, MaxSendMsgBytes: 1024}
	bounded := newCompressedReceiptProtocolWireServer(t, hlc.NodeID{0xd4}, boundedCfg)
	boundedCapability := receiptProtocolCapability(t, bounded)
	boundedContext := receiptProtocolContext(t, boundedCapability, 0xe5, 1,
		time.UnixMilli(int64(boundedCapability.GetServerNowUnixMs())).Add(-time.Second))
	_, err = bounded.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: large, ReceiptContext: boundedContext}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted ||
		!strings.Contains(err.Error(), "LANTERN_MAX_SEND_MSG_BYTES=1024") {
		t.Fatalf("oversized full receipt frame = %v, want pre-publication ResourceExhausted", err)
	}
	if stats := bounded.runtime.ReceiptStats(); stats.Entries != 0 {
		t.Fatalf("rejected receipt changed Store: %+v", stats)
	}
	if length, _, _ := bounded.runtime.MutationLogStats(); length != 0 {
		t.Fatalf("rejected receipt left %d log entries", length)
	}
	if _, err := bounded.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: large.GetKey()})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("rejected receipt left vertex: %v", err)
	}
}
