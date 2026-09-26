package provider

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newMessageLimitedListener(
	t *testing.T,
	netCfg NetConfig,
	primary *service.LanternService,
	replication *service.LanternReplicationService,
) (string, *http.Client) {
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
	httpClient := &http.Client{Transport: &http.Transport{Protocols: protocols, DisableCompression: true}}
	t.Cleanup(httpClient.CloseIdleConnections)
	return "http://" + server.Addr(), httpClient
}

func newMessageLimitedClients(
	t *testing.T,
	netCfg NetConfig,
	primary *service.LanternService,
	replication *service.LanternReplicationService,
) (graphv1connect.LanternServiceClient, graphv1connect.LanternReplicationServiceClient) {
	t.Helper()
	baseURL, httpClient := newMessageLimitedListener(t, netCfg, primary, replication)
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

func sizedGetVertex(t *testing.T, key string, size int, random bool) *pb.Vertex {
	t.Helper()
	payload := bytes.Repeat([]byte{'a'}, size)
	if random {
		if _, err := rand.Read(payload); err != nil {
			t.Fatal(err)
		}
	}
	vertex := &pb.Vertex{
		Key:        key,
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	}
	for n := size; n >= 0; n-- {
		vertex.Value = &pb.Vertex_Bytes{Bytes: payload[:n]}
		if proto.Size(&pb.GetVertexResponse{Vertex: vertex}) == size {
			return vertex
		}
	}
	t.Fatalf("cannot construct %d-byte GetVertex response", size)
	return nil
}

func readGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func rawGetVertex(
	t *testing.T,
	httpClient *http.Client,
	baseURL, key string,
	acceptGzip bool,
) (*pb.GetVertexResponse, string, int) {
	t.Helper()
	data, err := proto.Marshal(&pb.GetVertexRequest{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		baseURL+graphv1connect.LanternServiceGetVertexProcedure, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	if acceptGzip {
		request.Header.Set("Accept-Encoding", "gzip")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GetVertex(%q) HTTP %d: %s", key, response.StatusCode, body)
	}
	encoding := response.Header.Get("Content-Encoding")
	size := len(body)
	if encoding == "gzip" {
		body = readGzip(t, body)
	}
	message := new(pb.GetVertexResponse)
	if err := proto.Unmarshal(body, message); err != nil {
		t.Fatal(err)
	}
	return message, encoding, size
}

func TestLanternListenerCompressionThresholdAndSendCaps(t *testing.T) {
	for _, tc := range []struct {
		cap   int
		sizes []int
	}{
		{cap: 0, sizes: []int{1023, 1024, 1025}},
		{cap: 128, sizes: []int{127, 128, 129}},
		{cap: 1024, sizes: []int{1023, 1024, 1025}},
		{cap: 2048, sizes: []int{1023, 1024, 1025}},
	} {
		t.Run(fmt.Sprintf("send cap %d", tc.cap), func(t *testing.T) {
			cache, primary, replication := newMessageLimitServices(t)
			baseURL, httpClient := newMessageLimitedListener(t, NetConfig{
				MaxRecvMsgBytes: 4 << 10,
				MaxSendMsgBytes: tc.cap,
			}, primary, replication)
			client := graphv1connect.NewLanternServiceClient(httpClient, baseURL)
			for _, size := range tc.sizes {
				for _, random := range []bool{false, true} {
					name := fmt.Sprintf("%d bytes/random=%t", size, random)
					t.Run(name, func(t *testing.T) {
						key := fmt.Sprintf("compression/%d/%d/%t", tc.cap, size, random)
						vertex := sizedGetVertex(t, key, size, random)
						if err := cache.PutVertex(key, vertex); err != nil {
							t.Fatal(err)
						}
						response, err := client.GetVertex(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: key}))
						// Connect checks the compressed size against the send cap when
						// gzip is used: incompressible messages at the cap fail, but
						// compressible messages above it may still fit.
						if tc.cap > 0 && size >= tc.cap && random {
							if connect.CodeOf(err) != connect.CodeResourceExhausted {
								t.Fatalf("GetVertex(%d bytes) = %v, want ResourceExhausted", size, err)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						wantEncoding := ""
						if size >= 1024 || (tc.cap > 0 && size >= tc.cap) {
							wantEncoding = "gzip"
						}
						raw, encoding, wireSize := rawGetVertex(t, httpClient, baseURL, key, true)
						if encoding != wantEncoding || !proto.Equal(raw, response.Msg) ||
							proto.Size(raw) != size || !bytes.Equal(raw.GetVertex().GetBytes(), vertex.GetBytes()) {
							t.Fatalf("GetVertex(%d bytes): encoding=%q, wire=%d, proto=%d, response=%v",
								size, encoding, wireSize, proto.Size(raw), raw)
						}
						if tc.cap > 0 && wireSize > tc.cap {
							t.Fatalf("GetVertex(%d bytes): wire size %d exceeds send cap %d", size, wireSize, tc.cap)
						}
					})
				}
			}
			if tc.cap == 0 || tc.cap == 2048 {
				key := fmt.Sprintf("compression/%d/1025/false", tc.cap)
				raw, encoding, _ := rawGetVertex(t, httpClient, baseURL, key, false)
				if encoding != "" || proto.Size(raw) != 1025 {
					t.Fatalf("unnegotiated GetVertex = %q, %d bytes, want raw 1025 bytes", encoding, proto.Size(raw))
				}
			} else {
				noGzip := graphv1connect.NewLanternServiceClient(httpClient, baseURL,
					connect.WithAcceptCompression("gzip", nil, nil))
				key := fmt.Sprintf("compression/%d/%d/false", tc.cap, tc.cap+1)
				_, err := noGzip.GetVertex(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: key}))
				if connect.CodeOf(err) != connect.CodeResourceExhausted {
					t.Fatalf("unnegotiated GetVertex above send cap = %v, want ResourceExhausted", err)
				}
			}
		})
	}
}

func TestLanternListenerSubscribeCompressionPerFrame(t *testing.T) {
	_, primary, replication := newMessageLimitServices(t)
	baseURL, httpClient := newMessageLimitedListener(t, NetConfig{
		MaxRecvMsgBytes: 4 << 10,
		MaxSendMsgBytes: 4 << 10,
	}, primary, replication)
	client := graphv1connect.NewLanternServiceClient(httpClient, baseURL)
	for _, vertex := range []*pb.Vertex{
		{Key: "small", Value: &pb.Vertex_String_{String_: "s"}},
		{Key: "large", Value: &pb.Vertex_Bytes{Bytes: bytes.Repeat([]byte{'a'}, 1500)}},
	} {
		if _, err := client.PutVertex(t.Context(), connect.NewRequest(&pb.PutVertexRequest{Vertex: vertex})); err != nil {
			t.Fatal(err)
		}
	}
	for _, protocol := range []struct {
		name         string
		contentType  string
		acceptHeader string
		options      []connect.ClientOption
	}{
		{name: "Connect", contentType: "application/connect+proto", acceptHeader: "Connect-Accept-Encoding"},
		{name: "gRPC", contentType: "application/grpc", acceptHeader: "Grpc-Accept-Encoding", options: []connect.ClientOption{connect.WithGRPC()}},
		{name: "gRPC-Web", contentType: "application/grpc-web+proto", acceptHeader: "Grpc-Accept-Encoding", options: []connect.ClientOption{connect.WithGRPCWeb()}},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := proto.Marshal(&pb.SubscribeRequest{FromLocalSeq: 1})
			if err != nil {
				t.Fatal(err)
			}
			envelope := make([]byte, 5+len(data))
			binary.BigEndian.PutUint32(envelope[1:5], uint32(len(data)))
			copy(envelope[5:], data)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost,
				baseURL+graphv1connect.LanternReplicationServiceSubscribeProcedure, bytes.NewReader(envelope))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", protocol.contentType)
			request.Header.Set(protocol.acceptHeader, "gzip")
			request.Header.Set("Accept-Encoding", "identity")
			if protocol.name == "Connect" {
				request.Header.Set("Connect-Protocol-Version", "1")
			} else if protocol.name == "gRPC" {
				request.Header.Set("Te", "trailers")
			}
			response, err := httpClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("Subscribe HTTP %d", response.StatusCode)
			}
			for i, wantKey := range []string{"small", "large"} {
				var prefix [5]byte
				if _, err := io.ReadFull(response.Body, prefix[:]); err != nil {
					t.Fatalf("Subscribe frame %d prefix: %v", i, err)
				}
				length := binary.BigEndian.Uint32(prefix[1:5])
				if length > 8<<10 {
					t.Fatalf("Subscribe frame %d length = %d", i, length)
				}
				body := make([]byte, length)
				if _, err := io.ReadFull(response.Body, body); err != nil {
					t.Fatalf("Subscribe frame %d body: %v", i, err)
				}
				if wantFlag := byte(i); prefix[0] != wantFlag {
					t.Fatalf("Subscribe frame %d flag = %#x, want %#x", i, prefix[0], wantFlag)
				}
				if prefix[0] == 1 {
					body = readGzip(t, body)
				}
				var frame pb.SubscribeResponse
				if err := proto.Unmarshal(body, &frame); err != nil {
					t.Fatal(err)
				}
				entries := frame.GetMutation().GetOp().GetReplicatedPutVertices().GetEntries()
				if len(entries) != 1 || entries[0].GetLive().GetKey() != wantKey {
					t.Fatalf("Subscribe frame %d = %v, want %q", i, &frame, wantKey)
				}
				if (proto.Size(&frame) < 1024) != (i == 0) {
					t.Fatalf("Subscribe frame %d = %d bytes, want %s 1024", i, proto.Size(&frame),
						[]string{"below", "at or above"}[i])
				}
			}
			typed := graphv1connect.NewLanternReplicationServiceClient(httpClient, baseURL, protocol.options...)
			stream, err := typed.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			for _, wantKey := range []string{"small", "large"} {
				if !stream.Receive() {
					t.Fatalf("typed Subscribe(%q): %v", wantKey, stream.Err())
				}
				entries := stream.Msg().GetMutation().GetOp().GetReplicatedPutVertices().GetEntries()
				if len(entries) != 1 || entries[0].GetLive().GetKey() != wantKey {
					t.Fatalf("typed Subscribe = %v, want %q", stream.Msg(), wantKey)
				}
			}
		})
	}
}

