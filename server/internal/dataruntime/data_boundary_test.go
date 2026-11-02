package dataruntime

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// OFF admission does not call Done before the active-install wait. Observing
// that select gives these tests a deterministic boundary without a service hook.
type snapshotWaitObservedContext struct {
	context.Context
	entered chan struct{}
	proceed <-chan struct{}
	once    sync.Once
}

func (c *snapshotWaitObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	if c.proceed != nil {
		select {
		case <-c.proceed:
		case <-c.Context.Done():
		}
	}
	return c.Context.Done()
}

func TestInvokePublicWaitsForSnapshotBeforeGraphWrite(t *testing.T) {
	for _, namespace := range []bool{false, true} {
		for _, tc := range []struct {
			name         string
			verified     bool
			failAt       int
			receiptFault bool
		}{
			{name: "verified", verified: true},
			{name: "unverified"},
			{name: "validation rejected", verified: true, failAt: 3},
			{name: "gate rejected", verified: true, failAt: 4},
			{name: "persistent receipt fault", verified: true, receiptFault: true},
		} {
			name := tc.name + "/unmapped"
			if namespace {
				name = tc.name + "/namespaced"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
				svc := NewExecutor(cache)
				key := "wait:key"
				if namespace {
					svc.WithDataNamespace()
					key = "data:" + key
				}
				if tc.receiptFault {
					svc.replicationCutMu.Lock()
					svc.markReceiptCommitFaultLocked()
					svc.replicationCutMu.Unlock()
				}
				original := errors.New("original Snapshot authority rejected")
				checks := 0
				finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error {
					checks++
					if checks == tc.failAt {
						return original
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = finish(false) }()
				observed := &snapshotWaitObservedContext{Context: ctx, entered: make(chan struct{})}
				var calls atomic.Int32
				type result struct {
					response *pb.PutVerticesResponse
					err      error
				}
				done := make(chan result, 1)
				go func() {
					response, err := invokePublic(observed, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "wait:key"}}}, svc,
						func(ctx context.Context, request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
							calls.Add(1)
							return svc.PutVertices(ctx, request)
						})
					done <- result{response, err}
				}()
				waitReceiptTest(t, "public active-install wait", observed.entered)
				if calls.Load() != 0 {
					t.Fatal("public write reached dispatch during live install")
				}
				if _, live := cache.GetVertex(key); live {
					t.Fatal("waiting write changed the graph")
				}
				finishErr := finish(tc.verified)
				if tc.failAt != 0 && finishErr != original || tc.failAt == 0 && finishErr != nil {
					t.Fatal("wait changed the original finish error", finishErr)
				}
				got := waitReceiptTest(t, "public write after install", done)
				if calls.Load() != 1 {
					t.Fatal("public operation was skipped or retried", calls.Load())
				}
				if !tc.verified || tc.failAt != 0 || tc.receiptFault {
					if connect.CodeOf(got.err) != connect.CodeFailedPrecondition || got.response != nil {
						t.Fatal("completed failed install bypassed the original write guard", got.err)
					}
					if _, live := cache.GetVertex(key); live {
						t.Fatal("refused write changed the graph")
					}
					return
				}
				if got.err != nil || got.response == nil || len(got.response.GetOutcomes()) != 1 ||
					got.response.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
					t.Fatal("verified install did not resume the original write", got.err)
				}
				if _, live := cache.GetVertex(key); !live {
					t.Fatal("resumed write never reached the graph")
				}
			})
		}
	}
}

