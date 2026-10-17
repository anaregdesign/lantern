package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

type physicalFixtureCredentials struct {
	ObserverTokens []string  `json:"observer_tokens"`
	WriterToken    string    `json:"writer_token"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func runPhysicalFixtureServing(t *testing.T, ctx context.Context, provisionDir, endpoint string, control graphv1connect.LanternSecurityServiceClient, transport *http.Client, options *nativeFixtureServingOptions) {
	t.Helper()
	var credentials physicalFixtureCredentials
	raw, err := os.ReadFile(filepath.Join(provisionDir, "physical-credentials.json"))
	if err != nil || json.Unmarshal(raw, &credentials) != nil || len(credentials.ObserverTokens) != 2 || credentials.ObserverTokens[0] == credentials.ObserverTokens[1] || credentials.WriterToken == "" {
		t.Fatal("complete original physical machine credentials required")
	}
	data := graphv1connect.NewLanternServiceClient(transport, endpoint)
	var identity *pb.SecurityIdentity
	for _, token := range credentials.ObserverTokens {
		principal, err := control.GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if err != nil || principal.Msg.GetIdentity().GetMachineName() != "public-worker" || principal.Msg.GetUserId() != "" {
			t.Fatal("physical observer is not the original current machine", err)
		}
		if identity != nil && !proto.Equal(identity, principal.Msg.Identity) {
			t.Fatal("physical rotation changed registered identity")
		}
		identity = principal.Msg.Identity
		for _, prefix := range []string{"mobile-smoke:", "physical-api:", "physical-cdc:", "physical-contribution:", "physical-receipt:", "physical-head:", "flutter-demo:"} {
			key := prefix + "fixture-preflight"
			if _, err := data.PutVertex(ctx, securityWireRequest(token, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "synthetic fixture"}}})); err != nil {
				t.Fatal("physical scoped write", err)
			}
			if _, err := data.GetVertex(ctx, securityWireRequest(token, &pb.GetVertexRequest{Key: key})); err != nil {
				t.Fatal("physical scoped read", err)
			}
			if _, err := data.Illuminate(ctx, securityWireRequest(token, &pb.IlluminateRequest{Seed: key, VertexPrefix: prefix, Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 2}}})); err != nil {
				t.Fatal("physical scoped Query", err)
			}
			if _, err := data.DeleteVertex(ctx, securityWireRequest(token, &pb.DeleteVertexRequest{Key: key})); err != nil {
				t.Fatal("physical scoped delete", err)
			}
		}
		if _, err := data.GetVertex(ctx, securityWireRequest(token, &pb.GetVertexRequest{Key: "outside:physical-fixture-denied"})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("physical observer escaped synthetic prefixes", err)
		}
	}
	observer := credentials.ObserverTokens[0]
	capability, err := data.GetReceiptCapability(ctx, securityWireRequest(observer, &pb.GetReceiptCapabilityRequest{}))
	if err != nil || !capability.Msg.GetEnabled() || capability.Msg.GetPolicy().GetRetentionMs() != uint64((6*time.Hour).Milliseconds()) ||
		capability.Msg.GetPolicy().GetMaxEntries() != 1024 || capability.Msg.GetPolicy().GetMaxBytes() != 32<<20 {
		t.Fatal("physical certified receipt policy mismatch", err)
	}
	families := map[pb.ReceiptMutationKind]bool{}
	for _, family := range capability.Msg.GetSupportedMutations() {
		families[family] = true
	}
	for _, family := range []pb.ReceiptMutationKind{pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_PUT_VERTEX, pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_VERTEX, pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_EDGE, pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_ADD_EDGE, pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_EDGE_CONTRIBUTION} {
		if !families[family] {
			t.Fatal("physical receipt family unavailable")
		}
	}
	streamCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	stream, err := graphv1connect.NewLanternChangeServiceClient(transport, endpoint).WatchChanges(streamCtx, securityWireRequest(observer, &pb.WatchChangesRequest{Prefix: "physical-cdc:", Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_VALUE}))
	if err != nil {
		stop()
		t.Fatal("physical WatchChanges", err)
	}
	if !stream.Receive() || !stream.Msg().Bootstrap || len(stream.Msg().Cursor) == 0 {
		stop()
		_ = stream.Close()
		t.Fatal("physical WatchChanges bootstrap unavailable")
	}
	stop()
	_ = stream.Close()
	head := "physical-head:heads:fixture-preflight"
	tail := "physical-head:tails:fixture-preflight"
	for _, key := range []string{head, tail} {
		if _, err := data.PutVertex(ctx, securityWireRequest(observer, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}})); err != nil {
			t.Fatal("physical Head endpoint seed", err)
		}
	}
	writer := credentials.WriterToken
	for _, key := range []string{head, "physical-head:unreadable:fixture-preflight"} {
		if _, err := data.GetVertex(ctx, securityWireRequest(writer, &pb.GetVertexRequest{Key: key})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("physical Head writer gained head read", err)
		}
	}
	for _, key := range []string{tail, "physical-head:heads:denied:fixture-preflight"} {
		if _, err := data.PutVertex(ctx, securityWireRequest(writer, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("physical Head writer escaped write/Deny scope", err)
		}
	}
	if _, err := data.PutEdge(ctx, securityWireRequest(writer, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: tail, Head: head, Weight: 1}})); err != nil {
		t.Fatal("physical Head write-only edge", err)
	}
	if _, err := data.DeleteVertex(ctx, securityWireRequest(writer, &pb.DeleteVertexRequest{Key: head})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("physical Head writer gained VertexDelete", err)
	}
	if _, err := data.GetEdge(ctx, securityWireRequest(observer, &pb.GetEdgeRequest{Tail: tail, Head: head})); err != nil {
		t.Fatal("physical independent observer cannot verify Head effect", err)
	}
	if _, err := data.DeleteEdge(ctx, securityWireRequest(observer, &pb.DeleteEdgeRequest{Tail: tail, Head: head})); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{head, tail} {
		if _, err := data.DeleteVertex(ctx, securityWireRequest(observer, &pb.DeleteVertexRequest{Key: key})); err != nil {
			t.Fatal(err)
		}
	}
	until := time.Now().UTC().Add(options.Lifetime)
	if !until.Add(15*time.Second).Before(options.MembershipExpires) || !credentials.ExpiresAt.After(until) {
		t.Fatal("signed physical fixture membership/credentials have insufficient remaining lifetime")
	}
	export := map[string]any{"schema": 1, "kind": "current-physical-serving", "endpoint": options.Origin, "ca_file": "ca.pem", "serving_until": until, "credentials": credentials,
		"receipt_retention_ms": capability.Msg.Policy.RetentionMs, "receipt_max_entries": capability.Msg.Policy.MaxEntries, "receipt_max_bytes": capability.Msg.Policy.MaxBytes}
	raw, err = json.MarshalIndent(export, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := createPrivateTestFile(filepath.Join(options.ExportDir, "fixture.json"), append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := createPrivateTestFile(filepath.Join(options.ExportDir, "ready"), []byte("current physical fixture schema 1\n")); err != nil {
		t.Fatal(err)
	}
	t.Log("physical_fixture_ready schema=1 observer_credentials=2 writer_credentials=1")
	reason, err := waitNativeFixtureServing(ctx, filepath.Join(options.ExportDir, "release"), options.Lifetime, func() error {
		healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		got, err := control.GetCurrentPrincipal(healthCtx, securityWireRequest(observer, &pb.GetCurrentPrincipalRequest{}))
		if err != nil {
			return err
		}
		if !proto.Equal(got.Msg.Identity, identity) {
			return errors.New("physical fixture identity changed during serving")
		}
		return nil
	})
	if err != nil {
		t.Fatal("physical serving owner lifetime", err)
	}
	t.Log("physical_fixture_stopped reason=" + reason)
}