func TestLanternListenerAcceptsCompressedRequests(t *testing.T) {
	cache, primary, replication := newMessageLimitServices(t)
	vertex := &pb.Vertex{Key: "inbound", Value: &pb.Vertex_String_{String_: "ok"},
		Expiration: timestamppb.New(time.Now().Add(time.Hour))}
	if err := cache.PutVertex(vertex.GetKey(), vertex); err != nil {
		t.Fatal(err)
	}
	baseURL, httpClient := newMessageLimitedListener(t, NetConfig{
		MaxRecvMsgBytes: 128,
		MaxSendMsgBytes: 0,
	}, primary, replication)
	oversized := &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "oversized", Value: &pb.Vertex_Bytes{Bytes: bytes.Repeat([]byte{'a'}, 1024)},
	}}
	raw, err := proto.Marshal(oversized)
	if err != nil {
		t.Fatal(err)
	}
	var compressedRequest bytes.Buffer
	writer := gzip.NewWriter(&compressedRequest)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 128 || compressedRequest.Len() >= 128 {
		t.Fatalf("oversized request: raw %d, gzip %d bytes, want only decoded size over 128",
			len(raw), compressedRequest.Len())
	}
	for _, protocol := range []struct {
		name    string
		options []connect.ClientOption
	}{
		{name: "Connect"},
		{name: "gRPC", options: []connect.ClientOption{connect.WithGRPC()}},
		{name: "gRPC-Web", options: []connect.ClientOption{connect.WithGRPCWeb()}},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			for _, compressed := range []bool{false, true} {
				options := append([]connect.ClientOption(nil), protocol.options...)
				if compressed {
					options = append(options, connect.WithSendGzip())
				}
				client := graphv1connect.NewLanternServiceClient(httpClient, baseURL, options...)
				response, err := client.GetVertex(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: vertex.GetKey()}))
				if err != nil || !proto.Equal(response.Msg.GetVertex(), vertex) {
					t.Fatalf("GetVertex compressed=%t = (%v, %v), want %v", compressed, response, err, vertex)
				}
				_, err = client.PutVertex(t.Context(), connect.NewRequest(oversized))
				if connect.CodeOf(err) != connect.CodeResourceExhausted {
					t.Fatalf("oversized PutVertex compressed=%t = %v, want ResourceExhausted", compressed, err)
				}
			}
		})
	}
	if _, ok := cache.GetVertex(oversized.GetVertex().GetKey()); ok {
		t.Fatal("oversized compressed request changed the graph")
	}
}

func TestLanternListenerRejectsNegativeConnectMessageLimits(t *testing.T) {
	for _, netCfg := range []NetConfig{
		{MaxRecvMsgBytes: -1, MaxSendMsgBytes: 1},
		{MaxRecvMsgBytes: 1, MaxSendMsgBytes: -1},
	} {
		t.Run(fmt.Sprintf("recv=%d/send=%d", netCfg.MaxRecvMsgBytes, netCfg.MaxSendMsgBytes), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			if server, err := NewLanternListener(
				listener,
				netCfg,
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
		})
	}
}