func TestInvokePublicSnapshotWaitCancellationHasNoEffect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		active    bool
		completed bool
	}{
		{name: "no active install"},
		{name: "active install", active: true},
		{name: "completion and cancellation ready", active: true, completed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounded, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(bounded)
			defer cancel()
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			svc := NewExecutor(cache)
			var completion *snapshotInstallCompletion
			var finish func(bool) error
			if tc.active {
				var err error
				finish, err = svc.BeginSnapshotInstallChecked(bounded, func() error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = finish(false) }()
				completion = svc.activeSnapshotInstall.Load()
			} else {
				cancel()
			}
			observed := &snapshotWaitObservedContext{Context: ctx, entered: make(chan struct{})}
			proceed := make(chan struct{})
			release := sync.OnceFunc(func() { close(proceed) })
			defer release()
			if tc.completed {
				observed.proceed = proceed
			}
			var calls atomic.Int32
			done := make(chan error, 1)
			go func() {
				_, err := invokePublic(observed, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "canceled"}}}, svc,
					func(ctx context.Context, request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
						calls.Add(1)
						return svc.PutVertices(ctx, request)
					})
				done <- err
			}()
			if tc.active {
				waitReceiptTest(t, "cancellable active-install wait", observed.entered)
				if tc.completed {
					if err := finish(true); err != nil {
						t.Fatal(err)
					}
				}
				cancel()
				release()
			}
			if err := waitReceiptTest(t, "canceled public caller", done); connect.CodeOf(err) != connect.CodeCanceled || calls.Load() != 0 {
				t.Fatal("canceled wait dispatched a graph operation", err, calls.Load())
			}
			if _, live := cache.GetVertex("canceled"); live {
				t.Fatal("canceled wait changed the graph")
			}
			if tc.completed {
				if svc.activeSnapshotInstall.Load() != nil || svc.SnapshotInstallActive() || svc.SnapshotRepairRequired() {
					t.Fatal("completed install was changed by canceled public wait")
				}
			} else if tc.active {
				if svc.activeSnapshotInstall.Load() != completion || !svc.SnapshotInstallActive() || !svc.SnapshotRepairRequired() {
					t.Fatal("public cancellation ended or repaired the installer")
				}
				if err := finish(true); err != nil {
					t.Fatal("public cancellation withdrew the independent install authority", err)
				}
			}
		})
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Time{})
	defer cancel()
	svc := NewExecutor(newFakeBackend())
	if err := svc.waitForActiveSnapshotInstall(ctx); connect.CodeOf(err) != connect.CodeDeadlineExceeded {
		t.Fatal("expired caller with no active attempt lost its deadline", err)
	}
}

func TestInvokePublicSnapshotWaitDoesNotRetryMutationError(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	for _, key := range []string{"tail", "head"} {
		cache.PutVertexWithExpiration(key, &pb.Vertex{Key: key}, time.Now().Add(time.Hour))
	}
	cache.PutEdgeWithExpiration("tail", "head", 2, time.Now().Add(time.Hour))
	svc := NewExecutor(cache)
	finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = finish(false) }()
	observed := &snapshotWaitObservedContext{Context: ctx, entered: make(chan struct{})}
	original := connect.NewError(connect.CodeUnavailable, errors.New("response lost after application"))
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := invokePublic(observed, &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "tail", Head: "head", Weight: 3}}}, svc,
			func(ctx context.Context, request *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
				calls.Add(1)
				if _, err := svc.AddEdges(ctx, request); err != nil {
					return nil, err
				}
				return nil, original
			})
		done <- err
	}()
	waitReceiptTest(t, "add wait before application", observed.entered)
	if calls.Load() != 0 {
		t.Fatal("add was dispatched before verified completion")
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	if err := waitReceiptTest(t, "original add error", done); err != original || calls.Load() != 1 {
		t.Fatal("public wait changed or retried the mutation error", err, calls.Load())
	}
	if weight, live := cache.GetWeight("tail", "head"); !live || weight != 5 {
		t.Fatal("additive effect was absent or repeated", weight, live)
	}
}

