package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// These tests exercise the actual transport against the executor's public
// entry point. Private publication and WAL fault injection remain with the
// dataruntime owner, where no transport or test-only state export is needed.
func newConnectTestClients(
	t *testing.T,
	svc *LanternService,
	rep *LanternReplicationService,
	options ...connect.ClientOption,
) (graphv1connect.LanternServiceClient, graphv1connect.LanternReplicationServiceClient) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternServiceHandler(NewLanternServiceConnectHandler(svc)))
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(NewLanternReplicationServiceConnectHandler(rep)))
	srv := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = protocols
	srv.Start()
	t.Cleanup(srv.Close)

	clientProtocols := new(http.Protocols)
	clientProtocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: clientProtocols}}
	t.Cleanup(client.CloseIdleConnections)
	return graphv1connect.NewLanternServiceClient(client, srv.URL, options...),
		graphv1connect.NewLanternReplicationServiceClient(client, srv.URL, options...)
}

func TestBinaryProtobufContentType(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		binary      bool
	}{
		{"application/connect+proto", true},
		{"application/grpc+proto", true},
		{"application/grpc-web+proto", true},
		{"application/grpc-web-text+proto", false},
		{"application/grpc-web-text", false},
		{"application/connect+json", false},
		{"application/grpc+json", false},
		{"invalid content type", false},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			if got := binaryProtobufContentType(tc.contentType); got != tc.binary {
				t.Fatalf("binaryProtobufContentType(%q) = %t, want %t", tc.contentType, got, tc.binary)
			}
		})
	}
}

func TestConnectAdapter_PublicOperationsH2C(t *testing.T) {
	for _, namespaced := range []bool{false, true} {
		name := "unmapped"
		if namespaced {
			name = "namespaced"
		}
		t.Run(name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			cache.EnablePrefixIndex(func(key string) string { return key })
			svc := NewLanternService(cache).WithStatusInfo(StatusInfo{Version: "transport-test"})
			if namespaced {
				svc.WithDataNamespace()
			}
			client, _ := newConnectTestClients(t, svc, nil)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			request := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "users:42", Value: &pb.Vertex_String_{String_: "first"}},
				{Key: "users:43", Value: &pb.Vertex_String_{String_: "second"}},
			}}
			original := proto.Clone(request)
			put, err := client.PutVertices(ctx, connect.NewRequest(request))
			if err != nil || len(put.Msg.GetOutcomes()) != 2 ||
				put.Msg.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE ||
				put.Msg.GetOutcomes()[1] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
				t.Fatalf("PutVertices = %+v, %v", put, err)
			}
			if !proto.Equal(request, original) {
				t.Fatal("transport changed caller-owned request")
			}
			physical := "users:42"
			if namespaced {
				physical = "data:" + physical
			}
			if stored, ok := cache.GetVertex(physical); !ok || stored.GetKey() != physical {
				t.Fatalf("physical owner state = %+v, %t", stored, ok)
			}
			got, err := client.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "users:42"}))
			if err != nil || got.Msg.GetVertex().GetKey() != "users:42" || got.Msg.GetVertex().GetString_() != "first" {
				t.Fatalf("GetVertex = %+v, %v", got, err)
			}
			if _, err := client.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "missing"})); connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatalf("missing GetVertex = %v, want NotFound", err)
			}
			if _, err := client.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "users:invalid", Expiration: &timestamppb.Timestamp{Nanos: -1}}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid PutVertex expiration = %v, want InvalidArgument", err)
			}
			if _, err := client.MutateGraph(ctx, connect.NewRequest(&pb.MutateGraphRequest{Items: make([]*pb.GraphMutationItem, 65)})); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("oversized MutateGraph = %v, want InvalidArgument", err)
			}
			status, err := client.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
			if err != nil || status.Msg.GetVersion() != "transport-test" {
				t.Fatalf("GetServerStatus = %+v, %v", status, err)
			}
			stream, err := client.BackupSnapshot(ctx, connect.NewRequest(&pb.BackupSnapshotRequest{VertexPrefix: "users:"}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			vertices := make(map[string]string)
			for stream.Receive() {
				vertex := stream.Msg().GetVertex()
				if vertex == nil {
					t.Fatalf("unexpected backup frame = %+v", stream.Msg())
				}
				vertices[vertex.GetKey()] = vertex.GetString_()
			}
			if err := stream.Err(); err != nil || len(vertices) != 2 || vertices["users:42"] != "first" || vertices["users:43"] != "second" {
				t.Fatalf("BackupSnapshot logical frames = %+v, %v", vertices, err)
			}
		})
	}
}

