package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
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

func newProviderReceiptWireServer(
	t *testing.T,
	nodeID hlc.NodeID,
	netCfg provider.NetConfig,
	token string,
) publicReceiptWireServer {
	t.Helper()
	wire := newPublicReceiptWireServerWithNet(t, nodeID, 32, netCfg, token)
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listener, err := provider.NewLanternListener(
		socket,
		netCfg,
		provider.TLSConfig{},
		provider.ObservabilityConfig{},
		provider.CORSConfig{},
		wire.server.svc,
		wire.server.rep,
		provider.NewValidationInterceptor(defaultIntegrationValidationLimits()),
		nil,
		provider.NewAuthInterceptor(provider.AuthConfig{Tokens: []string{token}}),
		nil,
		nil,
		provider.NewSlowRPCInterceptor(0, logger),
		provider.NewHealthChecker(),
		logger,
	)
	if err != nil {
		_ = socket.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- listener.Server().Serve(listener.Listener()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := listener.Server().Shutdown(ctx); err != nil {
			t.Errorf("shutdown receipt listener: %v", err)
		}
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve receipt listener: %v", err)
		}
	})
	url := "http://" + listener.Addr()
	wire.server = &connectTestServer{svc: wire.server.svc, rep: wire.server.rep, url: url}
	wire.raw = graphv1connect.NewLanternServiceClient(h2cClient(), url)
	return wire
}

func TestCompressionPreservesAuthenticatedReceiptsAndPeerRelay_RealWire(t *testing.T) {
	const token = "receipt-compression-test-token"
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	netCfg := provider.NetConfig{MaxRecvMsgBytes: 8 << 10, MaxSendMsgBytes: 4 << 10}
	origin := newProviderReceiptWireServer(t, hlc.NodeID{0xa1}, netCfg, token)
	follower := newProviderReceiptWireServer(t, hlc.NodeID{0xb2}, netCfg, token)

	if _, err := origin.raw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless capability = %v, want Unauthenticated", err)
	}
	capability := publicReceiptCapability(t, origin, token)
	receiptContext := publicReceiptWireContext(t, capability, 0xc3, 1,
		time.UnixMilli(int64(capability.GetServerNowUnixMs())).Add(-time.Second))
	small := &pb.Vertex{Key: "compression/small", Value: &pb.Vertex_String_{String_: "s"}}
	if result, err := origin.raw.PutVertex(ctx, receiptRequestWithToken(
		&pb.PutVertexRequest{Vertex: small}, token,
	)); err != nil || result.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("small PutVertex = (%v, %v)", result, err)
	}
	large := &pb.Vertex{Key: "compression/large", Value: &pb.Vertex_Bytes{
		Bytes: bytes.Repeat([]byte{'a'}, 1500),
	}}
	if result, err := origin.raw.PutVertex(ctx, receiptRequestWithToken(
		&pb.PutVertexRequest{Vertex: large, ReceiptContext: receiptContext}, token,
	)); err != nil || result.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("receipt PutVertex = (%v, %v)", result, err)
	}

	statuses := receiptWireStatuses(t, ctx, origin, token, receiptContext.GetOperationIds())
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
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" {
		t.Fatalf("small authenticated status HTTP %d, encoding %q: %s",
			response.StatusCode, response.Header.Get("Content-Encoding"), body)
	}
	var rawStatus pb.GetReceiptStatusesResponse
	if err := proto.Unmarshal(body, &rawStatus); err != nil {
		t.Fatal(err)
	}
	if proto.Size(&rawStatus) >= 1024 ||
		!proto.Equal(&rawStatus, &pb.GetReceiptStatusesResponse{Statuses: statuses}) {
		t.Fatalf("small authenticated status = %v, want original receipt under 1024 bytes", &rawStatus)
	}

	replicationClient := graphv1connect.NewLanternReplicationServiceClient(h2cClient(), origin.server.url)
	subscribe := &pb.SubscribeRequest{FromLocalSeq: 1, AcceptReceiptEnvelopes: true}
	unauthenticated, err := replicationClient.Subscribe(ctx, connect.NewRequest(subscribe))
	if err == nil {
		_ = unauthenticated.Receive()
		err = unauthenticated.Err()
		_ = unauthenticated.Close()
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless Subscribe = %v, want Unauthenticated", err)
	}
	stream, err := replicationClient.Subscribe(ctx, receiptRequestWithToken(subscribe, token))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
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

	stopPump := startPublicReceiptPump(t, ctx, "compressed provider relay", follower, origin, token)
	defer stopPump()
	wantCut := map[string]uint64{hex.EncodeToString(origin.config.NodeID[:]): 2}
	requireExactReceiptCut(t, "compressed provider relay", waitForPublicReceiptCut(
		t, ctx, "compressed provider relay", follower, token, wantCut, 5*time.Second,
	), wantCut)
	relayed := receiptWireStatuses(t, ctx, follower, token, receiptContext.GetOperationIds())
	if relayed[0].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		!bytes.Equal(relayed[0].GetOperationId(), original.GetOperationId()) ||
		!bytes.Equal(relayed[0].GetReceipt().GetIntentSha256(), original.GetIntentSha256()) ||
		!proto.Equal(relayed[0].GetReceipt(), original) {
		t.Fatalf("relayed receipt = %+v, want %+v", relayed[0], original)
	}
	readBack, err := follower.raw.GetVertex(ctx, receiptRequestWithToken(
		&pb.GetVertexRequest{Key: large.GetKey()}, token,
	))
	if err != nil || !bytes.Equal(readBack.Msg.GetVertex().GetBytes(), large.GetBytes()) {
		t.Fatalf("relayed large vertex = (%v, %v)", readBack, err)
	}

	boundedCfg := provider.NetConfig{MaxRecvMsgBytes: 8 << 10, MaxSendMsgBytes: 1024}
	bounded := newProviderReceiptWireServer(t, hlc.NodeID{0xd4}, boundedCfg, token)
	boundedCapability := publicReceiptCapability(t, bounded, token)
	boundedContext := publicReceiptWireContext(t, boundedCapability, 0xe5, 1,
		time.UnixMilli(int64(boundedCapability.GetServerNowUnixMs())).Add(-time.Second))
	_, err = bounded.raw.PutVertex(ctx, receiptRequestWithToken(
		&pb.PutVertexRequest{Vertex: large, ReceiptContext: boundedContext}, token,
	))
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
	if _, err := bounded.raw.GetVertex(ctx, receiptRequestWithToken(
		&pb.GetVertexRequest{Key: large.GetKey()}, token,
	)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("rejected receipt left vertex: %v", err)
	}
}
