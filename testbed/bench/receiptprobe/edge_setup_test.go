package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	graphv1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type edgeSetupFakeClient struct {
	receiptClient
	put func(context.Context, *connect.Request[graphv1.PutVerticesRequest]) (*connect.Response[graphv1.PutVerticesResponse], error)
	get func(context.Context, *connect.Request[graphv1.GetVerticesRequest]) (*connect.Response[graphv1.GetVerticesResponse], error)
}

func (client *edgeSetupFakeClient) PutVertices(ctx context.Context, request *connect.Request[graphv1.PutVerticesRequest]) (*connect.Response[graphv1.PutVerticesResponse], error) {
	return client.put(ctx, request)
}

func (client *edgeSetupFakeClient) GetVertices(ctx context.Context, request *connect.Request[graphv1.GetVerticesRequest]) (*connect.Response[graphv1.GetVerticesResponse], error) {
	return client.get(ctx, request)
}

type edgeSetupFixture struct {
	endpoints []receiptEndpoint
	vertices  map[string]*graphv1.Vertex
	puts      [3]int
	gets      [3]int
}

func newEdgeSetupFixture(t *testing.T) *edgeSetupFixture {
	t.Helper()
	fixture := &edgeSetupFixture{vertices: make(map[string]*graphv1.Vertex)}
	for node := range receiptReplicaCount {
		client := &edgeSetupFakeClient{}
		client.put = func(ctx context.Context, request *connect.Request[graphv1.PutVerticesRequest]) (*connect.Response[graphv1.PutVerticesResponse], error) {
			fixture.puts[node]++
			if request.Header().Get(authorizationHeader) != bearerPrefix+"test-token" ||
				request.Msg.GetReceiptContext() != nil || !request.Msg.GetIfAbsent() ||
				len(request.Msg.GetVertices()) == 0 || len(request.Msg.GetVertices()) > edgeSetupBatchSize {
				t.Fatal("seed did not use bounded authenticated ordinary plural Put")
			}
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Sub(time.Now()) > 2*time.Second {
				t.Fatal("seed did not preserve the per-RPC deadline")
			}
			outcomes := make([]graphv1.PutOutcome, len(request.Msg.GetVertices()))
			for i, vertex := range request.Msg.GetVertices() {
				if _, present := fixture.vertices[vertex.GetKey()]; present {
					t.Fatal("fixture attempted to seed a duplicate vertex")
				}
				fixture.vertices[vertex.GetKey()] = proto.Clone(vertex).(*graphv1.Vertex)
				outcomes[i] = graphv1.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE
			}
			return connect.NewResponse(&graphv1.PutVerticesResponse{Outcomes: outcomes}), nil
		}
		client.get = func(ctx context.Context, request *connect.Request[graphv1.GetVerticesRequest]) (*connect.Response[graphv1.GetVerticesResponse], error) {
			fixture.gets[node]++
			if request.Header().Get(authorizationHeader) != bearerPrefix+"test-token" ||
				len(request.Msg.GetKeys()) == 0 || len(request.Msg.GetKeys()) > edgeSetupBatchSize {
				t.Fatal("verification did not use a bounded authenticated plural Get")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("verification omitted its deadline")
			}
			response := &graphv1.GetVerticesResponse{}
			// Reverse order verifies matching by key rather than response index.
			for i := len(request.Msg.GetKeys()) - 1; i >= 0; i-- {
				key := request.Msg.GetKeys()[i]
				if vertex, present := fixture.vertices[key]; present {
					response.Vertices = append(response.Vertices, proto.Clone(vertex).(*graphv1.Vertex))
				} else {
					response.Missing = append(response.Missing, key)
				}
			}
			return connect.NewResponse(response), nil
		}
		fixture.endpoints = append(fixture.endpoints, receiptEndpoint{
			address: fmt.Sprintf("https://replica-%d.test:6380", node), client: client,
			capability: &graphv1.GetReceiptCapabilityResponse{
				Enabled: true,
				Policy: &graphv1.ReceiptPolicy{
					DeploymentEpoch: bytes.Repeat([]byte{0x42}, 16), Fingerprint: bytes.Repeat([]byte{0x52}, 32),
					RetentionMs: uint64(time.Hour / time.Millisecond), MaxEntries: 1000, MaxBytes: 16 << 20,
				},
				Endpoint:           &graphv1.ReceiptEndpoint{NodeId: bytes.Repeat([]byte{byte(node + 1)}, 16), Generation: []byte{3}},
				SupportedMutations: []graphv1.ReceiptMutationKind{graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_ADD_EDGE},
			},
			issuedAt: time.Now().Add(-time.Second).Truncate(time.Millisecond),
		})
	}
	return fixture
}

func edgeSetupTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func edgeSetupTestPhases() []edgeSetupPhase {
	return []edgeSetupPhase{{Phase: "warmup", Duration: time.Second, PairRPS: 260}, {Phase: "steady", Duration: 2 * time.Second, PairRPS: 260}}
}

func TestEdgeAddPhaseLimit(t *testing.T) {
	for _, test := range []struct {
		name     string
		duration time.Duration
		rps      int
		want     uint64
	}{
		{"one tick", time.Second, 1, 2},
		{"rounded interval", time.Second, 3, 4},
		{"short duration", time.Nanosecond, 1, 1},
		{"largest duration", time.Duration(math.MaxInt64), 1_000_000_000, uint64(math.MaxInt64) + 1},
		{"zero duration", 0, 1, 0},
		{"negative duration", -time.Second, 1, 0},
		{"zero rate", time.Second, 0, 0},
		{"negative rate", time.Second, -1, 0},
		{"zero interval", time.Second, 1_000_000_001, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit, err := edgeAddPhaseLimit(test.duration, test.rps)
			if (err != nil) != (test.want == 0) || limit != test.want {
				t.Fatalf("limit=%d, err=%v; want %d", limit, err, test.want)
			}
		})
	}
}

func TestPrepareEdgeAddSeedsBoundedOrdinaryVerticesAndLoadsBothPhases(t *testing.T) {
	fixture := newEdgeSetupFixture(t)
	originalIssued := fixture.endpoints[0].issuedAt
	manifest, err := prepareEdgeAdd(edgeSetupTestContext(t), fixture.endpoints, "test-token", 2*time.Second, edgeSetupTestPhases())
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.vertices) != 393 || fixture.puts != [3]int{4, 0, 0} || fixture.gets != [3]int{4, 4, 4} {
		t.Fatalf("vertices=%d puts=%v gets=%v", len(fixture.vertices), fixture.puts, fixture.gets)
	}
	if !fixture.endpoints[0].issuedAt.Equal(originalIssued) || !proto.Equal(fixture.vertices[edgeSetupHead], edgeSetupVertex(edgeSetupHead)) {
		t.Fatal("setup changed receipt authority time or omitted the shared live head")
	}
	warmupNonce, _ := edgeSetupNonce(manifest.Phases[0])
	steadyNonce, _ := edgeSetupNonce(manifest.Phases[1])
	if bytes.Equal(warmupNonce[:8], steadyNonce[:8]) {
		t.Fatal("phases share operation-ID randomness")
	}
	path := filepath.Join(t.TempDir(), "edge-fixture.json")
	if err := writeEdgeSetupManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode: %v, %v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte("test-token")) {
		t.Fatal("manifest contains authority or could not be read")
	}
	if err := writeEdgeSetupManifest(path, manifest); !errors.Is(err, os.ErrExist) {
		t.Fatalf("manifest overwrite was not rejected: %v", err)
	}
	for _, phase := range manifest.Phases {
		cfg := probeConfig{family: receiptEdgeAdd, phase: phase.Phase, token: "test-token", duration: time.Duration(phase.DurationNS), pairRPS: phase.PairRPS, requestTimeout: 2 * time.Second}
		nonce, maxOps, err := loadEdgeSetupManifest(edgeSetupTestContext(t), path, cfg, fixture.endpoints)
		if err != nil || maxOps != phase.MaxOps || hex.EncodeToString(nonce[:]) != phase.NonceHex {
			t.Fatalf("phase load max=%d err=%v", maxOps, err)
		}
		for sequence := uint64(1); sequence <= maxOps; sequence++ {
			operation, err := newReceiptOperation(&fixture.endpoints[0], receiptEdgeAdd, nonce, sequence, phase.Phase)
			if err != nil || fixture.vertices[operation.addEdgeRequest.GetEdge().GetTail()] == nil {
				t.Fatalf("unprepared dynamic tail at sequence %d: %v", sequence, err)
			}
		}
	}
	if fixture.puts != [3]int{4, 0, 0} || fixture.gets != [3]int{12, 12, 12} {
		t.Fatalf("load rewrote seeds or omitted fresh all-replica reads: puts=%v gets=%v", fixture.puts, fixture.gets)
	}
}

