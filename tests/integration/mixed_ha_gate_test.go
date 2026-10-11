package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/backup"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// This fixture changes network reachability only. Product processes terminate
// mTLS and own every pump, anti-entropy, HLC, receipt, WAL and replay operation.
type mixedHAProxy struct {
	mu          sync.Mutex
	hosts       []string
	allowed     [3][3]bool
	connections map[net.Conn][2]int
	bytes       [3][3]int64
}

func newMixedHAProxy(t *testing.T, hosts []string) *mixedHAProxy {
	t.Helper()
	p := &mixedHAProxy{hosts: hosts, connections: make(map[net.Conn][2]int)}
	p.links(true)
	for target := range hosts {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", 17381+target))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		go func() {
			for {
				incoming, err := listener.Accept()
				if err != nil {
					return
				}
				go p.forward(incoming, target)
			}
		}()
	}
	t.Cleanup(func() { p.links(false) })
	return p
}

func (p *mixedHAProxy) forward(incoming net.Conn, target int) {
	defer func() { _ = incoming.Close() }()
	host, _, err := net.SplitHostPort(incoming.RemoteAddr().String())
	if err != nil {
		return
	}
	source := -1
	for i, known := range p.hosts {
		if host == known {
			source = i
		}
	}
	if source < 0 {
		return
	}
	p.mu.Lock()
	if !p.allowed[source][target] {
		p.mu.Unlock()
		return
	}
	p.connections[incoming] = [2]int{source, target}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.connections, incoming); p.mu.Unlock() }()
	outgoing, err := net.DialTimeout("tcp", net.JoinHostPort(p.hosts[target], "17380"), time.Second)
	if err != nil {
		return
	}
	defer func() { _ = outgoing.Close() }()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(outgoing, incoming); _ = outgoing.Close(); close(done) }()
	n, _ := io.Copy(incoming, outgoing)
	_ = incoming.Close()
	<-done
	p.mu.Lock()
	p.bytes[source][target] += n
	p.mu.Unlock()
}

func (p *mixedHAProxy) links(all bool, pairs ...[2]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.allowed {
		for j := range p.allowed[i] {
			p.allowed[i][j] = all && i != j
		}
	}
	for _, pair := range pairs {
		p.allowed[pair[0]][pair[1]], p.allowed[pair[1]][pair[0]] = true, true
	}
	for c, pair := range p.connections {
		if !p.allowed[pair[0]][pair[1]] {
			_ = c.Close()
		}
	}
}

type mixedHAOriginal struct {
	name     string
	origin   int
	identity client.GraphMutationReceiptContext
	vertices []client.VertexInput
	edges    []client.EdgeAddReceiptInput
	result   client.GraphMutationResult
	statuses []client.ReceiptStatus
}

type mixedHAGate struct {
	g             *custodyProcessGate
	proxy         *mixedHAProxy
	sdks          []*client.Lantern
	originals     []*mixedHAOriginal
	backupCuts    [3]uint64
	walOrigins    map[string]uint64
	cdcCursor     []byte
	extraVertices int
}

func newMixedHAGate(t *testing.T, g *custodyProcessGate, hosts []string) *mixedHAGate {
	t.Helper()
	m := &mixedHAGate{g: g, proxy: newMixedHAProxy(t, hosts)}
	for _, endpoint := range g.urls {
		sdk, err := client.NewLantern(endpoint, client.WithHTTPClient(g.http), client.WithAuthToken(g.machine))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sdk.Close() })
		m.sdks = append(m.sdks, sdk)
	}
	return m
}

