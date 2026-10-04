package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func TestChangeProjectionExactScopesAndCurrentImage(t *testing.T) {
	handler, _, contexts := securityAPIFixture(t)
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: "reader"}
	var rules []*pb.SecurityRule
	for i, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY, pb.SecurityAction_SECURITY_ACTION_CDC_VALUE, pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_EDGE_READ} {
		rules = append(rules, &pb.SecurityRule{Id: string(rune('a' + i)), Action: action, Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:"}})
	}
	rules = append(rules, &pb.SecurityRule{Id: "private", Action: pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY, Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:private:"}}, &pb.SecurityRule{Id: "unreadable", Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:unreadable:"}})
	_, err := handler.ApplySecurityChanges(contexts("admin", handler.now()), connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{31}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "cdc", Rules: rules}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "cdc"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewLanternService(cache).WithDataNamespace().WithDataAuthorization(handler.now)
	ctx := contexts("reader", handler.now())
	admission, _ := security.AdmissionFromContext(ctx)
	for _, key := range []string{"orders:1", "orders:private:1", "orders:unreadable:1", "other:1"} {
		if err := cache.PutVertex("data:"+key, &pb.Vertex{Key: "data:" + key, Value: &pb.Vertex_String_{String_: "current-" + key}}); err != nil {
			t.Fatal(err)
		}
	}
	mutation := &pb.Mutation{Origin: bytes.Repeat([]byte{1}, 16), Seq: 1, Hlc: &pb.HLCTimestamp{NodeId: bytes.Repeat([]byte{1}, 16)}, Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "data:orders:1"}, {Key: "data:orders:private:1"}, {Key: "data:orders:unreadable:1"}, {Key: "data:other:1"}, {Key: "sys:control"}}}}}}
	for _, projection := range []pb.ChangeProjection{pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, pb.ChangeProjection_CHANGE_PROJECTION_VALUE} {
		p := newChangeProjection(svc, &pb.WatchChangesRequest{Prefix: "orders:", Projection: projection}, admission)
		var frames []*pb.WatchChangesResponse
		advanced := 0
		wantItems := 2
		if projection == pb.ChangeProjection_CHANGE_PROJECTION_VALUE {
			wantItems = 1
		}
		err := p.project(ctx, mutation, func(visible bool) ([]byte, error) {
			if !visible {
				t.Fatal("visible progress omitted")
			}
			advanced++
			return []byte("opaque"), nil
		}, func(frame *pb.WatchChangesResponse) error { frames = append(frames, frame); return nil })
		if err != nil || advanced != 1 || len(frames) != 1 || len(frames[0].Invalidations) != wantItems || string(frames[0].Cursor) != "opaque" {
			t.Fatal(projection, frames, advanced, err)
		}
		for _, item := range frames[0].Invalidations {
			if strings.HasPrefix(item.GetVertexKey(), "data:") || strings.HasPrefix(item.GetVertexKey(), "sys:") {
				t.Fatal("physical/system identity leaked", item)
			}
			if projection == pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY && item.GetCurrentImage() != nil {
				t.Fatal("identity mode exposed a value", item)
			}
			if projection == pb.ChangeProjection_CHANGE_PROJECTION_VALUE && (item.GetVertex() == nil || item.GetVertex().GetKey() != item.GetVertexKey() || item.GetVertex().GetString_() != "current-"+item.GetVertexKey()) {
				t.Fatal("value mode sampled outside current authorized image", item)
			}
		}
	}
	// Both endpoints must qualify independently; hidden-only mutations still
	// advance internal progress without an observable extra frame.
	edges := &pb.Mutation{Origin: mutation.Origin, Seq: 2, Hlc: mutation.Hlc, Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "data:orders:1", Head: "data:orders:private:1"}, {Tail: "data:orders:1", Head: "data:other:1"}}}}}}
	p := newChangeProjection(svc, &pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}, admission)
	advanced := false
	if err := p.project(ctx, edges, func(visible bool) ([]byte, error) {
		if visible {
			t.Fatal("hidden progress requested an encrypted output")
		}
		advanced = true
		return nil, nil
	}, func(*pb.WatchChangesResponse) error { t.Fatal("hidden-only frame"); return nil }); err != nil || !advanced {
		t.Fatal("hidden progress", err)
	}
	// Predicate Deletes cannot prove exact victims and must require rebootstrap.
	edges.Op = &pb.MutationOp{Op: &pb.MutationOp_DeleteEdgesByPrefix{DeleteEdgesByPrefix: &pb.DeleteEdgesByPrefixRequest{TailPrefix: "data:orders:"}}}
	if err := p.project(ctx, edges, func(bool) ([]byte, error) { t.Fatal("unproven progress advanced"); return nil, nil }, func(*pb.WatchChangesResponse) error { return nil }); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal(err)
	}
}