func TestInvokePublicSnapshotWaitDoesNotFollowNextAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewExecutor(cache)
	finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = finish(false) }()
	proceed := make(chan struct{})
	release := sync.OnceFunc(func() { close(proceed) })
	defer release()
	observed := &snapshotWaitObservedContext{Context: ctx, entered: make(chan struct{}), proceed: proceed}
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := invokePublic(observed, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "raced"}}}, svc,
			func(ctx context.Context, request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
				calls.Add(1)
				return svc.PutVertices(ctx, request)
			})
		done <- err
	}()
	// Done is evaluated only after the helper has captured the first attempt.
	waitReceiptTest(t, "first captured attempt", observed.entered)
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	next, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next(false) }()
	completion := svc.activeSnapshotInstall.Load()
	release()
	if err := waitReceiptTest(t, "original guard after one completed attempt", done); connect.CodeOf(err) != connect.CodeFailedPrecondition || calls.Load() != 1 {
		t.Fatal("public call followed a newer install or bypassed its guard", err, calls.Load())
	}
	if svc.activeSnapshotInstall.Load() != completion || !svc.SnapshotInstallActive() || !svc.SnapshotRepairRequired() {
		t.Fatal("public call ended or repaired the newer install")
	}
	if _, live := cache.GetVertex("raced"); live {
		t.Fatal("racing install permitted a graph effect")
	}
}

func TestInvokePublicSnapshotWaitReadsOnlyCompletedGraph(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	expiration := time.Now().Add(time.Hour)
	cache.PutVertexWithExpiration("read", &pb.Vertex{Key: "read", Value: &pb.Vertex_String_{String_: "partial"}}, expiration)
	svc := NewExecutor(cache)
	finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = finish(false) }()
	observed := &snapshotWaitObservedContext{Context: ctx, entered: make(chan struct{})}
	type result struct {
		response *pb.GetVerticesResponse
		err      error
	}
	var calls atomic.Int32
	done := make(chan result, 1)
	go func() {
		response, err := invokePublic(observed, &pb.GetVerticesRequest{Keys: []string{"read"}}, svc,
			func(ctx context.Context, request *pb.GetVerticesRequest) (*pb.GetVerticesResponse, error) {
				calls.Add(1)
				return svc.GetVertices(ctx, request)
			})
		done <- result{response, err}
	}()
	waitReceiptTest(t, "public read active-install wait", observed.entered)
	if calls.Load() != 0 {
		t.Fatal("public read entered the partially installed graph")
	}
	cache.PutVertexWithExpiration("read", &pb.Vertex{Key: "read", Value: &pb.Vertex_String_{String_: "complete"}}, expiration)
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	got := waitReceiptTest(t, "public read after verified install", done)
	if got.err != nil || got.response == nil || len(got.response.GetVertices()) != 1 ||
		got.response.GetVertices()[0].GetString_() != "complete" || calls.Load() != 1 {
		t.Fatal("public read did not capture the completed graph exactly once", got.err, calls.Load())
	}
}

func TestInvokePublicSnapshotWaitPreservesAuthenticationPriority(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	svc := NewExecutor(newFakeBackend()).WithDataNamespace().WithDataAuthorization(time.Now)
	finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = finish(false) }()
	calls := 0
	_, err = invokePublic(ctx, &pb.GetVerticesRequest{Keys: []string{"private"}}, svc,
		func(context.Context, *pb.GetVerticesRequest) (*pb.GetVerticesResponse, error) {
			calls++
			return &pb.GetVerticesResponse{}, nil
		})
	if connect.CodeOf(err) != connect.CodeUnauthenticated || calls != 0 || !svc.SnapshotInstallActive() {
		t.Fatal("active install displaced initial authentication refusal", err, calls)
	}
}