func (m *mixedHAGate) phase(t *testing.T, phase string) {
	t.Helper()
	switch phase {
	case "mixed-relay-cdc":
		m.g.waitReady(0, 1, 2)
		m.proxy.links(false, [2]int{0, 1}, [2]int{1, 2})
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		changes := graphv1connect.NewLanternChangeServiceClient(m.g.http, m.g.urls[2])
		stream, err := changes.WatchChanges(ctx, securityWireRequest(m.g.machine, &pb.WatchChangesRequest{Prefix: "orders:", Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
		if err != nil || !stream.Receive() || !stream.Msg().Bootstrap {
			t.Fatal("CDC bootstrap", err)
		}
		defer func() { _ = stream.Close() }()
		original := m.write(t, 0, "relay", "orders:relay:", "initial", 2, false)
		m.await(t, original, 0, 1, 2)
		seen := make(map[string]bool)
		var cursor []byte
		for stream.Receive() {
			frame := stream.Msg()
			for _, invalidation := range frame.Invalidations {
				if key := invalidation.GetVertexKey(); key != "" {
					seen[key] = true
				}
				if edge := invalidation.GetEdgeKey(); edge != nil {
					seen[edge.Tail+"->"+edge.Head] = true
				}
			}
			if len(frame.Invalidations) > 0 && len(frame.Cursor) > 0 {
				if len(seen) != 3 {
					t.Fatal("cursor before complete visible mixed frame", seen)
				}
				cursor = append([]byte(nil), frame.Cursor...)
				break
			}
		}
		if len(cursor) == 0 {
			t.Fatal("mixed CDC not observed", stream.Err())
		}
		m.cdcCursor = cursor
		_ = stream.Close()
		m.snapshot(t, 2, "orders:relay:", 2, 1)
		m.proxy.links(true)
		// An explicit same-ID confirmation is permitted only after status. The
		// caller deliberately chooses it; the SDK never resends on its own.
		result, err := m.sdks[0].MutateGraph(t.Context(), original.vertices, original.edges, original.identity)
		if err != nil || !reflect.DeepEqual(result, original.result) {
			t.Fatal("same-ID original", err)
		}
		m.await(t, original, 0, 1, 2)
		t.Logf("relay: three immutable receipts, two vertices, one edge; final visible CDC cursor bytes=%d", len(cursor))
	case "mixed-gap-conflict":
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		changes := graphv1connect.NewLanternChangeServiceClient(m.g.http, m.g.urls[2])
		resumed, err := changes.WatchChanges(ctx, securityWireRequest(m.g.machine, &pb.WatchChangesRequest{Prefix: "orders:", Cursor: m.cdcCursor, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
		if err != nil {
			t.Fatal("resume prior complete mixed cursor", err)
		}
		defer func() { _ = resumed.Close() }()
		m.proxy.links(false)
		gap1 := m.write(t, 0, "gap-one", "orders:gap-one:", "one", 3, false)
		gap2 := m.write(t, 0, "gap-two", "orders:gap-two:", "two", 4, false)
		for _, original := range []*mixedHAOriginal{gap1, gap2} {
			status, err := m.sdks[2].GetReceiptStatus(t.Context(), original.identity.OperationIDs[0])
			if connect.CodeOf(err) != connect.CodePermissionDenied || status.Receipt != nil {
				t.Fatal("partition negative control", status, err)
			}
			_, err = m.g.data[2].GetVertex(t.Context(), securityWireRequest(m.g.machine, &pb.GetVertexRequest{Key: original.vertices[0].Key}))
			if connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatal("partition graph negative control", err)
			}
		}
		m.proxy.links(false, [2]int{0, 1})
		m.await(t, gap1, 1)
		m.await(t, gap2, 1)
		m.proxy.links(false, [2]int{0, 1}, [2]int{1, 2})
		m.await(t, gap1, 2)
		m.await(t, gap2, 2)
		seen, frames := map[string]bool{}, 0
		for resumed.Receive() {
			frame := resumed.Msg()
			for _, item := range frame.Invalidations {
				key := item.GetVertexKey()
				if edge := item.GetEdgeKey(); edge != nil {
					key = edge.Tail + "->" + edge.Head
				}
				if !strings.HasPrefix(key, "orders:gap-") || seen[key] {
					t.Fatal("CDC resume repeated or crossed its cut", key)
				}
				seen[key] = true
			}
			if len(frame.Invalidations) > 0 && len(frame.Cursor) > 0 {
				frames++
				if len(seen) != frames*3 {
					t.Fatal("resumed cursor splits visible mixed group", len(seen), frames)
				}
				if frames == 2 {
					break
				}
			}
		}
		if len(seen) != 6 || frames != 2 {
			t.Fatal("resumed mixed CDC suffix", len(seen), frames, resumed.Err())
		}
		_ = resumed.Close()
		m.proxy.links(false)
		conflict := m.write(t, 1, "conflict", "orders:conflict:", "older-B", 5, false)
		_, err = m.g.data[0].PutVertex(t.Context(), securityWireRequest(m.g.machine, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:conflict:a", Value: &pb.Vertex_String_{String_: "newer-A"}}}))
		if err != nil {
			t.Fatal(err)
		}
		// C observes A before B. B's own order is B before A. Keep B isolated
		// until the A-only value and absent B receipt are observed at C.
		m.proxy.links(false, [2]int{0, 2})
		m.awaitVertex(t, 2, "orders:conflict:a", "newer-A")
		status, err := m.sdks[2].GetReceiptStatus(t.Context(), conflict.identity.OperationIDs[0])
		if connect.CodeOf(err) != connect.CodePermissionDenied || status.Receipt != nil {
			t.Fatal("C received B before deliberate reconnect", status, err)
		}
		m.proxy.links(true)
		for i := range m.sdks {
			m.awaitVertex(t, i, "orders:conflict:a", "newer-A")
			m.awaitVertex(t, i, "orders:conflict:b", "older-B")
			m.awaitReceipts(t, conflict, i)
			edge, err := m.sdks[i].GetEdge(t.Context(), "orders:conflict:a", "orders:conflict:b")
			if err != nil || edge.Weight != 5 {
				t.Fatal("per-effect edge lost", i, edge, err)
			}
		}
		t.Log("gap/relay caught up; B-before-A and A-before-B converge per effect with immutable B result")
	case "mixed-backup-cut":
		m.captureBackups(t)
	case "mixed-closed-wal":
		m.inspectClosedWALs(t)
	case "mixed-lost-response":
		m.write(t, 0, "lost-response", "orders:lost:", "retained", 7, true)
		for _, original := range m.originals {
			m.awaitReceipts(t, original, 0, 1, 2)
		}
	case "mixed-restarted":
		m.g.waitReady(0, 1, 2)
		for _, original := range m.originals {
			m.awaitReceipts(t, original, 0, 1, 2)
		}
		original := m.originals[0]
		result, err := m.sdks[original.origin].MutateGraph(t.Context(), original.vertices, original.edges, original.identity)
		if err != nil || !reflect.DeepEqual(result, original.result) {
			t.Fatal("same-ID confirmation after restart changed the original result", err)
		}
		for i := range m.sdks {
			m.awaitVertex(t, i, "orders:conflict:a", "newer-A")
			for _, original := range m.originals {
				edge, err := m.sdks[i].GetEdge(t.Context(), original.edges[0].Edge.Tail, original.edges[0].Edge.Head)
				if err != nil || edge.Weight != original.edges[0].Edge.Weight {
					t.Fatal("restart double-add or missing edge", i, original.name, edge, err)
				}
			}
		}
		m.snapshot(t, 2, "orders:", 2*len(m.originals)+m.extraVertices, len(m.originals))
		m.checkRestoredOrigins(t)
	case "mixed-authorization":
		m.authorization(t)
		m.blind(t)
	case "mixed-partition-ready":
		m.g.waitReady(0, 1, 2)
		original := m.newOriginal(t, 2, "isolated-refusal", "orders:isolated:", "must not publish", 13)
		raw, err := protojson.Marshal(m.wireRequest(t, original))
		if err != nil {
			t.Fatal(err)
		}
		if err := createPrivateTestFile("/control/isolated-mixed-request.json", raw); err != nil {
			t.Fatal(err)
		}
	case "mixed-partition-write":
		m.g.waitReady(0, 1)
		original := m.write(t, 0, "full-partition", "orders:partition:", "surviving quorum", 11, false)
		m.await(t, original, 0, 1)
	case "mixed-full-reconnected":
		m.g.waitReady(0, 1, 2)
		for _, original := range m.originals {
			m.awaitReceipts(t, original, 0, 1, 2)
		}
		m.await(t, m.originals[len(m.originals)-1], 2)
		for i := range m.sdks {
			_, err := m.g.data[i].GetVertex(t.Context(), securityWireRequest(m.g.machine, &pb.GetVertexRequest{Key: "orders:isolated:a"}))
			if connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatal("isolated refused mutation was published", i, err)
			}
		}
	case "finish":
		m.proxy.links(false)
	default:
		t.Fatal("unknown mixed HA phase", phase)
	}
	m.proxy.mu.Lock()
	t.Logf("closed TLS proxy response-byte counts: %v", m.proxy.bytes)
	m.proxy.mu.Unlock()
}

func (m *mixedHAGate) newOriginal(t *testing.T, origin int, name, prefix, value string, weight float32) *mixedHAOriginal {
	t.Helper()
	sdk := m.sdks[origin]
	capability, err := sdk.GetReceiptCapability(t.Context())
	if err != nil || capability.GraphMutation == nil {
		t.Fatal("mixed capability", err)
	}
	identity, err := sdk.NewGraphMutationReceiptContext(capability, 3)
	if err != nil {
		t.Fatal(err)
	}
	id, err := sdk.NewContribID()
	if err != nil {
		t.Fatal(err)
	}
	original := &mixedHAOriginal{name: name, origin: origin, identity: identity, vertices: []client.VertexInput{{Key: prefix + "a", Value: value}, {Key: prefix + "b", Value: value}}, edges: []client.EdgeAddReceiptInput{{Edge: client.EdgeInput{Tail: prefix + "a", Head: prefix + "b", Weight: weight}, ContribID: id}}}
	return original
}

func (m *mixedHAGate) write(t *testing.T, origin int, name, prefix, value string, weight float32, lose bool) *mixedHAOriginal {
	t.Helper()
	original := m.newOriginal(t, origin, name, prefix, value, weight)
	sdk, identity := m.sdks[origin], original.identity
	var err error
	if lose {
		drop := &dropFirstReceiptResponseTransport{inner: m.g.http.Transport, pathSuffix: "/MutateGraph"}
		counter := &mixedHACountTransport{inner: drop}
		oneShot, e := client.NewLantern(m.g.urls[origin], client.WithHTTPClient(&http.Client{Transport: counter, Timeout: 6 * time.Second}), client.WithAuthToken(m.g.machine))
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = oneShot.Close() }()
		if _, e = oneShot.MutateGraph(t.Context(), original.vertices, original.edges, identity); e == nil {
			t.Fatal("lost response returned success")
		}
		if !drop.dropped.Load() {
			t.Fatal("no committed response was actually discarded")
		}
		if counter.calls.Load() != 1 {
			t.Fatal("uncertain mutation automatically resent", counter.calls.Load())
		}
		// No retry or replacement ID. Resolve all members first.
		m.awaitReceipts(t, original, origin)
	}
	original.result, err = sdk.MutateGraph(t.Context(), original.vertices, original.edges, identity)
	if err != nil || len(original.result.Vertices) != 2 || len(original.result.Edges) != 1 || original.result.Edges[0].EffectiveWeight != weight {
		t.Fatal("mixed commit/original", name, err)
	}
	m.awaitReceipts(t, original, origin)
	changed := append([]client.EdgeAddReceiptInput(nil), original.edges...)
	changed[0].Edge.Weight++
	if _, err := sdk.MutateGraph(t.Context(), original.vertices, changed, identity); !errors.Is(err, client.ErrInvalidArgument) {
		t.Fatal("changed original intent accepted", err)
	}
	m.originals = append(m.originals, original)
	return original
}

func (m *mixedHAGate) awaitReceipts(t *testing.T, original *mixedHAOriginal, nodes ...int) {
	t.Helper()
	for _, node := range nodes {
		var statuses []client.ReceiptStatus
		for index, id := range original.identity.OperationIDs {
			deadline := time.Now().Add(30 * time.Second)
			for {
				status, err := m.sdks[node].GetReceiptStatus(t.Context(), id)
				if err == nil && status.State == client.ReceiptConfirmed {
					if status.Receipt.ItemCount != 3 || status.Receipt.ItemIndex != uint32(index) || status.Receipt.GroupID != original.identity.GroupID {
						t.Fatal("mixed group mismatch")
					}
					statuses = append(statuses, status)
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("receipt convergence", original.name, node, status, err)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		if original.statuses == nil {
			original.statuses = statuses
		} else if !reflect.DeepEqual(statuses, original.statuses) {
			t.Fatal("immutable original intent/results changed", original.name, node)
		}
		raw, err := json.Marshal(statuses)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s node=%d original_receipts_sha256=%x", original.name, node+1, sha256.Sum256(raw))
	}
}

func (m *mixedHAGate) awaitVertex(t *testing.T, node int, key, value string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, err := m.g.data[node].GetVertex(t.Context(), securityWireRequest(m.g.machine, &pb.GetVertexRequest{Key: key}))
		if err == nil && response.Msg.GetVertex().GetString_() == value {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("vertex convergence", node, key, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (m *mixedHAGate) await(t *testing.T, original *mixedHAOriginal, nodes ...int) {
	t.Helper()
	m.awaitReceipts(t, original, nodes...)
	for _, node := range nodes {
		for _, vertex := range original.vertices {
			m.awaitVertex(t, node, vertex.Key, vertex.Value.(string))
		}
		edge, err := m.sdks[node].GetEdge(t.Context(), original.edges[0].Edge.Tail, original.edges[0].Edge.Head)
		if err != nil || edge.Weight != original.edges[0].Edge.Weight {
			t.Fatal("edge convergence/dedup", node, edge, err)
		}
	}
}

func (m *mixedHAGate) snapshot(t *testing.T, node int, prefix string, vertices, edges int) {
	t.Helper()
	stream, err := m.g.data[node].BackupSnapshot(t.Context(), securityWireRequest(m.g.machine, &pb.BackupSnapshotRequest{VertexPrefix: prefix}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	vs, es := make(map[string]bool), 0
	var seenEdges []*pb.Edge
	for stream.Receive() {
		if v := stream.Msg().GetVertex(); v != nil {
			vs[v.Key] = true
		}
		if e := stream.Msg().GetEdge(); e != nil {
			es++
			seenEdges = append(seenEdges, proto.Clone(e).(*pb.Edge))
		}
	}
	if stream.Err() != nil || len(vs) != vertices || es != edges {
		t.Fatal("mixed snapshot cut", len(vs), es, stream.Err())
	}
	for _, edge := range seenEdges {
		if !vs[edge.Tail] || !vs[edge.Head] {
			t.Fatal("snapshot edge without mixed endpoints")
		}
	}
	t.Logf("public consistent snapshot node=%d vertices=%d edges=%d", node+1, len(vs), es)
}

func (m *mixedHAGate) authorization(t *testing.T) {
	t.Helper()
	original := m.originals[0]
	request := &pb.MutateGraphRequest{Items: []*pb.GraphMutationItem{{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: &pb.Vertex{Key: "orders:denied", Value: &pb.Vertex_String_{String_: "must not publish"}}}}, {Operation: &pb.GraphMutationItem_AddEdge{AddEdge: &pb.GraphMutationEdgeAdd{Edge: &pb.Edge{Tail: "orders:denied", Head: "forbidden:head", Weight: 1}, ContribId: make([]byte, 24)}}}}}
	// Use freshly valid IDs/continuity so the auth negative control cannot pass
	// only because an old original was reused or its envelope was malformed.
	capability, err := m.sdks[0].GetReceiptCapability(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := m.sdks[0].NewGraphMutationReceiptContext(capability, 2)
	if err != nil {
		t.Fatal(err)
	}
	request.ReceiptContext = mixedHAWireContext(t, m.g, 0, identity)
	request.Items[1].GetAddEdge().ContribId[0] = 1
	_, err = m.g.data[0].MutateGraph(t.Context(), securityWireRequest(m.g.token, request))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("all-member authorization", err)
	}
	for i := range m.g.data {
		_, err = m.g.data[i].GetVertex(t.Context(), securityWireRequest(m.g.machine, &pb.GetVertexRequest{Key: "orders:denied"}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatal("denied group published prefix", i, err)
		}
	}
	reader := m.g.issuer.token(t, "reader", func(claims map[string]any) {
		claims["iat"] = time.Now().Add(-time.Minute).Unix()
		delete(claims, "auth_time")
	})
	read, err := m.g.data[2].GetVertex(t.Context(), securityWireRequest(reader, &pb.GetVertexRequest{Key: "orders:relay:a"}))
	if err != nil || read.Msg.GetVertex().GetString_() != "initial" {
		t.Fatal("reader authentication positive control", err)
	}
	id := original.identity.OperationIDs[0]
	response, err := m.g.data[2].GetReceiptStatus(t.Context(), securityWireRequest(reader, &pb.GetReceiptStatusRequest{OperationId: id[:]}))
	if err == nil && response.Msg.GetStatus().GetReceipt() != nil {
		t.Fatal("unauthorized original disclosed")
	}
	if err != nil && connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("unrelated nondisclosure failure", err)
	}
	principal, err := m.g.principal(0)
	if err != nil {
		t.Fatal(err)
	}
	// This known receipt is disclosed before revocation under the same token.
	before, err := m.g.data[2].GetReceiptStatus(t.Context(), securityWireRequest(m.g.token, &pb.GetReceiptStatusRequest{OperationId: id[:]}))
	if err != nil || before.Msg.GetStatus().GetReceipt() == nil {
		t.Fatal("revocation positive control", err)
	}
	request.Items[1].GetAddEdge().Edge.Head = "orders:relay:a"
	change := &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: m.g.issuer.provider.URL, Subject: "admin"}, RoleId: "wire_data"}}}
	prepared, err := m.g.controls[0].PrepareSecurityChanges(t.Context(), securityWireRequest(m.g.token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: principal.Version.CurrentProfile, ExpectedCut: principal.Version.CurrentCut, Changes: []*pb.SecurityChange{change}}}))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.g.controls[0].ApplySecurityChanges(t.Context(), securityWireRequest(m.g.token, &pb.ApplySecurityChangesRequest{CurrentReview: prepared.Msg.CurrentReview}))
	for i := range m.g.controls {
		outcome := awaitCurrentPublicOriginal(t, t.Context(), m.g.controls[i], m.g.token, prepared.Msg.CurrentReview)
		if outcome.GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			t.Fatal("revocation not applied")
		}
		cap, e := m.g.data[i].GetReceiptCapability(t.Context(), securityWireRequest(m.g.machine, &pb.GetReceiptCapabilityRequest{}))
		if e != nil {
			t.Fatal(e)
		}
		request.ReceiptContext.Endpoint = cap.Msg.Endpoint
		_, err = m.g.data[i].MutateGraph(t.Context(), securityWireRequest(m.g.token, request))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("revoked mixed mutation", i, err)
		}
		_, err = m.g.data[i].GetReceiptStatus(t.Context(), securityWireRequest(m.g.token, &pb.GetReceiptStatusRequest{OperationId: id[:]}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("revoked receipt disclosure", i, err)
		}
	}
}

type mixedHACountTransport struct {
	inner http.RoundTripper
	calls atomic.Int32
}

func (m *mixedHAGate) wireRequest(t *testing.T, original *mixedHAOriginal) *pb.MutateGraphRequest {
	t.Helper()
	request := &pb.MutateGraphRequest{ReceiptContext: mixedHAWireContext(t, m.g, original.origin, original.identity)}
	for _, vertex := range original.vertices {
		request.Items = append(request.Items, &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: &pb.Vertex{Key: vertex.Key, Value: &pb.Vertex_String_{String_: vertex.Value.(string)}}}})
	}
	for _, edge := range original.edges {
		request.Items = append(request.Items, &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_AddEdge{AddEdge: &pb.GraphMutationEdgeAdd{Edge: &pb.Edge{Tail: edge.Edge.Tail, Head: edge.Edge.Head, Weight: edge.Edge.Weight}, ContribId: append([]byte(nil), edge.ContribID[:]...)}}})
	}
	return request
}

func (m *mixedHAGate) blind(t *testing.T) {
	t.Helper()
	original := m.newOriginal(t, 2, "blind", "orders:blind:tail:", "undisclosed", 9)
	original.edges[0].Edge.Head = "orders:blind:head"
	if _, err := m.g.data[2].PutVertex(t.Context(), securityWireRequest(m.g.machine, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: original.edges[0].Edge.Head, Value: &pb.Vertex_String_{String_: "hidden head"}}})); err != nil {
		t.Fatal(err)
	}
	m.extraVertices++
	token := m.g.issuer.token(t, "blind-writer", func(claims map[string]any) {
		claims["iat"] = time.Now().Add(-time.Minute).Unix()
		delete(claims, "auth_time")
	})
	if _, err := m.g.controls[2].GetCurrentPrincipal(t.Context(), securityWireRequest(token, &pb.GetCurrentPrincipalRequest{})); err != nil {
		t.Fatal("blind writer authentication positive control", err)
	}
	response, err := m.g.data[2].MutateGraph(t.Context(), securityWireRequest(token, m.wireRequest(t, original)))
	if err != nil || response.Msg.GetAcceptance() == nil || len(response.Msg.GetResults()) != 0 {
		t.Fatal("whole mixed result disclosed to write-only principal", err)
	}
	m.originals = append(m.originals, original)
	m.await(t, original, 2, 0, 1)
	for node := range m.sdks {
		request := &pb.GetReceiptStatusesRequest{}
		for _, id := range original.identity.OperationIDs {
			request.OperationIds = append(request.OperationIds, append([]byte(nil), id[:]...))
		}
		status, e := m.g.data[node].GetReceiptStatuses(t.Context(), securityWireRequest(token, request))
		if e != nil || len(status.Msg.GetStatuses()) != 3 {
			t.Fatal("blind mixed status batch", node, e)
		}
		for _, item := range status.Msg.GetStatuses() {
			if item.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_EFFECT_UNDISCLOSED || item.GetReceipt() != nil {
				t.Fatal("blind receipt result disclosed", node)
			}
		}
	}
	t.Log("third-origin mixed commit with a write-only head accepted with empty results; all three nodes preserve whole-batch EFFECT_UNDISCLOSED without receipt payload")
}