func BenchmarkChangeProjectionMixedScopes(b *testing.B) {
	for _, hidden := range []bool{false, true} {
		name := "visible"
		if hidden {
			name = "hidden_heavy"
		}
		b.Run(name, func(b *testing.B) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			p := newChangeProjection(NewLanternService(cache), &pb.WatchChangesRequest{Prefix: "orders:", Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}, nil)
			vertices := make([]*pb.Vertex, 100)
			for i := range vertices {
				key := "data:orders:visible"
				if hidden && i > 0 {
					key = "data:private:hidden"
				}
				vertices[i] = &pb.Vertex{Key: key}
			}
			m := &pb.Mutation{Origin: bytes.Repeat([]byte{1}, 16), Seq: 1, Hlc: &pb.HLCTimestamp{NodeId: bytes.Repeat([]byte{1}, 16)}, Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: vertices}}}}
			b.ReportAllocs()
			for b.Loop() {
				if err := p.project(context.Background(), m, func(bool) ([]byte, error) { return nil, nil }, func(frame *pb.WatchChangesResponse) error { _ = proto.Size(frame); return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestChangeProjectionDirectedPairRequiresCompleteSelector(t *testing.T) {
	for _, value := range []bool{false, true} {
		rules := []security.PermissionRule{
			dataAccessPairRule("cdc", security.Allow, security.CDCIdentity, "users:", "targets:"),
			dataAccessPairRule("private", security.Deny, security.CDCIdentity, "users:", "targets:private:"),
		}
		projection := pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY
		if value {
			projection = pb.ChangeProjection_CHANGE_PROJECTION_VALUE
			rules = append(rules, dataAccessRule("read", security.Allow, security.VertexRead, ""),
				dataAccessPairRule("edge-read", security.Allow, security.EdgeRead, "users:", "targets:"),
				dataAccessPairRule("value", security.Allow, security.CDCValue, "users:", "targets:"))
		}
		clock, contexts := dataAccessFixture(t, rules)
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		svc := NewLanternService(cache).WithDataNamespace().WithDataAuthorization(clock)
		ctx := contexts("reader", clock())
		admission, _ := security.AdmissionFromContext(ctx)
		if !changeScope(admission.Access(), projection, false).Empty() {
			t.Fatal("pair CDC granted Vertex invalidations")
		}
		p := newChangeProjection(svc, &pb.WatchChangesRequest{Projection: projection}, admission)
		for _, test := range []struct {
			tail, head string
			allowed    bool
		}{{"users:1", "targets:1", true}, {"targets:1", "users:1", false}, {"users:1", "users:2", false}, {"users:1", "targets:private:1", false}, {"users:1", "outside:1", false}} {
			cache.AddEdge("data:"+test.tail, "data:"+test.head, 7)
			item, err := p.edge(ctx, &pb.EdgeKey{Tail: "data:" + test.tail, Head: "data:" + test.head})
			if err != nil || (item != nil) != test.allowed || item != nil && (item.GetCurrentImage() != nil) != value {
				t.Fatal("CDC pair disclosed reverse/hidden value or required unrelated read", value, test, item, err)
			}
		}
	}
}