func TestConnectAdapter_ReceiptForwardingH2C(t *testing.T) {
	now := time.Now()
	runtime, err := CreateDurableReceiptWALServingRuntime(DurableReceiptWALRuntimeConfig{
		Path: filepath.Join(t.TempDir(), "receipt.wal"),
		Receipt: mutationreceipt.Config{Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
			MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: now},
		Log: mutationlog.Options{Capacity: 16, SubscriberBuffer: 2}, DefaultTTL: time.Hour,
		ConfigureGraph: func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil },
		NodeID:         hlc.NodeID{0x31}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	svc := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewReplication(svc)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(svc, replication); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyReceiptBackup(svc, replication); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(svc, replication); err != nil {
		t.Fatal(err)
	}
	client, _ := newConnectTestClients(t, svc, replication)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	capability, err := client.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || !capability.Msg.GetEnabled() {
		t.Fatalf("GetReceiptCapability = %+v, %v", capability, err)
	}
	var epoch mutationreceipt.Epoch
	if len(capability.Msg.GetPolicy().GetDeploymentEpoch()) != len(epoch) {
		t.Fatal("capability omitted active epoch")
	}
	copy(epoch[:], capability.Msg.GetPolicy().GetDeploymentEpoch())
	id, err := mutationreceipt.NewID(epoch, time.UnixMilli(int64(capability.Msg.GetServerNowUnixMs())).Add(-time.Second), [24]byte{0x73})
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: "receipt:key", Value: &pb.Vertex_String_{String_: "once"}}, IfAbsent: true,
		ReceiptContext: &pb.MutationReceiptContext{OperationIds: [][]byte{id.Bytes()},
			LogicalCallId: bytes.Repeat([]byte{0x74}, 16), Endpoint: capability.Msg.GetEndpoint()},
	}
	for attempt := range 2 {
		put, err := client.PutVertex(ctx, connect.NewRequest(request))
		if err != nil || put.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
			t.Fatalf("receipt PutVertex attempt %d = %+v, %v", attempt, put, err)
		}
	}
	status, err := client.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{OperationId: id.Bytes()}))
	if err != nil || status.Msg.GetStatus().GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		status.Msg.GetStatus().GetReceipt().GetOriginalResult().GetPutVertexOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("GetReceiptStatus = %+v, %v", status, err)
	}
	if runtime.ReceiptStats().Entries != 1 || svc.LocalSeq(hlc.NodeID{0x31}) != 1 {
		t.Fatal("wire replay published a second receipt or origin event")
	}
}

func TestConnectAdapter_ReplicationDisabledH2C(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	_, client := newConnectTestClients(t, NewLanternService(cache), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := client.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("PeerStatus with replication disabled = %v, want Unavailable", err)
	}
	stream, err := client.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err == nil {
		for stream.Receive() {
			t.Fatal("disabled replication emitted Snapshot frame")
		}
		err = stream.Err()
		_ = stream.Close()
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Snapshot with replication disabled = %v, want Unavailable", err)
	}
}

func TestConnectAdapter_FullSubscribeRejectsJSONH2C(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x32}, hlc.Options{})
	svc := NewLanternService(cache).WithReplication(log, clock, nil)
	replication := NewLanternReplicationService(log, cache, clock)
	_, client := newConnectTestClients(t, svc, replication, connect.WithProtoJSON())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := client.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{}))
	if err == nil {
		for stream.Receive() {
			t.Fatal("JSON full Subscribe emitted mutation frame")
		}
		err = stream.Err()
		_ = stream.Close()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("JSON full Subscribe = %v, want InvalidArgument", err)
	}
}