func TestInvokePublicSnapshotWaitKeepsReplicationStatusAvailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	svc := NewExecutor(newFakeBackend()).WithDataNamespace()
	finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = finish(false) }()
	done := make(chan error, 1)
	go func() {
		_, err := invokePublic(ctx, &pb.GetReplicationStatusRequest{}, svc, svc.GetReplicationStatus)
		done <- err
	}()
	if err := waitReceiptTest(t, "replication diagnostic during install", done); err != nil || !svc.SnapshotInstallActive() {
		t.Fatal("replication diagnostic waited for or ended the install", err)
	}

	observed := &snapshotWaitObservedContext{Context: ctx, entered: make(chan struct{})}
	go func() {
		_, err := invokePublic(observed, &pb.GetServerStatusRequest{}, svc, svc.GetServerStatus)
		done <- err
	}()
	waitReceiptTest(t, "graph status active-install wait", observed.entered)
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	if err := waitReceiptTest(t, "graph status after verified install", done); err != nil {
		t.Fatal("graph status did not resume after verified install", err)
	}
}

func TestDataBoundarySchemaCoverage(t *testing.T) {
	service := pb.File_graph_v1_graph_proto.Services().ByName("LanternService")
	if len(publicDataRequests) != service.Methods().Len() {
		t.Fatal("public namespace boundary does not cover every RPC")
	}
	for i := range service.Methods().Len() {
		method := service.Methods().Get(i)
		if !publicDataRequests[method.Input().Name()] {
			t.Errorf("missing RPC namespace boundary: %s", method.Name())
		}
	}
	for name, fields := range dataIdentityFields {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			t.Fatal(err)
		}
		message := descriptor.(protoreflect.MessageDescriptor)
		for fieldName := range fields {
			field := message.Fields().ByName(fieldName)
			if field == nil || field.Kind() != protoreflect.StringKind {
				t.Errorf("invalid namespace rule: %s.%s", name, fieldName)
			}
		}
	}
}

func TestDataBoundaryOwnsLogicalAndPhysicalMessages(t *testing.T) {
	s := NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
	for _, logical := range []string{"users:user1", "sys:roles", "data:x", "日本語:\x00"} {
		request := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: logical, Value: &pb.Vertex_String_{String_: "sys:untouched"}}}}
		mapped, _, err := s.mapDataRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		physical := mapped.(*pb.PutVerticesRequest).Vertices[0]
		if physical.Key != "data:"+logical || physical.GetString_() != "sys:untouched" || request.Vertices[0].Key != logical {
			t.Fatal("identity or ordinary value conversion drift")
		}
		out := &pb.GetVerticesResponse{Vertices: []*pb.Vertex{physical}, Missing: []string{"data:" + logical}}
		decoded, err := s.mapDataResponse(out, nil)
		if err != nil || decoded.(*pb.GetVerticesResponse).Vertices[0].Key != logical || decoded.(*pb.GetVerticesResponse).Missing[0] != logical || out.Vertices[0].Key != "data:"+logical {
			t.Fatalf("owned response decoding: %v", err)
		}
	}
	if _, _, err := s.mapDataRequest(&pb.ScanVerticesRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mapDataResponse(&pb.GetVertexResponse{Vertex: &pb.Vertex{Key: "sys:security:revision"}}, nil); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatal("internal system identity escaped through public response")
	}
	for _, request := range []proto.Message{&pb.DeleteVerticesByPrefixRequest{}, &pb.DeleteEdgesByPrefixRequest{}, &pb.TopVerticesByDegreeRequest{}} {
		if _, _, err := s.mapDataRequest(request); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("physical namespace prefix defeated a logical input guard")
		}
	}
}

