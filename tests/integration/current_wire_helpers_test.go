package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

// Shared public-wire proof for machine and native end-user credentials. The
// capture sits after real final-Write authorization; it cannot grant access.
func currentWireRevokeActiveExport(t *testing.T, ctx context.Context, transport *http.Client, endpoint string, control graphv1connect.LanternSecurityServiceClient, admin, credential string, change *pb.SecurityChange, exportBoundary *atomic.Pointer[currentExportBoundary]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	const prefix = "orders:active-export:"
	vertices := make([]*pb.Vertex, 4)
	for i := range vertices {
		vertices[i] = &pb.Vertex{Key: fmt.Sprintf("%s%d", prefix, i), Value: &pb.Vertex_String_{String_: strings.Repeat(fmt.Sprintf("record-%d ", i), 2048)}}
	}
	writer := graphv1connect.NewLanternServiceClient(transport, endpoint)
	if _, err := writer.PutVertices(ctx, securityWireRequest(admin, &pb.PutVerticesRequest{Vertices: vertices})); err != nil {
		t.Fatal("seed multi-unit export", err)
	}

	// Separate transport owns the export connection; control keeps its
	// existing connection and the production server's credit/lifecycle hooks.
	exportHTTP := h2cClient()
	defer exportHTTP.CloseIdleConnections()
	exporter := graphv1connect.NewLanternServiceClient(&http.Client{Transport: authIngressRoundTripper{exportHTTP.Transport}}, endpoint, connect.WithAcceptCompression("gzip", nil, nil))
	type exportResult struct {
		vertices []*pb.Vertex
		err      error
	}
	readExport := func() exportResult {
		stream, err := exporter.BackupSnapshot(ctx, securityWireRequest(credential, &pb.BackupSnapshotRequest{VertexPrefix: prefix}))
		if err != nil {
			return exportResult{err: err}
		}
		defer func() { _ = stream.Close() }()
		var got []*pb.Vertex
		for stream.Receive() {
			got = append(got, stream.Msg().GetVertex())
		}
		return exportResult{got, stream.Err()}
	}
	baseline := readExport()
	if baseline.err != nil || len(baseline.vertices) != len(vertices) {
		t.Fatal("complete multi-unit positive control", len(baseline.vertices), baseline.err)
	}
	for _, vertex := range baseline.vertices {
		matched := false
		for _, want := range vertices {
			matched = matched || proto.Equal(vertex, want)
		}
		if !matched {
			t.Fatal("export baseline changed seeded data")
		}
	}
	type heldUnit struct {
		payload []byte
		remote  string
	}
	held := make(chan heldUnit, 1)
	release := make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	defer resume()
	prefixes, bodies := 0, 0
	var completedPrefix []byte
	boundary := &currentExportBoundary{done: make(chan struct{}), beforeWrite: func(payload []byte, remote string) {
		// Connect writes the uncompressed envelope prefix separately. Its
		// service admission and final Write authorization have both passed;
		// the body still needs its own Write authorization. Holding a body
		// instead would let the next service check mask a missing Write check.
		if len(payload) == 5 && payload[0] == 0 {
			if size := binary.BigEndian.Uint32(payload[1:]); size == 0 || size > 1<<20 {
				t.Error("export envelope exceeded unchanged send cap")
			}
			prefixes++
			if prefixes == 2 {
				held <- heldUnit{bytes.Clone(payload), remote}
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return
		}
		var frame pb.BackupSnapshotResponse
		if proto.Unmarshal(payload, &frame) == nil && frame.GetVertex() != nil {
			bodies++
		}
	}, afterWrite: func(payload []byte, n int, err error) {
		if prefixes == 2 && len(payload) == 5 && payload[0] == 0 {
			if err != nil || n != len(payload) {
				t.Error("already authorized prefix could not complete", n, err)
			}
			completedPrefix = bytes.Clone(payload)
		}
	}}
	exportBoundary.Store(boundary)
	defer exportBoundary.Store(nil)
	result := make(chan exportResult, 1)
	go func() { result <- readExport() }()
	var unit heldUnit
	select {
	case unit = <-held:
	case <-ctx.Done():
		t.Fatal("second authorized export prefix not reached", ctx.Err())
	}
	var controlAddress string
	controlCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { controlAddress = info.Conn.LocalAddr().String() }})
	current, err := control.GetCurrentPrincipal(controlCtx, securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := control.PrepareSecurityChanges(controlCtx, securityWireRequest(admin, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Msg.Version.CurrentProfile, ExpectedCut: current.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{change}}}))
	if err != nil {
		t.Fatal("prepare export grant withdrawal", err)
	}
	applied, err := control.ApplySecurityChanges(controlCtx, securityWireRequest(admin, &pb.ApplySecurityChangesRequest{CurrentReview: prepared.Msg.CurrentReview}))
	var original *pb.CurrentSecurityOriginalOutcome
	if err == nil {
		original = applied.Msg.GetCurrentResult().GetOriginal()
	}
	if original == nil {
		original = awaitCurrentPublicOriginal(t, ctx, control, admin, prepared.Msg.CurrentReview)
	}
	if original.GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED || proto.Equal(original.ResultingCut, current.Msg.Version.CurrentCut) {
		t.Fatal("export grant withdrawal not applied", original)
	}
	if controlAddress == "" || controlAddress == unit.remote {
		t.Fatal("control did not use an independent connection")
	}
	resume()
	var got exportResult
	select {
	case got = <-result:
	case <-ctx.Done():
		t.Fatal("revoked export did not terminate", ctx.Err())
	}
	select {
	case <-boundary.done:
	case <-ctx.Done():
		t.Fatal("export handler did not terminate", ctx.Err())
	}
	if !bytes.Equal(completedPrefix, unit.payload) {
		t.Fatal("already authorized immutable prefix changed")
	}
	if ctx.Err() != nil || got.err == nil || len(got.vertices) != 1 || bodies != 1 {
		t.Fatal("revoked export disclosed a body requiring new Write authorization", len(got.vertices), bodies, got.err, ctx.Err())
	}
	t.Logf("Contract A: baseline=%d records; independent-connection grant withdrawal APPLIED; immutable held prefix=%d bytes completed; received records=1; later bodies=0; stream refused", len(baseline.vertices), len(unit.payload))
}

type currentWireTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
	closeIdle func()
}

func (f currentWireTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f.roundTrip(request)
}

func (f currentWireTransport) CloseIdleConnections() { f.closeIdle() }