func TestPrepareEdgeAddWaitsOnlyForMissingWithinOriginalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newEdgeSetupFixture(t)
		client := fixture.endpoints[1].client.(*edgeSetupFakeClient)
		get := client.get
		var pending []string
		calls := 0
		client.get = func(ctx context.Context, request *connect.Request[graphv1.GetVerticesRequest]) (*connect.Response[graphv1.GetVerticesResponse], error) {
			calls++
			if calls == 1 {
				pending = append([]string(nil), request.Msg.GetKeys()[1:]...)
				return connect.NewResponse(&graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{edgeSetupVertex(request.Msg.GetKeys()[0])}, Missing: pending}), nil
			}
			if !reflect.DeepEqual(request.Msg.GetKeys(), pending) {
				t.Fatal("verification retried keys that were already confirmed")
			}
			return get(ctx, request)
		}
		phases := []edgeSetupPhase{{Phase: "warmup", Duration: time.Nanosecond, PairRPS: 1}, {Phase: "steady", Duration: time.Nanosecond, PairRPS: 1}}
		start := time.Now()
		if _, err := prepareEdgeAdd(edgeSetupTestContext(t), fixture.endpoints, "test-token", 2*time.Second, phases); err != nil || calls != 2 || time.Since(start) != 25*time.Millisecond {
			t.Fatalf("missing-only wait: calls=%d elapsed=%v err=%v", calls, time.Since(start), err)
		}

		fixture = newEdgeSetupFixture(t)
		client = fixture.endpoints[0].client.(*edgeSetupFakeClient)
		client.get = func(_ context.Context, request *connect.Request[graphv1.GetVerticesRequest]) (*connect.Response[graphv1.GetVerticesResponse], error) {
			return connect.NewResponse(&graphv1.GetVerticesResponse{Missing: request.Msg.GetKeys()}), nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		defer cancel()
		start = time.Now()
		if _, err := prepareEdgeAdd(ctx, fixture.endpoints, "test-token", 2*time.Second, phases); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 60*time.Millisecond {
			t.Fatalf("original setup deadline was not retained: elapsed=%v err=%v", time.Since(start), err)
		}
	})
}

