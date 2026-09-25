package integration_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

func authedReceiptRequest[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+testToken)
	return req
}

// TestReceiptReadSurface_RealConnectWire keeps the dormant capability/status
// surface honest: authentication precedes receipt inspection, and a server
// without an atomic receipt engine cannot fabricate an absent result.
func TestReceiptReadSurface_RealConnectWire(t *testing.T) {
	srv, _, _ := newAuthedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	if _, err := raw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless capability = %v, want Unauthenticated", err)
	}
	if _, err := raw.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless status = %v, want Unauthenticated", err)
	}

	capability, err := raw.GetReceiptCapability(ctx, authedReceiptRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || capability.Msg.GetEnabled() || capability.Msg.GetPolicy() != nil || capability.Msg.GetEndpoint() != nil || capability.Msg.GetServerNowUnixMs() != 0 {
		t.Fatalf("authenticated disabled capability = (%v, %v)", capability, err)
	}
	operationID := make([]byte, 49)
	for i := range operationID {
		operationID[i] = byte(i + 1)
	}
	if _, err := raw.GetReceiptStatus(ctx, authedReceiptRequest(&pb.GetReceiptStatusRequest{OperationId: operationID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled singular status = %v, want FailedPrecondition", err)
	}
	if _, err := raw.GetReceiptStatuses(ctx, authedReceiptRequest(&pb.GetReceiptStatusesRequest{OperationIds: [][]byte{operationID}})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled plural status = %v, want FailedPrecondition", err)
	}

	// Auth can be disabled for ordinary Lantern deployments. The dormant
	// preflight still cannot expose a continuity marker in that configuration.
	open := newConnectTestServer(t, service.NewLanternService(nil), nil)
	openRaw := graphv1connect.NewLanternServiceClient(h2cClient(), open.url)
	openCapability, err := openRaw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || openCapability.Msg.GetEnabled() || openCapability.Msg.GetPolicy() != nil || openCapability.Msg.GetEndpoint() != nil || openCapability.Msg.GetServerNowUnixMs() != 0 {
		t.Fatalf("unauthenticated deployment capability = (%v, %v)", openCapability, err)
	}
	if _, err := openRaw.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{OperationId: operationID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unauthenticated deployment status = %v, want FailedPrecondition", err)
	}
}

func durableReceiptWireConfig(path string, nodeID hlc.NodeID) service.DurableReceiptWALRuntimeConfig {
	now := time.Now()
	return service.DurableReceiptWALRuntimeConfig{
		Path: path,
		Receipt: mutationreceipt.Config{
			Epoch:          mutationreceipt.Epoch{0x42},
			Retention:      time.Hour,
			MaxEntries:     32,
			MaxBytes:       1 << 20,
			ClockHighWater: now,
		},
		Log:        mutationlog.Options{Capacity: 16, SubscriberBuffer: 4},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			provider.ConfigureGraphCache(
				graph,
				provider.CacheConfig{TTL: time.Hour},
				provider.SearchConfig{},
			)
			return nil
		},
		NodeID: nodeID,
		Now:    now,
	}
}

func mountDurableReceiptWireRuntime(
	t *testing.T,
	runtime *service.ServingRuntime,
) (*connectTestServer, *client.Lantern) {
	t.Helper()
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(2 * time.Hour)
	replication, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	server := newConnectTestServer(t, primary, replication)
	sdk, err := client.NewLantern(server.url, client.WithHTTPClient(h2cClient()))
	if err != nil {
		t.Fatal(err)
	}
	return server, sdk
}

func closeDurableReceiptWireRuntime(
	t *testing.T,
	server *connectTestServer,
	sdk *client.Lantern,
	runtime *service.ServingRuntime,
) {
	t.Helper()
	if err := sdk.Close(); err != nil {
		t.Fatal(err)
	}
	server.srv.Close()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestDurableReceiptWALRuntime_RealConnectWireRestart is the external-surface
// gate for the private production runtime. Public writes traverse real h2c,
// shutdown releases every serving consumer before the runtime owner, and a
// same-generation restart reconstructs live, expired, and deleted state.
func TestDurableReceiptWALRuntime_RealConnectWireRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "receipts.wal")
	nodeID := hlc.NodeID{0x31}
	config := durableReceiptWireConfig(path, nodeID)

	fresh, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	server, sdk := mountDurableReceiptWireRuntime(t, fresh)
	for _, key := range []string{"durable/live", "durable/deleted"} {
		if outcome, err := sdk.PutVertex(ctx, key, key, time.Hour); err != nil ||
			outcome != client.PutOutcomeAppliedAndLive {
			closeDurableReceiptWireRuntime(t, server, sdk, fresh)
			t.Fatalf("PutVertex(%q) = (%v, %v)", key, outcome, err)
		}
	}
	expiresAt := time.Now().Add(500 * time.Millisecond)
	if outcome, err := sdk.PutVertexAt(ctx, "durable/expired", "expired", expiresAt); err != nil ||
		outcome != client.PutOutcomeAppliedAndLive {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatalf("PutVertexAt(expired) = (%v, %v)", outcome, err)
	}
	if existed, err := sdk.DeleteVertex(ctx, "durable/deleted"); err != nil || !existed {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatalf("DeleteVertex = (%v, %v), want true, nil", existed, err)
	}
	if length, _, evicted := fresh.MutationLogStats(); length != 4 || evicted != 0 {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatalf("fresh mutation Log = len %d evicted %d, want 4, 0", length, evicted)
	}
	generation, err := os.ReadFile(path + ".generation")
	if err != nil {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatal(err)
	}
	closeDurableReceiptWireRuntime(t, server, sdk, fresh)
	if wait := time.Until(expiresAt) + 10*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}

	clockBeforeRejectedRestart, err := os.ReadFile(path + ".clock")
	if err != nil {
		t.Fatal(err)
	}
	wrongNode := config
	wrongNode.NodeID[0] ^= 0xff
	wrongNode.Now = time.Now()
	wrongNode.Receipt.ClockHighWater = wrongNode.Now
	if runtime, err := service.OpenDurableReceiptWALServingRuntime(wrongNode); runtime != nil ||
		err == nil || !strings.Contains(err.Error(), "generation binding mismatch") {
		if runtime != nil {
			_ = runtime.Close()
		}
		t.Fatalf("changed-NodeID restart = %p, %v", runtime, err)
	}
	clockAfterRejectedRestart, err := os.ReadFile(path + ".clock")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(clockAfterRejectedRestart, clockBeforeRejectedRestart) {
		t.Fatal("changed-NodeID restart modified the clock journal before rejection")
	}

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := service.OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	restartedGeneration, err := os.ReadFile(path + ".generation")
	if err != nil {
		_ = restarted.Close()
		t.Fatal(err)
	}
	if !bytes.Equal(restartedGeneration, generation) {
		_ = restarted.Close()
		t.Fatal("same-epoch restart changed durable generation bytes")
	}
	restartedServer, restartedSDK := mountDurableReceiptWireRuntime(t, restarted)
	defer closeDurableReceiptWireRuntime(t, restartedServer, restartedSDK, restarted)

	vertex, err := restartedSDK.GetVertex(ctx, "durable/live")
	if err != nil {
		t.Fatalf("GetVertex(live): %v", err)
	}
	if value, err := client.StringValue(vertex); err != nil || value != "durable/live" {
		t.Fatalf("recovered live value = %q, %v", value, err)
	}
	for _, key := range []string{"durable/deleted", "durable/expired"} {
		if _, err := restartedSDK.GetVertex(ctx, key); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("GetVertex(%q) after restart = %v, want ErrNotFound", key, err)
		}
	}
	if length, _, evicted := restarted.MutationLogStats(); length != 4 || evicted != 0 {
		t.Fatalf("restarted mutation Log = len %d evicted %d, want 4, 0", length, evicted)
	}
	status, err := newReplicationRawClient(t, restartedServer.url).PeerStatus(
		ctx,
		connect.NewRequest(&pb.PeerStatusRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(status.Msg.GetSelfOrigin(), nodeID[:]) ||
		len(status.Msg.GetOrigins()) != 1 ||
		status.Msg.GetOrigins()[0].GetLastSeq() != 4 {
		t.Fatalf("restarted PeerStatus = %+v", status.Msg)
	}
}
