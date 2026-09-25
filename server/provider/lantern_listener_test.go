package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newMessageLimitedClients(
	t *testing.T,
	netCfg NetConfig,
	primary *service.LanternService,
	replication *service.LanternReplicationService,
) (graphv1connect.LanternServiceClient, graphv1connect.LanternReplicationServiceClient) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := NewLanternListener(
		listener,
		netCfg,
		TLSConfig{},
		ObservabilityConfig{},
		CORSConfig{},
		primary,
		replication,
		nil,
		nil,
		nil,
		nil,
		nil,
		NewSlowRPCInterceptor(0, logger),
		NewHealthChecker(),
		logger,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Server().Serve(server.Listener())
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Server().Shutdown(ctx); err != nil {
			t.Errorf("shutdown message-limited listener: %v", err)
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve message-limited listener: %v", err)
		}
	})

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	httpClient := &http.Client{Transport: &http.Transport{Protocols: protocols}}
	t.Cleanup(httpClient.CloseIdleConnections)
	baseURL := "http://" + server.Addr()
	return graphv1connect.NewLanternServiceClient(httpClient, baseURL),
		graphv1connect.NewLanternReplicationServiceClient(httpClient, baseURL)
}

func newMessageLimitServices(
	t *testing.T,
) (*graphcache.GraphCache[string, *pb.Vertex], *service.LanternService, *service.LanternReplicationService) {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x42}, hlc.Options{})
	primary := service.NewLanternService(cache).WithReplication(log, clock, nil)
	replication := service.NewLanternReplicationService(log, cache, clock).WithOriginStates(primary)
	return cache, primary, replication
}

func TestLanternListenerEnforcesConnectMessageLimits(t *testing.T) {
	t.Run("Lantern request read limit", func(t *testing.T) {
		_, primary, replication := newMessageLimitServices(t)
		client, _ := newMessageLimitedClients(t, NetConfig{
			MaxRecvMsgBytes: 128,
			MaxSendMsgBytes: 4 << 10,
		}, primary, replication)
		_, err := client.PutVertex(context.Background(), connect.NewRequest(&pb.PutVertexRequest{
			Vertex: &pb.Vertex{
				Key:        "oversized-request",
				Value:      &pb.Vertex_Bytes{Bytes: bytes.Repeat([]byte{1}, 1024)},
				Expiration: timestamppb.New(time.Now().Add(time.Hour)),
			},
		}))
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("oversized Lantern request = %v, want ResourceExhausted", err)
		}
	})

	t.Run("Lantern response send limit", func(t *testing.T) {
		cache, primary, replication := newMessageLimitServices(t)
		value := make([]byte, 1024)
		if _, err := rand.Read(value); err != nil {
			t.Fatal(err)
		}
		if err := cache.PutVertex("large-response", &pb.Vertex{
			Key:        "large-response",
			Value:      &pb.Vertex_Bytes{Bytes: value},
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		}); err != nil {
			t.Fatal(err)
		}
		client, _ := newMessageLimitedClients(t, NetConfig{
			MaxRecvMsgBytes: 4 << 10,
			MaxSendMsgBytes: 128,
		}, primary, replication)
		_, err := client.GetVertex(context.Background(), connect.NewRequest(&pb.GetVertexRequest{
			Key: "large-response",
		}))
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("oversized Lantern response = %v, want ResourceExhausted", err)
		}
	})

	t.Run("replication request read limit", func(t *testing.T) {
		_, primary, replication := newMessageLimitServices(t)
		_, client := newMessageLimitedClients(t, NetConfig{
			MaxRecvMsgBytes: 128,
			MaxSendMsgBytes: 4 << 10,
		}, primary, replication)
		request := &pb.PeerStatusRequest{}
		unknown := protowire.AppendTag(nil, 1000, protowire.BytesType)
		unknown = protowire.AppendBytes(unknown, bytes.Repeat([]byte{3}, 1024))
		request.ProtoReflect().SetUnknown(unknown)
		_, err := client.PeerStatus(context.Background(), connect.NewRequest(request))
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("oversized replication request = %v, want ResourceExhausted", err)
		}
	})

	t.Run("replication response send limit", func(t *testing.T) {
		_, primary, replication := newMessageLimitServices(t)
		_, client := newMessageLimitedClients(t, NetConfig{
			MaxRecvMsgBytes: 4 << 10,
			MaxSendMsgBytes: 8,
		}, primary, replication)
		_, err := client.PeerStatus(context.Background(), connect.NewRequest(&pb.PeerStatusRequest{}))
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("oversized replication response = %v, want ResourceExhausted", err)
		}
	})
}

func TestLanternListenerRejectsNegativeConnectMessageLimits(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if server, err := NewLanternListener(
		listener,
		NetConfig{MaxRecvMsgBytes: -1, MaxSendMsgBytes: 1},
		TLSConfig{},
		ObservabilityConfig{},
		CORSConfig{},
		service.NewLanternService(nil),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		NewSlowRPCInterceptor(0, logger),
		NewHealthChecker(),
		logger,
	); server != nil || err == nil {
		t.Fatalf("negative Connect message limit = %p, %v", server, err)
	}
}