func TestPrepareEdgeAddFailsClosedBeforeMeasurement(t *testing.T) {
	t.Run("bounded context required", func(t *testing.T) {
		fixture := newEdgeSetupFixture(t)
		if _, err := prepareEdgeAdd(context.Background(), fixture.endpoints, "test-token", 2*time.Second, edgeSetupTestPhases()); err == nil || fixture.puts != [3]int{} {
			t.Fatal("unbounded preparation was accepted")
		}
	})
	t.Run("single configured replica", func(t *testing.T) {
		fixture := newEdgeSetupFixture(t)
		fixture.endpoints = fixture.endpoints[:1]
		if _, err := prepareEdgeAdd(edgeSetupTestContext(t), fixture.endpoints, "test-token", 2*time.Second, edgeSetupTestPhases()); err != nil || fixture.gets != [3]int{4, 0, 0} {
			t.Fatalf("single local integration endpoint failed: gets=%v err=%v", fixture.gets, err)
		}
	})
	for _, test := range []struct {
		name string
		edit func(*edgeSetupFixture, []edgeSetupPhase)
	}{
		{"policy budget", func(f *edgeSetupFixture, _ []edgeSetupPhase) {
			for i := range f.endpoints {
				f.endpoints[i].capability.Policy.MaxEntries = 781
			}
		}},
		{"phase names", func(_ *edgeSetupFixture, p []edgeSetupPhase) { p[1].Phase = "warmup" }},
		{"duplicate node", func(f *edgeSetupFixture, _ []edgeSetupPhase) {
			f.endpoints[2].capability.Endpoint.NodeId = f.endpoints[1].capability.Endpoint.NodeId
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, phases := newEdgeSetupFixture(t), edgeSetupTestPhases()
			test.edit(fixture, phases)
			if _, err := prepareEdgeAdd(edgeSetupTestContext(t), fixture.endpoints, "test-token", 2*time.Second, phases); err == nil || fixture.puts != [3]int{} {
				t.Fatalf("invalid preparation reached writes: puts=%v err=%v", fixture.puts, err)
			}
		})
	}
	for _, outcome := range []graphv1.PutOutcome{graphv1.PutOutcome_PUT_OUTCOME_UNSPECIFIED, graphv1.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET, graphv1.PutOutcome_PUT_OUTCOME_EXPIRED, 99} {
		t.Run(fmt.Sprintf("put outcome %d", outcome), func(t *testing.T) {
			fixture := newEdgeSetupFixture(t)
			fixture.endpoints[0].client.(*edgeSetupFakeClient).put = func(_ context.Context, request *connect.Request[graphv1.PutVerticesRequest]) (*connect.Response[graphv1.PutVerticesResponse], error) {
				values := make([]graphv1.PutOutcome, len(request.Msg.GetVertices()))
				for i := range values {
					values[i] = outcome
				}
				return connect.NewResponse(&graphv1.PutVerticesResponse{Outcomes: values}), nil
			}
			if _, err := prepareEdgeAdd(edgeSetupTestContext(t), fixture.endpoints, "test-token", 2*time.Second, edgeSetupTestPhases()); err == nil || fixture.gets != [3]int{} {
				t.Fatal("invalid ordinary Put result was accepted")
			}
		})
	}
	for _, code := range []connect.Code{connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeUnavailable, connect.CodeDeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			fixture := newEdgeSetupFixture(t)
			calls := 0
			fixture.endpoints[0].client.(*edgeSetupFakeClient).get = func(context.Context, *connect.Request[graphv1.GetVerticesRequest]) (*connect.Response[graphv1.GetVerticesResponse], error) {
				calls++
				return nil, connect.NewError(code, errors.New("fixture read rejected"))
			}
			if _, err := prepareEdgeAdd(edgeSetupTestContext(t), fixture.endpoints, "test-token", 2*time.Second, edgeSetupTestPhases()); connect.CodeOf(err) != code || calls != 1 {
				t.Fatalf("RPC failure was retried: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestEdgeSetupMissingRejectsMalformedResponses(t *testing.T) {
	keys := []string{"a", "b"}
	for _, test := range []struct {
		name     string
		response *graphv1.GetVerticesResponse
	}{
		{"nil message", nil},
		{"length", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{edgeSetupVertex("a")}}},
		{"nil vertex", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{nil}, Missing: []string{"b"}}},
		{"unknown vertex", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{edgeSetupVertex("c")}, Missing: []string{"b"}}},
		{"duplicate vertex", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{edgeSetupVertex("a"), edgeSetupVertex("a")}}},
		{"wrong value", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{{Key: "a", Value: &graphv1.Vertex_String_{String_: "wrong"}}}, Missing: []string{"b"}}},
		{"wrong type", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{{Key: "a", Value: &graphv1.Vertex_Bytes{Bytes: []byte(edgeSetupValue)}}}, Missing: []string{"b"}}},
		{"expiration", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{{Key: "a", Value: &graphv1.Vertex_String_{String_: edgeSetupValue}, Expiration: timestamppb.New(time.Unix(1, 0))}}, Missing: []string{"b"}}},
		{"overlap", &graphv1.GetVerticesResponse{Vertices: []*graphv1.Vertex{edgeSetupVertex("a")}, Missing: []string{"a"}}},
		{"duplicate missing", &graphv1.GetVerticesResponse{Missing: []string{"a", "a"}}},
		{"unknown missing", &graphv1.GetVerticesResponse{Missing: []string{"a", "c"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var response *connect.Response[graphv1.GetVerticesResponse]
			if test.response != nil {
				response = connect.NewResponse(test.response)
			}
			if _, err := edgeSetupMissing(keys, response); err == nil {
				t.Fatal("malformed verification was accepted")
			}
		})
	}
}

