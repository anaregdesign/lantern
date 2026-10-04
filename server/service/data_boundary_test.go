package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestDataUnaryBlindHandlingAndPublicationFence(t *testing.T) {
	clock, contexts := dataAccessFixture(t, []security.PermissionRule{
		dataAccessRule("read", security.Allow, security.VertexRead, "tails:"),
		dataAccessRule("write", security.Allow, security.VertexWrite, "heads:"),
	})
	now := clock()
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace().WithDataAuthorization(func() time.Time { return now })
	ctx := contexts("reader", now)
	request := connect.NewRequest(&pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "tails:a", Head: "heads:b", Weight: 3}}})
	for _, outcome := range []struct {
		failure error
		code    connect.Code
	}{
		{nil, 0},
		{connect.NewError(connect.CodeAlreadyExists, mutationreceipt.ErrIntentConflict), 0},
		{connect.NewError(connect.CodeFailedPrecondition, mutationreceipt.ErrNoLongerProvable), 0},
		{connect.NewError(connect.CodeInvalidArgument, mutationreceipt.ErrInvalidID), connect.CodeInvalidArgument},
		{connect.NewError(connect.CodeInternal, errors.New("WAL commit failed")), connect.CodeInternal},
	} {
		calls := 0
		response, err := dataUnary(ctx, request, svc, func(_ context.Context, mapped *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
			calls++
			if mapped.Edges[0].Tail != "data:tails:a" || mapped.Edges[0].Head != "data:heads:b" {
				t.Fatal("namespace conversion drift")
			}
			if outcome.failure != nil {
				return nil, outcome.failure
			}
			return &pb.AddEdgesResponse{Written: 1, EffectiveWeights: []float32{17}}, nil
		})
		if calls != 1 {
			t.Fatal("blind handling was replayed or skipped")
		}
		if outcome.code != 0 {
			if connect.CodeOf(err) != outcome.code || response != nil {
				t.Fatal("public failure was masked", response, err)
			}
		} else if err != nil || response.Msg.GetAcceptance().GetKind() != pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED || response.Msg.Written != 0 || len(response.Msg.EffectiveWeights) != 0 {
			t.Fatal("blind handling disclosed effects", response, err)
		}
	}
	_, err := dataUnary(ctx, connect.NewRequest(&pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "other:a", Head: "heads:b"}}}), svc, func(context.Context, *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
		t.Fatal("unauthorized input reached mutation handling")
		return nil, nil
	})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("policy rejection was hidden", err)
	}
	response, err := dataUnary(ctx, request, svc, func(context.Context, *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
		now = now.Add(2 * time.Hour)
		return &pb.AddEdgesResponse{Written: 1, EffectiveWeights: []float32{17}}, nil
	})
	if response != nil || connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("expired admission published acceptance", response, err)
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
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
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
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
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
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
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
