package integration_test

// This opt-in native gate crosses production provisioning, provider admission,
// service codecs/output ownership and public SDK transport. Ordinary unit runs
// do not substitute fixture clocks for a configured native time source.
import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestCurrentSecurityPublicNativeGate(t *testing.T) {
	if os.Getenv("LANTERN_CURRENT_PUBLIC_GATE") != "1" {
		t.Skip("requires configured native time; explicit container gate")
	}
	f := newOIDCWireIssuer(t)
	f.realClock = true
	dir := t.TempDir()
	var command *exec.Cmd
	if binary := os.Getenv("LANTERN_CURRENT_FIXTURE_EXPORTER"); binary != "" {
		command = exec.CommandContext(t.Context(), binary, "-test.run=^TestCurrentProvisioningExportPublicFixture$", "-test.v")
	} else {
		command = exec.CommandContext(t.Context(), "go", "test", "./server/internal/security", "-run", "^TestCurrentProvisioningExportPublicFixture$", "-count=1", "-v")
		command.Dir = filepath.Join("..", "..")
	}
	command.Env = append(os.Environ(), "LANTERN_CURRENT_FIXTURE_DIR="+dir, "LANTERN_CURRENT_FIXTURE_ISSUER="+f.provider.URL)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("independent provisioning export: %v\n%s", err, out)
	}
	roots := filepath.Join(dir, "oidc.pem")
	if err := createPrivateTestFile(roots, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.provider.Certificate().Raw})); err != nil {
		t.Fatal(err)
	}
	var servers []*httptest.Server
	var publicCA string
	var controls []graphv1connect.LanternSecurityServiceClient
	var runtimes []*provider.SecurityRuntime
	var stops []func()
	transport := &http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}
	startNode := func(id int, mode string) {
		graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		graph.EnablePrefixIndex(func(key string) string { return key })
		var data *service.ServingRuntime
		var err error
		if id == 1 {
			config := durableReceiptWireConfig(filepath.Join(dir, "data-receipts.wal"), hlc.NodeID{byte(id)})
			config.NamespaceFormat = "namespaced-v1"
			data, err = service.CreateDurableReceiptWALServingRuntime(config)
		} else {
			data, err = service.NewGraphOnlyServingRuntime(graph, mutationlog.New(mutationlog.Options{Capacity: 64}), hlc.New(hlc.NodeID{byte(id)}, hlc.Options{}), "namespaced-v1")
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = data.Close() })
		runtime, closeRuntime, err := provider.NewSecurityRuntime(provider.SecurityConfig{Mode: "oidc", Profile: "current-v2", CurrentConfigFile: filepath.Join(dir, fmt.Sprintf("node-%d/node.json", id)), StoreMode: mode, BrowserOrigin: "https://admin.example", RootCAFile: roots, PrivateOrigins: map[string][]netip.Prefix{f.provider.URL: {netip.MustParsePrefix("127.0.0.1/32")}}, TrustedProxyIPs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}, data)
		if err != nil {
			t.Fatal("production current constructor", id, err)
		}
		t.Cleanup(closeRuntime)
		runtimes = append(runtimes, runtime)
		stops = append(stops, closeRuntime)
		svc := data.NewLanternService(nil)
		if id == 1 {
			svc.WithTombstoneTTL(time.Hour)
		}
		peer, err := data.NewLanternReplicationService(svc)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := provider.NewRuntimeRestored(data, svc)
		if err != nil {
			t.Fatal(err)
		}
		limits := provider.NetConfig{MaxRecvMsgBytes: 1 << 20, MaxSendMsgBytes: 1 << 20, MaxConcurrentStreams: 16}
		certified, err := provider.NewRuntimeCertified(data, svc, peer, restored, limits)
		if err != nil {
			t.Fatal(err)
		}
		if id == 1 {
			if err := data.CertifyReceiptBackup(svc, peer); err != nil {
				t.Fatal(err)
			}
			if err := data.ActivatePublicReceipts(svc, peer); err != nil {
				t.Fatal(err)
			}
		}
		public, err := provider.NewPublicSecurityCertified(runtime, provider.TLSConfig{}, nil, nil, data, svc, certified)
		if err != nil {
			t.Fatal(err)
		}
		s := httptest.NewUnstartedServer(nil)
		listener, err := provider.NewPublicLanternListener(s.Listener, limits, provider.TLSConfig{}, provider.ObservabilityConfig{}, provider.CORSConfig{}, svc, runtime, provider.ChangeConfig{Enabled: true, Options: service.ChangeServiceOptions{CursorKeys: []service.ChangeCursorKey{{Version: 1, Key: [32]byte{13}}}, CurrentKeyVersion: 1, Heartbeat: 100 * time.Millisecond}}, nil, nil, nil, nil, nil, provider.NewHealthChecker(), slog.New(slog.NewTextHandler(io.Discard, nil)), public)
		if err != nil {
			t.Fatal(err)
		}
		// Keep the actual owned http.Server, including ConnContext/ConnState and h2
		// budgets; copying only Handler would bypass production connection credit.
		s.Config = listener.Server()
		if os.Getenv("LANTERN_CURRENT_PUBLIC_SDK4") == "1" {
			s.EnableHTTP2 = true
			ca := configureSDKServerTLS(t, s)
			if id == 1 {
				publicCA = ca
			}
			s.StartTLS()
		} else {
			s.Start()
		}
		t.Cleanup(s.Close)
		servers = append(servers, s)
		controls = append(controls, graphv1connect.NewLanternSecurityServiceClient(transport, s.URL))
	}
	for id := 1; id <= 3; id++ {
		startNode(id, "fresh")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	for i, runtime := range runtimes {
		for !runtime.Ready(ctx) {
			if ctx.Err() != nil {
				t.Fatal("native time/quorum readiness", i, ctx.Err())
			}
			select {
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	t.Log("production native constructor: independently provisioned three voters, two origins, observer; no injected clock or H")
	token := f.token(t, "admin", func(c map[string]any) {
		c["iat"] = time.Now().Add(-time.Minute).Unix()
		c["auth_time"] = time.Now().Add(-time.Hour).Unix()
	})
	if os.Getenv("LANTERN_CURRENT_PUBLIC_SDK4") == "1" {
		runCurrentPublicSDK4(t, servers[0], token, publicCA)
		return
	}
	var principal *pb.GetCurrentPrincipalResponse
	var ordinaryReview *pb.CurrentSecurityReview
	t.Run("public protocols and full binding", func(t *testing.T) {
		for _, mode := range []struct {
			name    string
			options []connect.ClientOption
			http    *http.Client
		}{{"h2-connect-proto", nil, transport}, {"h2-connect-json", []connect.ClientOption{connect.WithProtoJSON()}, transport}, {"h2-grpc", []connect.ClientOption{connect.WithGRPC()}, transport}, {"h2-grpc-web", []connect.ClientOption{connect.WithGRPCWeb()}, transport}, {"http1-connect-json", []connect.ClientOption{connect.WithProtoJSON()}, &http.Client{Transport: authIngressRoundTripper{http.DefaultTransport}}}} {
			t.Run(mode.name, func(t *testing.T) {
				c := graphv1connect.NewLanternSecurityServiceClient(mode.http, servers[0].URL, mode.options...)
				result, e := c.GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = client.CurrentSecurityVersionBinding(result.Msg.Version); e != nil {
					t.Fatal(e)
				}
				principal = result.Msg
			})
		}
	})
	if principal == nil {
		t.Fatal("no qualified principal")
	}
	t.Run("ordinary review apply foreign status", func(t *testing.T) {
		before := principal.Version
		ordinaryToken := f.token(t, "admin", func(c map[string]any) { c["iat"] = time.Now().Add(-time.Minute).Unix(); delete(c, "auth_time") })
		change := &pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "wire_ordinary", Name: "Ordinary no recent-auth age gate"}}}
		prepared, e := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(ordinaryToken, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: before.CurrentProfile, ExpectedCut: before.CurrentCut, Changes: []*pb.SecurityChange{change, {Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: principal.Identity, RoleId: "wire_ordinary"}}}}}}))
		if e != nil {
			t.Fatal("prepare", e)
		}
		review := prepared.Msg.CurrentReview
		ordinaryReview = proto.Clone(review).(*pb.CurrentSecurityReview)
		if review == nil || review.ChangeId == nil || len(review.IntentDigest) != 32 || prepared.Msg.Requirement != pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY {
			t.Fatal("full server-owned review", prepared.Msg)
		}
		applied, e := controls[0].ApplySecurityChanges(ctx, securityWireRequest(ordinaryToken, &pb.ApplySecurityChangesRequest{CurrentReview: review}))
		var original *pb.CurrentSecurityOriginalOutcome
		if e == nil {
			original = applied.Msg.GetCurrentResult().GetOriginal()
		}
		if original == nil {
			original = awaitCurrentPublicOriginal(t, ctx, controls[0], token, review)
		}
		if original.GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			t.Fatal("original apply", original)
		}
		for i, c := range controls {
			// Apply invalidates old renewal; only status reads are retried while
			// the regular native worker obtains authority for the new cut.
			deadline := time.Now().Add(15 * time.Second)
			for {
				status, e := c.GetSecurityChangeStatus(ctx, securityWireRequest(token, &pb.GetSecurityChangeStatusRequest{CurrentProfile: review.Profile, CurrentChangeId: review.ChangeId, CurrentIntentDigest: review.IntentDigest}))
				if e == nil && proto.Equal(status.Msg.GetCurrentResult().GetOriginal(), original) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("exact foreign original", i, status, e)
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		current, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if e != nil {
			t.Fatal(e)
		}
		b1, _ := client.CurrentSecurityVersionBinding(before)
		b2, e := client.CurrentSecurityVersionBinding(current.Msg.Version)
		if e != nil || b1 == b2 || proto.Equal(before.CurrentCut, current.Msg.Version.CurrentCut) {
			t.Fatal("post-Apply cut recapture", e)
		}
		if _, e := controls[0].ApplySecurityChanges(ctx, securityWireRequest(token, &pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{1}, 16), Changes: []*pb.SecurityChange{change}})); connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatal("legacy scalar refusal", e)
		}
	})
	t.Run("full-cut and profile mismatch refuse before dispatch", func(t *testing.T) {
		current, err := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		changes := []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "never_installed"}}}}
		for _, edit := range []func(*pb.CurrentSemanticCut){
			func(c *pb.CurrentSemanticCut) { c.Sequence++ },
			func(c *pb.CurrentSemanticCut) { c.Previous[0] ^= 1 },
			func(c *pb.CurrentSemanticCut) { c.Projection[0] ^= 1 },
			func(c *pb.CurrentSemanticCut) { c.Frontier[0] ^= 1 },
			func(c *pb.CurrentSemanticCut) { c.Fences[0] ^= 1 },
			func(c *pb.CurrentSemanticCut) { c.Policy[0] ^= 1 },
		} {
			cut := proto.Clone(current.Msg.Version.CurrentCut).(*pb.CurrentSemanticCut)
			edit(cut)
			_, err := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Msg.Version.CurrentProfile, ExpectedCut: cut, Changes: changes}}))
			if connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("partial cut accepted", err)
			}
		}
		profile := proto.Clone(current.Msg.Version.CurrentProfile).(*pb.CurrentAuthorityProfile)
		profile.Protocol[0] ^= 1
		_, err = controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: profile, ExpectedCut: current.Msg.Version.CurrentCut, Changes: changes}}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("foreign protocol accepted", err)
		}
		_, err = controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Msg.Version.CurrentProfile, ExpectedCut: current.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: current.Msg.Identity, RoleId: "security_admin"}}}}}}))
		if err == nil {
			t.Fatal("last qualified admin removal accepted")
		}
		expansion, err := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Msg.Version.CurrentProfile, ExpectedCut: current.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "blocked_admin", Rules: []*pb.SecurityRule{{Id: "allow", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_MANAGE, Resource: &pb.SecurityRule_Global{Global: true}}}}}}}}}))
		if err != nil || expansion.Msg.Requirement != pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION {
			t.Fatal("removing Deny hid effective manage expansion", err)
		}
		after, err := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if err != nil || !proto.Equal(current.Msg.Version.CurrentCut, after.Msg.Version.CurrentCut) {
			t.Fatal("refused preparation changed cut", err)
		}
	})
	t.Run("sdk and typed data response", func(t *testing.T) {
		sdk, e := client.NewLantern(servers[0].URL, client.WithHTTPClient(transport), client.WithAuthToken(token))
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = sdk.Close() }()
		if _, e = sdk.GetCurrentPrincipal(ctx); e != nil {
			t.Fatal(e)
		}
		c := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
		if _, e = c.PutVertices(ctx, securityWireRequest(token, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "orders:a", Value: &pb.Vertex_String_{String_: "visible"}}, {Key: "orders:b", Value: &pb.Vertex_String_{String_: "second"}}}})); e != nil {
			t.Fatal(e)
		}
		got, e := c.GetVertices(ctx, securityWireRequest(token, &pb.GetVerticesRequest{Keys: []string{"orders:a", "orders:b"}}))
		if e != nil || len(got.Msg.Vertices) != 2 {
			t.Fatal("bounded batch", got, e)
		}
		if _, e = c.GetVertices(ctx, securityWireRequest(token, &pb.GetVerticesRequest{Keys: []string{"orders:a", "other:denied"}})); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("whole batch authority", e)
		}
	})
	t.Run("reader explicit Deny and no write grant", func(t *testing.T) {
		reader := f.token(t, "reader", func(c map[string]any) { c["iat"] = time.Now().Add(-time.Minute).Unix(); delete(c, "auth_time") })
		c := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
		if _, err := c.GetVertex(ctx, securityWireRequest(reader, &pb.GetVertexRequest{Key: "orders:a"})); err != nil {
			t.Fatal("reader exact grant", err)
		}
		for _, key := range []string{"orders:private:hidden", "other:denied"} {
			if _, err := c.GetVertex(ctx, securityWireRequest(reader, &pb.GetVertexRequest{Key: key})); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("reader Deny/prefix", err)
			}
		}
		if _, err := c.PutVertex(ctx, securityWireRequest(reader, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:reader-write"}})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("reader gained write", err)
		}
	})
	t.Run("machine data grants and management refusal", func(t *testing.T) {
		raw, e := os.ReadFile(filepath.Join(dir, "machine.token"))
		if e != nil {
			t.Fatal(e)
		}
		machine := string(raw)
		c := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
		if _, e := c.PutVertex(ctx, securityWireRequest(machine, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:machine", Value: &pb.Vertex_String_{String_: "explicit grant"}}})); e != nil {
			t.Fatal("machine data write", e)
		}
		principal, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(machine, &pb.GetCurrentPrincipalRequest{}))
		if e != nil {
			t.Fatal("machine reference read", e)
		}
		_, e = controls[0].PrepareSecurityChanges(ctx, securityWireRequest(machine, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: principal.Msg.Version.CurrentProfile, ExpectedCut: principal.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_DeleteRole{DeleteRole: "reader"}}}}}))
		if connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("machine management mutation", e)
		}
	})
	t.Run("scoped receipt export and CDC cut invalidation", func(t *testing.T) {
		c := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
		cap, e := c.GetReceiptCapability(ctx, securityWireRequest(token, &pb.GetReceiptCapabilityRequest{}))
		if e != nil || !cap.Msg.GetEnabled() {
			t.Fatal("current receipt capability", e)
		}
		var epoch mutationreceipt.Epoch
		copy(epoch[:], cap.Msg.Policy.DeploymentEpoch)
		id, e := mutationreceipt.NewID(epoch, time.UnixMilli(int64(cap.Msg.ServerNowUnixMs)), [24]byte{17})
		if e != nil {
			t.Fatal(e)
		}
		request := &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:receipt", Value: &pb.Vertex_String_{String_: "original"}}, ReceiptContext: &pb.MutationReceiptContext{Endpoint: cap.Msg.Endpoint, LogicalCallId: bytes.Repeat([]byte{17}, 16), OperationIds: [][]byte{id.Bytes()}}}
		put, e := c.PutVertex(ctx, securityWireRequest(token, request))
		if e != nil {
			t.Fatal("receipt write", e)
		}
		status, e := c.GetReceiptStatus(ctx, securityWireRequest(token, &pb.GetReceiptStatusRequest{OperationId: id.Bytes()}))
		if e != nil || status.Msg.GetStatus().GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED {
			t.Fatal("typed receipt disclosure", e)
		}
		replay, e := c.PutVertex(ctx, securityWireRequest(token, request))
		if e != nil || !proto.Equal(put.Msg, replay.Msg) {
			t.Fatal("original receipt replay", e)
		}
		snapshot, e := c.BackupSnapshot(ctx, securityWireRequest(token, &pb.BackupSnapshotRequest{VertexPrefix: "orders:"}))
		if e != nil {
			t.Fatal(e)
		}
		rows := 0
		for snapshot.Receive() {
			if v := snapshot.Msg().GetVertex(); v != nil {
				rows++
				if !strings.HasPrefix(v.Key, "orders:") {
					t.Fatal("export escaped scope")
				}
			}
		}
		if snapshot.Err() != nil || rows < 3 {
			t.Fatal("scoped export", rows, snapshot.Err())
		}
		changes := graphv1connect.NewLanternChangeServiceClient(transport, servers[0].URL)
		valueCtx, valueStop := context.WithTimeout(ctx, 3*time.Second)
		values, e := changes.WatchChanges(valueCtx, securityWireRequest(token, &pb.WatchChangesRequest{Prefix: "orders:", Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_VALUE}))
		if e != nil || !values.Receive() || !values.Msg().Bootstrap {
			valueStop()
			t.Fatal("value CDC bootstrap", e)
		}
		if _, e := c.PutVertex(ctx, securityWireRequest(token, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:value-stream", Value: &pb.Vertex_String_{String_: "stream-value"}}})); e != nil {
			t.Fatal(e)
		}
		seenValue := false
		for values.Receive() {
			for _, item := range values.Msg().Invalidations {
				if item.GetVertexKey() == "orders:value-stream" && item.GetVertex().GetString_() == "stream-value" {
					seenValue = true
				}
			}
			if seenValue {
				break
			}
		}
		valueStop()
		_ = values.Close()
		if !seenValue {
			t.Fatal("typed value CDC response", values.Err())
		}
		page, e := c.ScanVertexKeys(ctx, securityWireRequest(token, &pb.ScanVertexKeysRequest{Prefix: "orders:", Limit: 1}))
		if e != nil || len(page.Msg.NextCursor) == 0 {
			t.Fatal("query cursor", e)
		}
		oldQueryCursor := append([]byte(nil), page.Msg.NextCursor...)
		streamCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		stream, e := changes.WatchChanges(streamCtx, securityWireRequest(token, &pb.WatchChangesRequest{Prefix: "orders:", Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = stream.Close() }()
		if !stream.Receive() || !stream.Msg().Bootstrap || len(stream.Msg().Cursor) == 0 {
			t.Fatal("current CDC bootstrap", stream.Err())
		}
		cursor := bytes.Clone(stream.Msg().Cursor)
		principal, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if e != nil {
			t.Fatal(e)
		}
		prepared, e := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: principal.Msg.Version.CurrentProfile, ExpectedCut: principal.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "invalidate_cursor"}}}}}}))
		if e != nil {
			t.Fatal(e)
		}
		applied, e := controls[0].ApplySecurityChanges(ctx, securityWireRequest(token, &pb.ApplySecurityChangesRequest{CurrentReview: prepared.Msg.CurrentReview}))
		if e != nil || applied.Msg.GetCurrentResult().GetOriginal().GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			t.Fatal("cursor cut change", e)
		}
		for stream.Receive() {
			if len(stream.Msg().Invalidations) != 0 {
				t.Fatal("old stream published new scope data")
			}
		}
		if stream.Err() == nil {
			t.Fatal("old stream survived changed full cut")
		}
		resumed, e := changes.WatchChanges(ctx, securityWireRequest(token, &pb.WatchChangesRequest{Prefix: "orders:", Cursor: cursor, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
		if e == nil {
			for resumed.Receive() {
				t.Fatal("old cursor relabeled under fresh cut")
			}
			e = resumed.Err()
			_ = resumed.Close()
		}
		if e == nil {
			t.Fatal("old cursor accepted")
		}
		if _, e := c.ScanVertexKeys(ctx, securityWireRequest(token, &pb.ScanVertexKeysRequest{Prefix: "orders:", Limit: 1, Cursor: oldQueryCursor})); e == nil {
			t.Fatal("old query cursor relabeled under fresh cut")
		}
	})
	f.server = servers[0]
	t.Run("operation-bound real Code approval", func(t *testing.T) {
		current, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if e != nil {
			t.Fatal(e)
		}
		change := &pb.SecurityChange{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "bob"}, RoleId: "security_admin"}}}
		prepared, e := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Msg.Version.CurrentProfile, ExpectedCut: current.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{change}}}))
		if e != nil {
			t.Fatal(e)
		}
		if prepared.Msg.Requirement != pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION {
			t.Fatal("manage expansion lacked purpose requirement")
		}
		begin, e := controls[0].BeginSecurityChangeAuthorization(ctx, securityWireRequest(token, &pb.BeginSecurityChangeAuthorizationRequest{CurrentReview: prepared.Msg.CurrentReview}))
		if e != nil {
			t.Fatal(e)
		}
		target, e := url.Parse(begin.Msg.StartUrl)
		if e != nil {
			t.Fatal(e)
		}
		start := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, target.RequestURI(), nil, ""))
		if start.StatusCode != http.StatusFound {
			t.Fatal("purpose navigation", start.StatusCode)
		}
		path := f.browserCallbackPath(t, start, "admin", false)
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
		callback := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, start.Cookies(), ""))
		if callback.StatusCode != http.StatusOK {
			t.Fatal("purpose Code completion", callback.StatusCode)
		}
		for _, cookie := range callback.Cookies() {
			if cookie.Name != "__Host-lantern-login" || cookie.Value != "" {
				t.Fatal("purpose callback changed session")
			}
		}
		var proof []byte
		deadline := time.Now().Add(5 * time.Second)
		for {
			status, e := controls[0].GetSecurityChangeAuthorization(ctx, securityWireRequest(token, &pb.GetSecurityChangeAuthorizationRequest{CurrentProfile: prepared.Msg.CurrentReview.Profile, AuthorizationId: begin.Msg.AuthorizationId, AttemptAffinity: begin.Msg.AttemptAffinity}))
			if e != nil {
				t.Fatal("purpose read", e)
			}
			if status.Msg.State == pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_APPROVED {
				proof = status.Msg.AuthorizationProof
				break
			}
			if status.Msg.State != pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_PENDING || time.Now().After(deadline) {
				t.Fatal("purpose never became usable", status.Msg.State)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
		}
		if len(proof) != 32 {
			t.Fatal("incomplete purpose proof")
		}

		after, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if e != nil || !proto.Equal(after.Msg.Version.CurrentCut, current.Msg.Version.CurrentCut) {
			t.Fatal("purpose changed ordinary cut", e)
		}
		applied, e := controls[0].ApplySecurityChanges(ctx, securityWireRequest(token, &pb.ApplySecurityChangesRequest{CurrentReview: prepared.Msg.CurrentReview, AuthorizationProof: proof}))
		var original *pb.CurrentSecurityOriginalOutcome
		if e == nil {
			original = applied.Msg.GetCurrentResult().GetOriginal()
		}
		if original == nil {
			t.Log("post-Apply disclosure unavailable; retaining review and using status only", connect.CodeOf(e))
			original = awaitCurrentPublicOriginal(t, ctx, controls[0], token, prepared.Msg.CurrentReview)
		}
		if original.GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			t.Fatal("approved exact operation", original)
		}
		replay, e := controls[0].ApplySecurityChanges(ctx, securityWireRequest(token, &pb.ApplySecurityChangesRequest{CurrentReview: prepared.Msg.CurrentReview}))
		if e != nil || !proto.Equal(replay.Msg.GetCurrentResult().GetOriginal(), original) {
			t.Fatal("original retry demanded fresh purpose", e)
		}
		bob := f.token(t, "bob", func(c map[string]any) { c["iat"] = time.Now().Add(-time.Minute).Unix(); delete(c, "auth_time") })
		if _, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(bob, &pb.GetCurrentPrincipalRequest{})); e != nil {
			t.Fatal("new qualified administrator", e)
		}
		data := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
		if _, e := data.GetVertex(ctx, securityWireRequest(bob, &pb.GetVertexRequest{Key: "orders:a"})); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("security.manage implicitly granted data", e)
		}
	})
	t.Run("browser Code one exchange and CSRF bootstrap", func(t *testing.T) {
		start := f.browserLoginStart(t, nil, false)
		path := f.browserCallbackPath(t, start, "admin", false)
		beforeWrongNode := f.fetches.Load()
		f.server = servers[1]
		wrongNode := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, start.Cookies(), ""))
		_ = wrongNode.Body.Close()
		if wrongNode.StatusCode == http.StatusSeeOther || f.fetches.Load() != beforeWrongNode {
			t.Fatal("foreign attempt exchanged Code")
		}
		f.server = servers[0]
		// Simulate an IdP round trip. This delay is not qualification: the
		// production Consume still checks its own native lower endpoint.
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
		callback := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, start.Cookies(), ""))
		if callback.StatusCode != http.StatusSeeOther {
			body, _ := io.ReadAll(callback.Body)
			t.Fatalf("native callback %d: %s", callback.StatusCode, body)
		}
		var cookies []*http.Cookie
		for _, cookie := range callback.Cookies() {
			if cookie.Value != "" {
				cookies = append(cookies, cookie)
			}
		}
		if len(cookies) != 2 {
			t.Fatal("fresh session cookie missing", len(cookies))
		}
		session := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, ""))
		if session.StatusCode != http.StatusOK {
			t.Fatal("read-only CSRF bootstrap", session.StatusCode)
		}
		var current pb.BrowserSession
		raw, e := io.ReadAll(session.Body)
		if e != nil || protojson.Unmarshal(raw, &current) != nil || current.Principal.GetCsrfToken() == "" {
			t.Fatal("complete session response", e)
		}
		// An enrolled, caught-up origin-free node accepts the established
		// session. The request carries the same exact cookie and origin.
		f.server = servers[2]
		deadline := time.Now().Add(15 * time.Second)
		for {
			observed := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, ""))
			if observed.StatusCode == http.StatusOK {
				break
			}
			_ = observed.Body.Close()
			if time.Now().After(deadline) {
				t.Fatal("session observer never became current", observed.StatusCode)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		// Independent minimum floors plus unchanged M/P/B volumes reopen the
		// same member. Pending login transactions remain process-owned.
		f.server = servers[1]
		pending := f.browserLoginStart(t, nil, false)
		oldAttempt := f.browserCallbackPath(t, pending, "admin", false)
		floors, e := runtimes[1].ExportCurrentAuthorityFloors()
		if e != nil {
			t.Fatal("independent floor export", e)
		}
		floorPath := filepath.Join(dir, "node-2/floors.json")
		if e := createPrivateTestFile(floorPath, floors); e != nil {
			t.Fatal(e)
		}
		stops[1]()
		servers[1].Close()
		nodePath := filepath.Join(dir, "node-2/node.json")
		rawNode, e := os.ReadFile(nodePath)
		if e != nil {
			t.Fatal(e)
		}
		encodedPath, _ := json.Marshal(floorPath)
		updated := bytes.Replace(rawNode, []byte(`"FloorsFile":""`), append([]byte(`"FloorsFile":`), encodedPath...), 1)
		if bytes.Equal(rawNode, updated) {
			t.Fatal("resume floor path not installed")
		}
		if e := os.WriteFile(nodePath, updated, 0600); e != nil {
			t.Fatal(e)
		}
		startNode(2, "resume")
		last := len(runtimes) - 1
		runtimes[1], stops[1], servers[1], controls[1] = runtimes[last], stops[last], servers[last], controls[last]
		deadline = time.Now().Add(60 * time.Second)
		for !runtimes[1].Ready(ctx) {
			if time.Now().After(deadline) || ctx.Err() != nil {
				t.Fatal("intact resume failed fresh native qualification")
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		f.server = servers[1]
		beforeRestartCallback := f.fetches.Load()
		lostAttempt := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, oldAttempt, pending.Cookies(), ""))
		_ = lostAttempt.Body.Close()
		if lostAttempt.StatusCode == http.StatusSeeOther || f.fetches.Load() != beforeRestartCallback {
			t.Fatal("restart revived old Code attempt")
		}
		resumed := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, ""))
		_ = resumed.Body.Close()
		if resumed.StatusCode != http.StatusOK {
			t.Fatal("intact resume lost installed session", resumed.StatusCode)
		}
		f.server = servers[0]
		before := f.fetches.Load()
		replay := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, start.Cookies(), ""))
		if replay.StatusCode == http.StatusSeeOther || f.fetches.Load() != before {
			t.Fatal("Code callback replay reached IdP")
		}
		// A new explicit login replaces only the session present at its start.
		// The old cookies must not survive the fresh post-Apply publication.
		replacementStart := f.browserLoginStart(t, cookies, false)
		replacementPath := f.browserCallbackPath(t, replacementStart, "admin", false)
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
		replacement := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, replacementPath, replacementStart.Cookies(), ""))
		if replacement.StatusCode != http.StatusSeeOther {
			t.Fatal("replacement callback", replacement.StatusCode)
		}
		oldSession := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, ""))
		_ = oldSession.Body.Close()
		if oldSession.StatusCode == http.StatusOK {
			t.Fatal("replaced session remained active")
		}
		cookies = nil
		for _, cookie := range replacement.Cookies() {
			if cookie.Value != "" {
				cookies = append(cookies, cookie)
			}
		}
		freshSession := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, ""))
		freshBody, e := io.ReadAll(freshSession.Body)
		_ = freshSession.Body.Close()
		if e != nil || freshSession.StatusCode != http.StatusOK || protojson.Unmarshal(freshBody, &current) != nil {
			t.Fatal("replacement fresh session", e)
		}
		logout := func(message *pb.CurrentLogoutRequest, csrf string) *pb.SessionRevocation {
			raw, e := protojson.Marshal(message)
			if e != nil {
				t.Fatal(e)
			}
			request := f.browserRequest(t, http.MethodPost, "/auth/logout", cookies, csrf)
			request.Body = io.NopCloser(bytes.NewReader(raw))
			request.ContentLength = int64(len(raw))
			request.Header.Set("Content-Type", "application/json")
			response := wireBrowserDo(t, request)
			body, e := io.ReadAll(response.Body)
			if csrf == "" {
				if response.StatusCode == http.StatusOK || len(response.Cookies()) != 0 {
					t.Fatal("GET bootstrap became mutation proof")
				}
				return nil
			}
			var result pb.SessionRevocation
			if e != nil || response.StatusCode != http.StatusOK || protojson.Unmarshal(body, &result) != nil {
				t.Fatalf("logout response %d %v: %s", response.StatusCode, e, body)
			}
			return &result
		}
		logout(&pb.CurrentLogoutRequest{Profile: current.CurrentProfile, PrepareOnly: true}, "")
		prepared := logout(&pb.CurrentLogoutRequest{Profile: current.CurrentProfile, PrepareOnly: true}, current.Principal.CsrfToken)
		if prepared.CurrentReview == nil || prepared.LocalCookieCleared {
			t.Fatal("logout preparation lost retained ID")
		}
		// Retained before the single dispatch; successful local deletion must
		// not disclose a protected cluster outcome via the revoked cookie.
		retained := proto.Clone(prepared.CurrentReview).(*pb.CurrentSessionRevocationReview)
		revoked := logout(&pb.CurrentLogoutRequest{Profile: current.CurrentProfile, Review: retained}, current.Principal.CsrfToken)
		if !revoked.LocalCookieCleared || revoked.CurrentResult != nil {
			t.Fatal("self-revoked admission disclosed cluster original")
		}
		if after := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, "")); after.StatusCode == http.StatusOK {
			t.Fatal("revoked session retained current authorization")
		}
	})
	t.Run("original stop observation remains separate from Apply", func(t *testing.T) {
		if ordinaryReview == nil {
			t.Fatal("missing original review")
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			status, err := controls[0].GetSecurityChangeStatus(ctx, securityWireRequest(token, &pb.GetSecurityChangeStatusRequest{CurrentProfile: ordinaryReview.Profile, CurrentChangeId: ordinaryReview.ChangeId, CurrentIntentDigest: ordinaryReview.IntentDigest}))
			if err == nil && status.Msg.CurrentResult.GetStopObservation() == pb.CurrentAuthorizationStopObservation_CURRENT_AUTHORIZATION_STOP_OBSERVATION_OLD_CUT_NEW_AUTHORIZATIONS_STOPPED {
				if status.Msg.CurrentResult.GetOriginal().GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
					t.Fatal("stop observation changed original")
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("native lower-bound stop observation", err)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	})
	t.Run("stale administrator cannot consume retained new work", func(t *testing.T) {
		current, err := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Msg.Version.CurrentProfile, ExpectedCut: current.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "stale_admin_never_installed"}}}}}}))
		if err != nil {
			t.Fatal(err)
		}
		bob := f.token(t, "bob", func(c map[string]any) { c["iat"] = time.Now().Add(-time.Minute).Unix(); delete(c, "auth_time") })
		revoke, err := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(bob, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Msg.Version.CurrentProfile, ExpectedCut: current.Msg.Version.CurrentCut, Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: current.Msg.Identity, RoleId: "security_admin"}}}}}}))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = controls[0].ApplySecurityChanges(ctx, securityWireRequest(bob, &pb.ApplySecurityChangesRequest{CurrentReview: revoke.Msg.CurrentReview}))
		if original := awaitCurrentPublicOriginal(t, ctx, controls[0], bob, revoke.Msg.CurrentReview); original.Disposition != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			t.Fatal("restrictive control Apply", original)
		}
		if _, err := controls[0].ApplySecurityChanges(ctx, securityWireRequest(token, &pb.ApplySecurityChangesRequest{CurrentReview: prepared.Msg.CurrentReview})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("stale admin consumed new H", err)
		}
		roles, err := controls[0].ListRoles(ctx, securityWireRequest(bob, &pb.ListRolesRequest{Exact: "stale_admin_never_installed"}))
		if err != nil || len(roles.Msg.Roles) != 0 {
			t.Fatal("stale admin changed policy", err)
		}
	})
	t.Run("public authorization stops after quorum loss", func(t *testing.T) {
		stops[1]()
		stops[2]()
		deadline := time.Now().Add(20 * time.Second)
		for runtimes[0].Ready(ctx) {
			if time.Now().After(deadline) {
				t.Fatal("native renewal survived missing quorum")
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		c := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
		if response, e := c.GetVertex(ctx, securityWireRequest(token, &pb.GetVertexRequest{Key: "orders:a"})); e == nil || response != nil {
			t.Fatal("isolated public node authorized new data read")
		}
		caps, e := controls[0].GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
		if e != nil || caps.Msg.Mode != pb.AuthMode_AUTH_MODE_OIDC || caps.Msg.Ready || caps.Msg.ProtocolVersion != 2 {
			t.Fatal("unavailable current became OFF", e)
		}
	})

}

// Separate SDK seam selection reuses the production constructor without
// repeating the container recovery campaign. Every SDK trusts this fixture CA,
// not a global certificate bypass.
func runCurrentPublicSDK4(t *testing.T, s *httptest.Server, token, ca string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("go", func(t *testing.T) {
		sdk, err := client.NewLantern(s.URL, client.WithHTTPClient(s.Client()), client.WithAuthToken(token))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sdk.Close() }()
		if _, err := sdk.GetCurrentPrincipal(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
	module := func(path string) string { raw, _ := json.Marshal(filepath.Join(root, path)); return string(raw) }
	script := `import {readFileSync} from "node:fs";
import {connectSecurity,currentSecurityVersionBinding,CurrentSecurityProgress} from ` + module("sdks/node/src/index.ts") + `;
import {SecurityManagementController,SecurityChangeRecovery} from ` + module("admin/app/lib/client/usecase/security/security-management.ts") + `;
const c=connectSecurity(process.env.LANTERN_CURRENT_WIRE_URL,{token:process.env.LANTERN_CURRENT_WIRE_CREDENTIAL,transportOptions:{nodeOptions:{ca:readFileSync(process.env.LANTERN_CURRENT_WIRE_CA)}}});
const principal=await c.getCurrentPrincipal({});currentSecurityVersionBinding(principal.version);
let dispatches=0;
const port={roles:(cursor,signal)=>c.listRoles({cursor},{signal}),prepare:(currentReview,signal)=>c.prepareSecurityChanges({currentReview},{signal}),apply:async(request,signal)=>{dispatches++;await c.applySecurityChanges(request,{signal});throw new Error("fixture response lost after actual Apply");},status:async(review,signal)=>(await c.getSecurityChangeStatus({currentProfile:review.profile,currentChangeId:review.changeId,currentIntentDigest:review.intentDigest},{signal})).currentResult,invocationRejected:()=>undefined,failure:()=>"unavailable"};
const recovery=new SecurityChangeRecovery();const owner="native-current";
const controller=new SecurityManagementController(port,"roles",new AbortController().signal,recovery,owner);
await controller.load();if(controller.getSnapshot().phase!=="ready")throw new Error("Admin full version not loaded");
await controller.review("ordinary",[{$typeName:"graph.v1.SecurityChange",operation:{case:"putRole",value:{$typeName:"graph.v1.SecurityRole",id:"native_sdk_admin",name:"current",rules:[],envOwned:false}}}]);
if(controller.getSnapshot().review?.approval!=="ordinary")throw new Error("Admin exact review unavailable");
await controller.apply();if(dispatches!==1||controller.getSnapshot().mutation!=="unconfirmed"||!recovery.read(owner))throw new Error("ambiguous original not retained");
await controller.checkStatus();if(controller.getSnapshot().result?.progress!==CurrentSecurityProgress.APPLIED)throw new Error("Admin original status not reconciled");
if(dispatches!==1)throw new Error("Admin replayed mutation");controller.dispose();
console.log("Node/Admin current native wire: PASS");process.exit(0);
`
	file := filepath.Join(t.TempDir(), "current-node-admin.ts")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"bun", "dart", "cargo"} {
		t.Run(language, func(t *testing.T) {
			binary, err := exec.LookPath(language)
			if err != nil {
				t.Fatal("required SDK tool missing", language)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			var cmd *exec.Cmd
			switch language {
			case "bun":
				cmd = exec.CommandContext(ctx, binary, "run", file)
				cmd.Dir = filepath.Join(root, "admin")
			case "dart":
				cmd = exec.CommandContext(ctx, binary, "test", "integration_test/current_security_test.dart")
				cmd.Dir = filepath.Join(root, "sdks/dart")
			case "cargo":
				cmd = exec.CommandContext(ctx, binary, "test", "--locked", "--lib", "security::tests::real_public_current_wire", "--", "--ignored", "--exact")
				cmd.Dir = filepath.Join(root, "sdks/rust")
			}
			cmd.Env = append(os.Environ(), "LANTERN_CURRENT_WIRE_URL="+s.URL, "LANTERN_CURRENT_WIRE_CA="+ca, "LANTERN_CURRENT_WIRE_CREDENTIAL="+token)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s native current wire: %v\n%s", language, err, out)
			}
			if language == "cargo" && !strings.Contains(string(out), "test security::tests::real_public_current_wire ... ok") {
				t.Fatal("Rust selected test did not run")
			}
			t.Logf("%s native public current wire: PASS", language)
		})
	}
}

func awaitCurrentPublicOriginal(t *testing.T, ctx context.Context, control graphv1connect.LanternSecurityServiceClient, token string, review *pb.CurrentSecurityReview) *pb.CurrentSecurityOriginalOutcome {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		response, err := control.GetSecurityChangeStatus(ctx, securityWireRequest(token, &pb.GetSecurityChangeStatusRequest{CurrentProfile: review.Profile, CurrentChangeId: review.ChangeId, CurrentIntentDigest: review.IntentDigest}))
		if err == nil && response.Msg.GetCurrentResult().GetOriginal() != nil {
			return response.Msg.CurrentResult.Original
		}
		if time.Now().After(deadline) {
			t.Fatal("retained original unresolved after status-only recovery", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