func TestLoadEdgeSetupManifestRejectsTamperingAndRechecksLiveState(t *testing.T) {
	fixture := newEdgeSetupFixture(t)
	manifest, err := prepareEdgeAdd(edgeSetupTestContext(t), fixture.endpoints, "test-token", 2*time.Second, edgeSetupTestPhases())
	if err != nil {
		t.Fatal(err)
	}
	valid, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cfg := probeConfig{family: receiptEdgeAdd, phase: "steady", token: "test-token", duration: 2 * time.Second, pairRPS: 260, requestTimeout: 2 * time.Second}
	for _, test := range []struct {
		name string
		edit func(*edgeSetupManifest, *probeConfig)
	}{
		{"rate", func(_ *edgeSetupManifest, c *probeConfig) { c.pairRPS++ }},
		{"duration", func(_ *edgeSetupManifest, c *probeConfig) { c.duration++ }},
		{"phase", func(_ *edgeSetupManifest, c *probeConfig) { c.phase = "unknown" }},
		{"family", func(_ *edgeSetupManifest, c *probeConfig) { c.family = receiptVertexPut }},
		{"max operations", func(m *edgeSetupManifest, _ *probeConfig) { m.Phases[1].MaxOps++ }},
		{"tail count", func(m *edgeSetupManifest, _ *probeConfig) { m.Phases[1].TailCount++ }},
		{"shared nonce", func(m *edgeSetupManifest, _ *probeConfig) { m.Phases[1].NonceHex = m.Phases[0].NonceHex }},
		{"policy binding", func(m *edgeSetupManifest, _ *probeConfig) { m.Binding = strings.Repeat("0", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var altered edgeSetupManifest
			if err := json.Unmarshal(valid, &altered); err != nil {
				t.Fatal(err)
			}
			loadCfg := cfg
			test.edit(&altered, &loadCfg)
			data, err := json.Marshal(altered)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "fixture.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			before := fixture.gets
			if _, _, err := loadEdgeSetupManifest(edgeSetupTestContext(t), path, loadCfg, fixture.endpoints); err == nil || fixture.gets != before {
				t.Fatalf("tampered manifest reached reads: gets=%v err=%v", fixture.gets, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := writeEdgeSetupManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadEdgeSetupManifest(edgeSetupTestContext(t), filepath.Dir(path), cfg, fixture.endpoints); err == nil {
		t.Fatal("nonregular manifest was accepted")
	}
	if _, _, err := loadEdgeSetupManifest(context.Background(), path, cfg, fixture.endpoints); err == nil {
		t.Fatal("unbounded manifest loading was accepted")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadEdgeSetupManifest(edgeSetupTestContext(t), path, cfg, fixture.endpoints); err == nil {
		t.Fatal("public manifest was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.vertices[edgeSetupHead].Value = &graphv1.Vertex_String_{String_: "changed"}
	if _, _, err := loadEdgeSetupManifest(edgeSetupTestContext(t), path, cfg, fixture.endpoints); err == nil {
		t.Fatal("manifest was treated as proof of current live values")
	}
	fixture.vertices[edgeSetupHead] = edgeSetupVertex(edgeSetupHead)
	fixture.endpoints[1].capability.Endpoint.Generation = []byte{4}
	if _, _, err := loadEdgeSetupManifest(edgeSetupTestContext(t), path, cfg, fixture.endpoints); err == nil {
		t.Fatal("changed endpoint generation was accepted")
	}
}

func TestOpenEdgeSetupManifestRejectsReplacedFIFOWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		file, err := openEdgeSetupManifest(path, expected)
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("replaced FIFO was accepted")
		}
	case <-time.After(2 * time.Second):
		// Rescue a regressed blocking reader, then join it before failing.
		writer, openErr := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if writer != nil {
			_ = writer.Close()
		}
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Errorf("manifest reader did not join after rescue: %v", openErr)
		}
		t.Fatal("manifest open waited for a FIFO writer")
	}
}