func (c *mixedHACountTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == graphv1connect.LanternServiceMutateGraphProcedure {
		c.calls.Add(1)
	}
	return c.inner.RoundTrip(r)
}

// Select one already committed, validated backup before the final mutation.
// Copies preserve its exact bytes; all original sets/WALs remain untouched.
// Restart consumes this earlier cut plus its real, later WAL suffix.
func (m *mixedHAGate) captureBackups(t *testing.T) {
	t.Helper()
	policy := mixedHAReceiptPolicy()
	for node := range m.sdks {
		source, target := fmt.Sprintf("/archive-%d", node+1), fmt.Sprintf("/restore-%d", node+1)
		instance := fmt.Sprintf("mixed-node-%d", node+1)
		deadline := time.Now().Add(20 * time.Second)
		var selected backup.ReceiptBackupSetEvidence
		for {
			evidence, err := backup.LoadLatestReceiptBackupSet(source, instance)
			if err == nil {
				restore, e := backup.PrepareRestartReceiptStartupRestore(evidence, policy)
				if e != nil {
					t.Fatal("invalid produced backup", e)
				}
				if len(restore.Capture.Receipts.Receipts) == 3*len(m.originals) && len(restore.Capture.Origins) == 2 {
					selected = evidence
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("complete mixed backup not produced", node, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		paths, err := filepath.Glob(filepath.Join(source, "*.set.json"))
		if err != nil {
			t.Fatal(err)
		}
		copied := false
		for _, path := range paths {
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			var manifest struct {
				SetID   string `json:"set_id"`
				Members []struct {
					Name string `json:"name"`
				} `json:"members"`
			}
			if json.Unmarshal(raw, &manifest) != nil {
				t.Fatal("invalid produced manifest")
			}
			if manifest.SetID != fmt.Sprintf("%020d", selected.SetID) {
				continue
			}
			for _, member := range manifest.Members {
				if filepath.Base(member.Name) != member.Name {
					t.Fatal("unsafe member")
				}
				memberRaw, e := os.ReadFile(filepath.Join(source, member.Name))
				if e != nil {
					t.Fatal(e)
				}
				if e = createPrivateTestFile(filepath.Join(target, member.Name), memberRaw); e != nil {
					t.Fatal(e)
				}
			}
			if e = createPrivateTestFile(filepath.Join(target, filepath.Base(path)), raw); e != nil {
				t.Fatal(e)
			}
			copied = true
		}
		frozen, err := backup.LoadLatestReceiptBackupSet(target, instance)
		if !copied || err != nil || frozen.WALCut != selected.WALCut || !bytes.Equal(frozen.Archive, selected.Archive) {
			t.Fatal("selected backup changed", err)
		}
		m.backupCuts[node] = selected.WALCut.Seq
		t.Logf("node=%d validated frozen backup cut=%d receipts=%d origins=2", node+1, selected.WALCut.Seq, 3*len(m.originals))
	}
}

func (m *mixedHAGate) inspectClosedWALs(t *testing.T) {
	t.Helper()
	for node := range m.sdks {
		groups := make(map[string]bool)
		origins := make(map[string]uint64)
		var tip uint64
		err := mutationlog.ReplayFileWAL(fmt.Sprintf("/journal-%d/business.wal", node+1), func(raw []byte) (mutationlog.MutationOp, error) { return append([]byte(nil), raw...), nil }, func(entry mutationlog.Entry) error {
			tip = entry.Seq
			raw := entry.Op.([]byte)
			if len(raw) < 16 || string(raw[:8]) != "LRWU\x0a\x00\x00\x00" || int(binary.BigEndian.Uint32(raw[12:16])) != len(raw)-16 {
				return errors.New("unexpected WAL union")
			}
			if raw[8] != 11 {
				return nil
			}
			var mutation pb.Mutation
			if err := proto.Unmarshal(raw[16:], &mutation); err != nil {
				return err
			}
			call := mutation.GetOp().GetGraphMutation()
			if len(call.GetItems()) != 3 || mutation.Seq == 0 || len(mutation.Origin) != 16 {
				return errors.New("incomplete mixed WAL bundle")
			}
			origin := fmt.Sprintf("%x", mutation.Origin)
			origins[origin] = max(origins[origin], mutation.Seq)
			group := string(call.Items[0].Receipt.LogicalCallId)
			if groups[group] {
				return errors.New("duplicate mixed WAL decision")
			}
			groups[group] = true
			for i, item := range call.Items {
				if item.Receipt.ItemCount != 3 || item.Receipt.ItemIndex != uint32(i) || string(item.Receipt.LogicalCallId) != group {
					return errors.New("split receipt/WAL group")
				}
			}
			t.Logf("node=%d mixed WAL local=%d origin=%x origin_seq=%d items=3 projection=%d", node+1, entry.Seq, mutation.Origin, mutation.Seq, len(call.Projection))
			return nil
		})
		if err != nil || len(groups) != len(m.originals) || m.backupCuts[node] == 0 || tip <= m.backupCuts[node] {
			t.Fatal("closed WAL or snapshot-tail evidence", node, len(groups), tip, m.backupCuts[node], err)
		}
		if m.walOrigins == nil {
			m.walOrigins = origins
		} else if !reflect.DeepEqual(m.walOrigins, origins) {
			t.Fatal("replicas disagree on the complete origin prefixes", node, origins, m.walOrigins)
		}
		t.Logf("node=%d closed WAL tip=%d selected snapshot=%d positive tail=%d", node+1, tip, m.backupCuts[node], tip-m.backupCuts[node])
	}
}

func mixedHAReceiptPolicy() mutationreceipt.Config {
	var epoch mutationreceipt.Epoch
	for i := range epoch {
		epoch[i] = 0x42
	}
	return mutationreceipt.Config{Epoch: epoch, Retention: time.Hour, MaxEntries: 128, MaxBytes: 1048576}
}

func (m *mixedHAGate) checkRestoredOrigins(t *testing.T) {
	t.Helper()
	if len(m.walOrigins) != 3 {
		t.Fatal("missing pre-restart three-origin prefix evidence", m.walOrigins)
	}
	for node := range m.sdks {
		deadline := time.Now().Add(20 * time.Second)
		for {
			evidence, err := backup.LoadLatestReceiptBackupSet(fmt.Sprintf("/restore-%d", node+1), fmt.Sprintf("mixed-node-%d", node+1))
			if err == nil && evidence.WALCut.Seq > m.backupCuts[node] {
				restored, e := backup.PrepareRestartReceiptStartupRestore(evidence, mixedHAReceiptPolicy())
				if e != nil {
					t.Fatal("invalid post-restart backup", e)
				}
				origins := make(map[string]uint64)
				for _, origin := range restored.Capture.Origins {
					origins[fmt.Sprintf("%x", origin.Origin)] = origin.LastSeq
				}
				if !reflect.DeepEqual(origins, m.walOrigins) || len(restored.Capture.Receipts.Receipts) != 3*len(m.originals) {
					t.Fatal("restarted origin prefixes or receipt bundle count changed", node, origins, m.walOrigins)
				}
				t.Logf("node=%d restored origin prefixes=%v receipts=%d cut=%d", node+1, origins, len(restored.Capture.Receipts.Receipts), evidence.WALCut.Seq)
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("no post-restart backup to verify replayed prefixes", node, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// Read-only diagnostic for retained volumes after a campaign has stopped every
// product process. This does not certify CLEAN or authorize reopening a server.
func TestMixedHAClosedWALInspection(t *testing.T) {
	if os.Getenv("LANTERN_MIXED_HA_INSPECT") != "1" {
		t.Skip("explicit read-only inspection of stopped mixed HA volumes")
	}
	groups := 5
	if raw := os.Getenv("LANTERN_MIXED_HA_INSPECT_GROUPS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 16 {
			t.Fatal("bounded expected group count required")
		}
		groups = value
	}
	m := &mixedHAGate{sdks: make([]*client.Lantern, 3), originals: make([]*mixedHAOriginal, groups)}
	for node := range m.sdks {
		selected, err := backup.LoadLatestReceiptBackupSet(fmt.Sprintf("/restore-%d", node+1), fmt.Sprintf("mixed-node-%d", node+1))
		if err != nil {
			t.Fatal(err)
		}
		m.backupCuts[node] = selected.WALCut.Seq
	}
	m.inspectClosedWALs(t)
}

func mixedHAWireContext(t *testing.T, g *custodyProcessGate, node int, identity client.GraphMutationReceiptContext) *pb.MutationReceiptContext {
	t.Helper()
	capability, err := g.data[node].GetReceiptCapability(t.Context(), securityWireRequest(g.machine, &pb.GetReceiptCapabilityRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	result := &pb.MutationReceiptContext{Endpoint: capability.Msg.Endpoint, LogicalCallId: identity.GroupID[:]}
	for _, id := range identity.OperationIDs {
		result.OperationIds = append(result.OperationIds, append([]byte(nil), id[:]...))
	}
	return result
}