func TestDataCursorConfidentialityAndRequestBinding(t *testing.T) {
	s := NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
	_, binding, err := s.mapDataRequest(&pb.ScanVerticesRequest{Prefix: "users:", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	internal := encodeCursor(scanCursor{LastKey: "data:users:secret"})
	response, err := s.mapDataResponse(&pb.ScanVerticesResponse{NextCursor: internal}, binding)
	if err != nil {
		t.Fatal(err)
	}
	cursor := response.(*pb.ScanVerticesResponse).NextCursor
	if bytes.Contains(cursor, []byte("secret")) || bytes.Equal(cursor, internal) {
		t.Fatal("public cursor exposed internal identities")
	}
	mapped, _, err := s.mapDataRequest(&pb.ScanVerticesRequest{Prefix: "users:", Limit: 2, Cursor: cursor})
	if err != nil || !bytes.Equal(mapped.(*pb.ScanVerticesRequest).Cursor, internal) {
		t.Fatalf("cursor round trip: %v", err)
	}
	for _, request := range []proto.Message{
		&pb.ScanVerticesRequest{Prefix: "other:", Cursor: cursor},
		&pb.ScanVerticesRequest{Prefix: "users:", Order: pb.ScanOrder_SCAN_ORDER_DESC, Cursor: cursor},
		&pb.ScanVertexKeysRequest{Prefix: "users:", Cursor: cursor},
		&pb.ScanEdgesRequest{TailPrefix: "users:", Cursor: cursor},
		&pb.ScanVerticesRequest{Prefix: "users:", Cursor: internal},
	} {
		if _, _, err := s.mapDataRequest(request); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("foreign, changed-scope or legacy cursor accepted")
		}
	}
}

func TestDataCursorEquivalentScanOrderAndTypedSearchRejection(t *testing.T) {
	s := NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
	for _, keys := range []bool{false, true} {
		var request proto.Message = &pb.ScanVerticesRequest{Prefix: "orders:", Limit: 1}
		var response proto.Message = &pb.ScanVerticesResponse{NextCursor: []byte("inner")}
		if keys {
			request = &pb.ScanVertexKeysRequest{Prefix: "orders:", Limit: 1}
			response = &pb.ScanVertexKeysResponse{NextCursor: []byte("inner")}
		}
		binding, err := dataCursorBinding(request)
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := s.mapDataResponse(response, binding)
		if err != nil {
			t.Fatal(err)
		}
		cursor := wrapped.ProtoReflect().Get(wrapped.ProtoReflect().Descriptor().Fields().ByName("next_cursor")).Bytes()
		m := request.ProtoReflect()
		m.Set(m.Descriptor().Fields().ByName("cursor"), protoreflect.ValueOfBytes(cursor))
		m.Set(m.Descriptor().Fields().ByName("order"), protoreflect.ValueOfEnum(protoreflect.EnumNumber(pb.ScanOrder_SCAN_ORDER_ASC)))
		if _, _, err := s.mapDataRequest(request); err != nil {
			t.Fatal("equivalent ascending resume", keys, err)
		}
		m.Set(m.Descriptor().Fields().ByName("order"), protoreflect.ValueOfEnum(protoreflect.EnumNumber(pb.ScanOrder_SCAN_ORDER_DESC)))
		if _, _, err := s.mapDataRequest(request); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("changed direction accepted", err)
		}
	}
	for _, tc := range []struct {
		cursor []byte
		scoped bool
		reason pb.SearchErrorReason
		code   connect.Code
	}{
		{[]byte{1}, false, pb.SearchErrorReason_SEARCH_CURSOR_INVALID, connect.CodeInvalidArgument},
		{bytes.Repeat([]byte{3}, 40), false, pb.SearchErrorReason_SEARCH_CURSOR_INVALID, connect.CodeInvalidArgument},
		{bytes.Repeat([]byte{3}, 40), true, pb.SearchErrorReason_SEARCH_CURSOR_STALE, connect.CodeAborted},
	} {
		var scope [][32]byte
		if tc.scoped {
			scope = append(scope, [32]byte{1})
		}
		_, _, err := s.mapDataRequest(&pb.SearchVerticesRequest{Prefix: "orders:", Query: "term", Cursor: tc.cursor}, scope...)
		if connect.CodeOf(err) != tc.code {
			t.Fatal(err)
		}
		ce, ok := err.(*connect.Error)
		if !ok || len(ce.Details()) != 1 {
			t.Fatal("missing typed Search detail", err)
		}
		detail, err := ce.Details()[0].Value()
		if err != nil || detail.(*pb.SearchErrorDetail).Reason != tc.reason {
			t.Fatal(detail, err)
		}
	}
}
