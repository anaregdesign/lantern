package dataruntime

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The public operation entry point owns admission and mapping independently of
// transport. Connect boxing and actual h2c forwarding are tested in service.
func TestPublicAPI_PutAndGetVertexRoundTrip(t *testing.T) {
	svc := NewExecutor(newFakeBackend())
	client := svc.Public()
	ctx := context.Background()

	const key = "users/42"
	const value = "hello-connect"

	if _, err := client.PutVertex(ctx, &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: value}},
	}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	getResp, err := client.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	got := getResp.GetVertex()
	if got == nil {
		t.Fatal("GetVertex: nil vertex")
	}
	if got.Key != key {
		t.Errorf("key = %q, want %q", got.Key, key)
	}
	if s := got.GetString_(); s != value {
		t.Errorf("value = %q, want %q", s, value)
	}
}

func TestPublicAPI_GetServerStatus(t *testing.T) {
	svc := NewExecutor(newFakeBackend())
	client := svc.Public()
	ctx := context.Background()

	resp, err := client.GetServerStatus(ctx, &pb.GetServerStatusRequest{})
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}
	if resp == nil {
		t.Fatal("GetServerStatus: nil response")
	}
}

func TestPublicAPI_FaultedReceiptRuntimeFailsClosed(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	client := svc.Public()
	ctx := context.Background()

	capability, err := client.GetReceiptCapability(
		ctx,
		&pb.GetReceiptCapabilityRequest{},
	)
	if err != nil || !capability.GetEnabled() {
		t.Fatalf("initial capability = %+v, %v", capability, err)
	}
	operationID, err := mutationreceipt.NewID(
		runtime.receipt.epoch,
		time.UnixMilli(int64(capability.GetServerNowUnixMs())).Add(-time.Second),
		[24]byte{0x5a},
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptContext := &pb.MutationReceiptContext{
		OperationIds:  [][]byte{operationID.Bytes()},
		LogicalCallId: []byte{0x6b, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Endpoint:      capability.GetEndpoint(),
	}
	if _, err := client.PutEdge(ctx, &pb.PutEdgeRequest{
		Edge: &pb.Edge{
			Tail:       "faulted",
			Head:       "protected",
			Weight:     1,
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		},
	}); err != nil {
		t.Fatal(err)
	}

	svc.replicationCutMu.Lock()
	svc.markReceiptCommitFaultLocked()
	svc.replicationCutMu.Unlock()

	capability, err = client.GetReceiptCapability(
		ctx,
		&pb.GetReceiptCapabilityRequest{},
	)
	if err != nil || capability.GetEnabled() || capability.GetPolicy() != nil ||
		capability.GetEndpoint() != nil {
		t.Fatalf("faulted capability = %+v, %v", capability, err)
	}
	if _, err := client.GetReceiptStatus(ctx, &pb.GetReceiptStatusRequest{
		OperationId: operationID.Bytes(),
	}); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("faulted status = %v, want Internal", err)
	}
	if _, err := client.DeleteEdge(ctx, &pb.DeleteEdgeRequest{
		Tail: "faulted", Head: "protected", ReceiptContext: receiptContext,
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("faulted receipt mutation = %v, want FailedPrecondition", err)
	}
	if _, _, ok := runtime.GraphCache().GetEdgeDetail("faulted", "protected"); !ok {
		t.Fatal("faulted receipt mutation changed the graph")
	}
}

type postCaptureWriteMetrics struct {
	noopHotPathMetrics
	once  sync.Once
	write func()
}

func (m *postCaptureWriteMetrics) OnGetVertices(int, int) { m.once.Do(m.write) }

func TestPublicAPI_PostCaptureWriteKeepsDetachedRead(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{})
	defer func() { _ = log.Close() }()
	ctx := context.Background()
	svc := NewExecutor(newFakeBackend()).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	before := &pb.Vertex{Key: "k", Value: &pb.Vertex_String_{String_: "before"}}
	if _, err := svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: before}); err != nil {
		t.Fatal(err)
	}
	var writeErr error
	svc.WithHotPathMetrics(&postCaptureWriteMetrics{write: func() {
		_, writeErr = svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: "k", Value: &pb.Vertex_String_{String_: "after"},
		}})
	}})
	handler := svc.Public()
	read, err := handler.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{"k"}})
	if writeErr != nil || err != nil {
		t.Fatalf("write after detached capture = %v, GetVertices = %v", writeErr, err)
	}
	if len(read.GetVertices()) != 1 || read.GetVertices()[0].GetString_() != "before" {
		t.Fatalf("post-capture publication altered detached read: %+v", read)
	}
	got, err := svc.GetVertex(ctx, &pb.GetVertexRequest{Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetVertex().GetString_() != "after" {
		t.Fatalf("latest committed value = %+v, want after", got.GetVertex())
	}
}

type blockingVertexReadBackend struct {
	Backend
	entered chan struct{}
	release chan struct{}
}

func (b *blockingVertexReadBackend) GetVertex(key string) (*pb.Vertex, bool) {
	select {
	case <-b.entered:
	default:
		close(b.entered)
		<-b.release
	}
	return b.Backend.GetVertex(key)
}

func TestPublicAPI_GetVerticesBatchRetriesWithoutBlockingPublication(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{})
	defer func() { _ = log.Close() }()
	backend := &blockingVertexReadBackend{
		Backend: newFakeBackend(), entered: make(chan struct{}), release: make(chan struct{}),
	}
	defer func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	}()
	svc := NewExecutor(backend).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	handler := svc.Public()
	type readResult struct {
		response *pb.GetVerticesResponse
		err      error
	}
	readDone := make(chan readResult, 1)
	go func() {
		response, err := handler.GetVertices(context.Background(), &pb.GetVerticesRequest{Keys: []string{"a", "b"}})
		readDone <- readResult{response, err}
	}()
	<-backend.entered
	writerDone := make(chan struct{})
	go func() {
		completeTestPublicationInterval(t, svc)
		close(writerDone)
	}()
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("plural read blocked publication")
	}
	close(backend.release)
	select {
	case result := <-readDone:
		if result.err != nil || result.response == nil ||
			len(result.response.GetVertices()) != 0 ||
			len(result.response.GetMissing()) != 2 ||
			result.response.GetMissing()[0] != "a" || result.response.GetMissing()[1] != "b" {
			t.Fatalf("retried batch read = %+v, %v; want each missing key once", result.response, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plural read did not finish")
	}
}

type blockingPrefixCountBackend struct {
	Backend
	entered chan struct{}
	release chan struct{}
}

func (b *blockingPrefixCountBackend) CountByPrefixContext(ctx context.Context, prefix string) int {
	select {
	case <-b.entered:
	default:
		close(b.entered)
		<-b.release
	}
	return b.Backend.CountByPrefixContext(ctx, prefix)
}

func TestPublicAPI_PrefixCountRetriesWithoutBlockingPublication(t *testing.T) {
	cases := []struct {
		name string
		read func(*PublicAPI) error
	}{
		{"CountVerticesByPrefix", func(h *PublicAPI) error {
			_, err := h.CountVerticesByPrefix(context.Background(), &pb.CountVerticesByPrefixRequest{Prefix: "a"})
			return err
		}},
		{"DeleteVerticesByPrefixDryRun", func(h *PublicAPI) error {
			_, err := h.DeleteVerticesByPrefix(context.Background(), &pb.DeleteVerticesByPrefixRequest{Prefix: "a", DryRun: true})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := mutationlog.New(mutationlog.Options{})
			defer func() { _ = log.Close() }()
			backend := &blockingPrefixCountBackend{
				Backend: newFakeBackend(), entered: make(chan struct{}), release: make(chan struct{}),
			}
			defer func() {
				select {
				case <-backend.release:
				default:
					close(backend.release)
				}
			}()
			svc := NewExecutor(backend).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
			handler := svc.Public()
			readDone := make(chan error, 1)
			go func() { readDone <- tc.read(handler) }()
			select {
			case <-backend.entered:
			case err := <-readDone:
				t.Fatalf("prefix count skipped the blocking Backend probe: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("prefix count did not enter the blocking Backend probe")
			}
			writerDone := make(chan struct{})
			go func() {
				completeTestPublicationInterval(t, svc)
				close(writerDone)
			}()
			select {
			case <-writerDone:
			case <-time.After(2 * time.Second):
				t.Fatal("prefix count blocked publication")
			}
			close(backend.release)
			select {
			case err := <-readDone:
				if err != nil {
					t.Fatalf("retried prefix count: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("prefix count did not finish")
			}
		})
	}
}

// BenchmarkPublicAPI_GetEdgesPublicationCut isolates the read-side
// publication gate cost at the typed public operation entry point.
func BenchmarkPublicAPI_GetEdgesPublicationCut(b *testing.B) {
	for _, replicated := range []bool{false, true} {
		name := "single-node"
		if replicated {
			name = "replicated"
		}
		for _, parallel := range []bool{false, true} {
			mode := "serial"
			if parallel {
				mode = "parallel"
			}
			b.Run(name+"/"+mode, func(b *testing.B) {
				cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
				cache.PutEdgeWithExpiration("bench/tail", "bench/head", 1, time.Now().Add(time.Hour))
				svc := NewExecutor(cache)
				if replicated {
					log := mutationlog.New(mutationlog.Options{})
					b.Cleanup(func() { _ = log.Close() })
					svc.WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
				}
				handler := svc.Public()
				req := &pb.GetEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "bench/tail", Head: "bench/head"}}}
				ctx := context.Background()
				read := func() {
					if _, err := handler.GetEdges(ctx, req); err != nil {
						b.Error(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				if parallel {
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							read()
						}
					})
				} else {
					for i := 0; i < b.N; i++ {
						read()
					}
				}
			})
		}
	}
}
