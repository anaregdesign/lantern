package integration_test

// Cross-module acceptance helpers extend the existing fixed native cohort.
// They never provision another cohort, retry an unresolved mutation, or replace
// a registered owner with a test capability. The caller owns all lifecycle hooks.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	coresearch "github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/proto"
)

// These probes retain the actual registered resources built by the current
// production constructors in startNode; none is a caller-invented capability.
type currentOperationalFixture struct {
	Runtime          *provider.SecurityRuntime
	Primary          *service.LanternService
	Slow             *provider.SlowRPCInterceptor
	Rate             *provider.RateLimitInterceptor
	Backupper        *backup.Backupper
	ShutdownPlan     *service.ShutdownPlan
	Pump             *replication.Pump
	Anti             *replication.AntiEntropy
	Gate             *readiness.Gate
	CapacitySnapshot func() (*pb.OperationalCapacityValues, uint64)
	RequestObserve   func() (provider.OperationalOwnerObservation, error)
	start            func()
	startSecurity    func()
	stop             func()
}

var currentOperationalGroups = [...]struct {
	group       pb.OperationalGroup
	first, last pb.OperationalSetting
	mode        pb.OperationalSettingsChangeMode
}{
	{1, 1, 1, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT},
	{2, 2, 3, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT},
	{3, 4, 15, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT},
	{4, 16, 18, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT},
	{5, 19, 24, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_WORKER_RESTART},
	{6, 25, 27, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT},
	{7, 28, 38, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_COORDINATED_RESTART},
	{8, 39, 43, pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_PROCESS_RESTART},
}

type currentOperationalAcceptance struct {
	ctx       context.Context
	controls  []graphv1connect.LanternSecurityServiceClient
	nodes     []*currentOperationalFixture
	transport *http.Client
	servers   []*httptest.Server
	token     string
	reader    string
	bootstrap map[pb.OperationalGroup]*pb.OperationalPolicyValues
	desired   map[pb.OperationalGroup]*pb.ManagedOperationalPolicy
	reviews   []*pb.CurrentSecurityReview
	originals []*pb.CurrentSecurityOriginalOutcome
	keys      []string

	queryReader string
}

func beginCurrentOperationalAcceptance(t *testing.T, ctx context.Context, controls []graphv1connect.LanternSecurityServiceClient, nodes []*currentOperationalFixture, transport *http.Client, servers []*httptest.Server, token, reader, queryReader, machine string) *currentOperationalAcceptance {
	t.Helper()
	if len(controls) != 3 || len(nodes) != 3 || len(servers) != 3 || transport == nil {
		t.Fatal("operational acceptance requires the original three actual members")
	}
	a := &currentOperationalAcceptance{ctx: ctx, controls: controls, nodes: nodes, transport: transport, servers: servers, token: token, reader: reader, queryReader: queryReader,
		bootstrap: make(map[pb.OperationalGroup]*pb.OperationalPolicyValues), desired: make(map[pb.OperationalGroup]*pb.ManagedOperationalPolicy)}
	for member := range controls {
		response := a.read(t, member)
		seen := make(map[pb.OperationalSetting]bool)
		for index, spec := range currentOperationalGroups {
			state := response.Groups[index]
			if state.Group != spec.group || state.Desired != nil || state.AppliedGeneration != 0 || state.AppliedCut != nil || state.Preparation != nil || state.Effective == nil || state.Adoption != pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_UNADOPTED || state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED || state.Error != pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_NONE || state.RestartRequired || state.ChangeMode != spec.mode || len(state.Provenance) != int(spec.last-spec.first+1) {
				t.Fatal("actual closed bootstrap owner is unavailable or invented", member+1, state)
			}
			for _, source := range state.Provenance {
				if source.Setting < spec.first || source.Setting > spec.last || seen[source.Setting] || source.Source < pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_DEFAULT || source.Source > pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_DERIVED {
					t.Fatal("bootstrap provenance is not the exact closed allowlist", member+1, source)
				}
				seen[source.Setting] = true
			}
			if spec.group == pb.OperationalGroup_OPERATIONAL_GROUP_SEARCH {
				if len(state.EffectiveCompatibilityFingerprint) != 64 || state.EffectiveCompatibilityFingerprint != nodes[member].Primary.SearchConfigFingerprint() {
					t.Fatal("bootstrap status fingerprint differs from actual Search engine")
				}
			} else if state.EffectiveCompatibilityFingerprint != "" {
				t.Fatal("non-Search group exposed a compatibility fingerprint", state.Group)
			}
			if member == 0 {
				a.bootstrap[spec.group] = proto.Clone(state.Effective).(*pb.OperationalPolicyValues)
			} else if !proto.Equal(a.bootstrap[spec.group], state.Effective) {
				t.Fatal("cohort fixture has different selected bootstrap values", state.Group)
			}
			a.assertActual(t, nodes[member], spec.group, state.Effective, 0)
		}
		if len(seen) != 43 {
			t.Fatal("operational bootstrap omitted a definitive setting", len(seen))
		}
	}
	initial := a.read(t, 0)
	valid := &pb.OperationalPolicyChange{Version: 1, Values: proto.Clone(a.bootstrap[2]).(*pb.OperationalPolicyValues)}
	for _, credential := range []string{reader, queryReader} {
		if _, err := controls[0].GetOperationalSettings(ctx, securityWireRequest(credential, &pb.GetOperationalSettingsRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("ordinary reader disclosed operational settings", err)
		}
	}
	if _, err := controls[0].GetOperationalSettings(ctx, securityWireRequest(machine, &pb.GetOperationalSettingsRequest{})); err != nil {
		t.Fatal("authorized machine lacks positive settings-read control", err)
	}
	for _, credential := range []string{reader, queryReader, machine} {
		if _, err := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(credential, currentOperationalPrepare(initial.Version, valid))); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("ordinary reader or machine prepared operational mutation", err)
		}
	}
	for _, group := range []pb.OperationalGroup{2, 3, 4, 5, 6, 7, 8} {
		bad := proto.Clone(a.bootstrap[group]).(*pb.OperationalPolicyValues)
		switch group {
		case 2:
			bad.GetRate().Rps, bad.GetRate().Burst = 1, 0
		case 3:
			bad.GetRequest().ScanDefaultLimit = bad.GetRequest().ScanMaxLimit + 1
		case 4:
			bad.GetBackup().IntervalNanos = 0
		case 5:
			bad.GetCapacity().GcEdgeBudget = -1
		case 6:
			bad.GetLifecycle().DrainDelaySeconds = -1
		case 7:
			bad.GetSearch().MaxQueryBytes = 0
		case 8:
			bad.GetReplication().PumpBackoffMinMs = bad.GetReplication().PumpBackoffMaxMs + 1
		}
		if _, err := controls[0].PrepareSecurityChanges(ctx, securityWireRequest(token, currentOperationalPrepare(initial.Version, &pb.OperationalPolicyChange{Version: 1, Values: bad}))); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("typed invalid atomic group prepared", group, err)
		}
	}
	after := a.read(t, 0)
	if !proto.Equal(initial.Version.CurrentCut, after.Version.CurrentCut) {
		t.Fatal("refused operational intents changed cut, floor or actual values")
	}
	for i := range initial.Groups {
		if !proto.Equal(initial.Groups[i], after.Groups[i]) {
			t.Fatal("refused operational intent mutated its atomic group", initial.Groups[i].Group)
		}
	}
	return a
}

func currentOperationalPrepare(version *pb.SecurityVersion, change *pb.OperationalPolicyChange) *pb.PrepareSecurityChangesRequest {
	return &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: version.CurrentProfile, ExpectedCut: version.CurrentCut,
		Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutOperationalPolicy{PutOperationalPolicy: change}}}}}
}

// Eight new committed operations total: seven adoptions and this one Rate
// rollback. Search is staged separately at the caller's existing restart edge.
func (a *currentOperationalAcceptance) Adopt(t *testing.T) {
	t.Helper()
	stale := a.read(t, 0).Version
	rate := &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Rate{Rate: &pb.OperationalRateValues{Rps: 0.000001, Burst: 64}}}
	a.commit(t, 2, rate, false)
	for _, node := range a.nodes {
		// Real status/replay requests already spent some of this member's
		// credit. Bound the drain without replenishing or reconfiguring it.
		credits := 0
		for credits < 65 && node.Rate.Allow() {
			credits++
		}
		if credits < 1 || credits > 64 || node.Rate.Allow() {
			t.Fatal("managed per-node atomic credit did not exhaust independently")
		}
	}
	if _, err := a.data(0).GetServerStatus(a.ctx, securityWireRequest(a.token, &pb.GetServerStatusRequest{})); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("ordinary data borrowed the management recovery reserve", err)
	}
	_ = a.read(t, 0) // Actual authenticated management survives exhausted data credit.
	rollback := proto.Clone(a.bootstrap[2]).(*pb.OperationalPolicyValues)
	if rollback.GetRate().Rps != 0 || rollback.GetRate().Burst != 0 {
		t.Fatal("finite acceptance requires disabled bootstrap admission")
	}
	a.commit(t, 2, rollback, false)
	if proto.Equal(a.reviews[0].ChangeId, a.reviews[1].ChangeId) || proto.Equal(a.originals[0].ResultingCut, a.originals[1].ResultingCut) {
		t.Fatal("Rate rollback did not allocate a new original and generation")
	}
	if _, err := a.controls[0].PrepareSecurityChanges(a.ctx, securityWireRequest(a.token, currentOperationalPrepare(stale, &pb.OperationalPolicyChange{Version: 1, Values: proto.Clone(a.bootstrap[3]).(*pb.OperationalPolicyValues)}))); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("cross-group stale cut was accepted after Rate changed", err)
	}
	request := &pb.OperationalRequestValues{DeleteByPrefixDefaultLimit: 1, DeleteByPrefixMaxLimit: 4, IlluminateMaxK: 1024, IlluminateMaxStep: 32, MaxBatchSize: 1024, MaxKeyLen: 4096,
		ScanDefaultLimit: 2, ScanMaxLimit: 8, TraversalMaxPushes: 100000, TraversalMaxResults: 1024, TraversalMaxTouchedEdges: 1000000, TraversalTimeoutMs: 1000}
	a.commit(t, 3, &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Request{Request: request}}, true)
	if _, err := a.data(0).GetVertex(a.ctx, securityWireRequest(a.token, &pb.GetVertexRequest{Key: "orders:" + strings.Repeat("x", 4096)})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("actual transport validator did not install request policy", err)
	}
	vertices := make([]*pb.Vertex, 9)
	for i := range vertices {
		key := fmt.Sprintf("orders:operational-acceptance:%02d", i)
		a.keys = append(a.keys, key)
		vertices[i] = &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "managed acceptance"}}
	}
	if _, err := a.data(0).PutVertices(a.ctx, securityWireRequest(a.token, &pb.PutVerticesRequest{Vertices: vertices})); err != nil {
		t.Fatal("request-policy fixture batch", err)
	}
	for _, limit := range []uint32{0, 100} {
		result, err := a.data(0).ScanVertexKeys(a.ctx, securityWireRequest(a.token, &pb.ScanVertexKeysRequest{Prefix: "orders:operational-acceptance:", Limit: limit}))
		want := 2
		if limit != 0 {
			want = 8
		}
		if err != nil || len(result.Msg.Keys) != want || len(result.Msg.NextCursor) == 0 {
			t.Fatal("actual scan owner did not enforce default and maximum", result, err)
		}
	}
	backup := &pb.OperationalBackupValues{Enabled: true, IntervalNanos: int64(time.Hour) + 1, Retain: 0}
	a.commit(t, 4, &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Backup{Backup: backup}}, false)
	// The first resident attempt may coexist. No exact file count or pruning
	// claim is inferred from this one explicit real producer invocation.
	produced, err := a.nodes[0].Backupper.BackupNow(a.ctx)
	if err != nil || produced.Bytes == 0 || produced.Vertices < len(a.keys) {
		t.Fatal("managed production did not create an actual backup", produced, err)
	}
	capacity := &pb.OperationalCapacityValues{GcEdgeBudget: 7, GcIntervalSeconds: 3600, MaxVertices: 1, MaxEdges: 1, MaxVertexCausalEntries: 1, MaxEdgeCausalEntries: 1}
	capacityValues := &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Capacity{Capacity: capacity}}
	beforePreview := a.read(t, 0)
	preview, previewErr := a.controls[0].PreviewOperationalPolicy(a.ctx, securityWireRequest(a.token, &pb.PreviewOperationalPolicyRequest{Values: capacityValues}))
	if previewErr != nil {
		t.Fatal("actual connected capacity preview", previewErr)
	}
	usage := preview.Msg.CapacityUsage
	if usage == nil || usage.Member != beforePreview.Member || usage.EffectiveGeneration != beforePreview.Groups[4].AppliedGeneration || usage.VertexFootprint < uint64(len(a.keys)) || usage.VerticesOverLimit != (usage.VertexFootprint > 1) || usage.EdgesOverLimit != (usage.EdgeFootprint > 1) || usage.VertexCausalOverLimit != (usage.VertexCausalEntries > 1) || usage.EdgeCausalOverLimit != (usage.EdgeCausalEntries > 1) || preview.Msg.BackupRetention != nil || preview.Msg.CompatibilityFingerprint != "" || !proto.Equal(preview.Msg.Version, beforePreview.Version) || !strings.Contains(preview.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("capacity preview lacks actual local usage or read-only response guards", preview.Msg)
	}
	if denied, err := a.controls[0].PreviewOperationalPolicy(a.ctx, securityWireRequest(a.reader, &pb.PreviewOperationalPolicyRequest{Values: capacityValues})); err == nil || denied != nil {
		t.Fatal("non-management actor sampled operational capacity", denied, err)
	}
	if afterPreview := a.read(t, 0); !proto.Equal(beforePreview, afterPreview) {
		t.Fatal("capacity preview changed operational policy or native state")
	}
	a.commit(t, 5, &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Capacity{Capacity: capacity}}, false)
	retained, err := a.data(0).GetVertices(a.ctx, securityWireRequest(a.token, &pb.GetVerticesRequest{Keys: a.keys}))
	if err != nil || len(retained.Msg.Vertices) != len(a.keys) || len(retained.Msg.Missing) != 0 {
		t.Fatal("capacity shrink evicted existing vertices", retained, err)
	}
	if _, err := a.data(0).PutVertex(a.ctx, securityWireRequest(a.token, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:operational-overflow"}})); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("actual capacity owner admitted a new vertex over its shrunken cap", err)
	}
	lifecycle := &pb.OperationalLifecycleValues{DrainDelaySeconds: 0, MaxReplicationLag: 100, ShutdownTimeoutSeconds: 11}
	a.commit(t, 6, &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Lifecycle{Lifecycle: lifecycle}}, false)
	replication := &pb.OperationalReplicationValues{AntiEntropyGapWarnThreshold: 17, AntiEntropyIntervalMs: 0, AntiEntropySubscribeTimeoutMs: 1234, PumpBackoffMinMs: 17, PumpBackoffMaxMs: 53}
	a.commit(t, 8, &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Replication{Replication: replication}}, false)
}

func (a *currentOperationalAcceptance) StageSearch(t *testing.T) {
	t.Helper()
	if len(a.reviews) != 7 {
		t.Fatal("Search must follow the six other adoptions and one Rate rollback")
	}
	if _, err := a.data(0).SearchVertices(a.ctx, securityWireRequest(a.queryReader, &pb.SearchVerticesRequest{Prefix: "orders:", Query: "orders"})); err != nil {
		t.Fatal("ordinary reader lacks positive fresh native Search admission", err)
	}
	search := &pb.OperationalSearchValues{DefaultLimit: 3, MaxLimit: 17, DefaultMode: pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_ALL, DefaultMinShould: 2, TimeoutMs: 1234,
		MaxQueryBytes: 4096, MaxQueryTerms: 128, MaxDictionaryVisits: 50000, MaxPostingVisits: 100000, MaxPositionVisits: 200000, MaxExpirationVisits: 2048}
	a.commit(t, 7, &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_Search{Search: search}}, false)
	for member := range a.controls {
		if _, err := a.data(member).SearchVertices(a.ctx, securityWireRequest(a.queryReader, &pb.SearchVerticesRequest{Prefix: "orders:", Query: "orders"})); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal("pending managed Search admitted a direct RPC", member+1, err)
		}
	}
	a.assertAudit(t)
}

func (a *currentOperationalAcceptance) AfterRestart(t *testing.T, control graphv1connect.LanternSecurityServiceClient, node *currentOperationalFixture, server *httptest.Server) {
	t.Helper()
	if len(a.reviews) != 8 {
		t.Fatal("operational matrix did not execute exactly eight committed originals")
	}
	a.controls[1], a.nodes[1], a.servers[1] = control, node, server
	for _, group := range []pb.OperationalGroup{2, 3, 4, 5, 6, 7, 8} {
		expected := a.desired[group]
		state := a.await(t, 1, group, expected.Generation, true)
		if state.RestartRequired || !proto.Equal(state.Effective, expected.Values) || state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED {
			t.Fatal("intact restart did not restore managed actual state", group, state)
		}
		for _, source := range state.Provenance {
			if source.Source != pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_MANAGED {
				t.Fatal("restart replaced managed provenance with bootstrap input", source)
			}
		}
		a.assertActual(t, node, group, expected.Values, expected.Generation)
	}
	search := a.desired[pb.OperationalGroup_OPERATIONAL_GROUP_SEARCH]
	pump, pOK := node.Pump.Scheduling()
	anti, aOK := node.Anti.Scheduling()
	if !pOK || !aOK || anti.Pump != node.Pump || pump.SearchFingerprint != search.CompatibilityFingerprint || anti.SearchFingerprint != search.CompatibilityFingerprint || node.Primary.SearchConfigFingerprint() != search.CompatibilityFingerprint {
		t.Fatal("reconstructed workers did not capture the restored actual Search engine")
	}
	if _, err := a.data(1).SearchVertices(a.ctx, securityWireRequest(a.queryReader, &pb.SearchVerticesRequest{Prefix: "orders:", Query: "orders"})); err != nil {
		t.Fatal("restored actual Search engine rejected ordinary current reader", err)
	}
	if _, err := a.data(0).SearchVertices(a.ctx, securityWireRequest(a.queryReader, &pb.SearchVerticesRequest{Prefix: "orders:", Query: "orders"})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("one member restart falsely activated an old member's engine", err)
	}
}

func (a *currentOperationalAcceptance) data(member int) graphv1connect.LanternServiceClient {
	return graphv1connect.NewLanternServiceClient(a.transport, a.servers[member].URL)
}

func (a *currentOperationalAcceptance) read(t *testing.T, member int) *pb.GetOperationalSettingsResponse {
	t.Helper()
	return a.readBefore(t, member, time.Now().Add(20*time.Second))
}

func (a *currentOperationalAcceptance) readBefore(t *testing.T, member int, deadline time.Time) *pb.GetOperationalSettingsResponse {
	t.Helper()
	ctx, cancel := context.WithDeadline(a.ctx, deadline)
	defer cancel()
	for {
		response, err := a.controls[member].GetOperationalSettings(ctx, securityWireRequest(a.token, &pb.GetOperationalSettingsRequest{}))
		if err == nil {
			if response.Msg.SchemaVersion != 1 || response.Msg.Member != uint32(member+1) || len(response.Msg.Groups) != 8 || response.Msg.Version.GetCurrentCut() == nil || response.Msg.Version.GetCurrentProfile() == nil {
				t.Fatal("closed operational response lost its native cut/member binding", response.Msg)
			}
			return response.Msg
		}
		if connect.CodeOf(err) != connect.CodeUnavailable || time.Now().After(deadline) {
			t.Fatal("operational read failed outside bounded current renewal", member+1, err)
		}
		a.pause(t)
	}
}

func (a *currentOperationalAcceptance) pause(t *testing.T) {
	t.Helper()
	select {
	case <-a.ctx.Done():
		t.Fatal(a.ctx.Err())
	case <-time.After(250 * time.Millisecond):
	}
}

func (a *currentOperationalAcceptance) await(t *testing.T, member int, group pb.OperationalGroup, generation uint64, applied bool) *pb.OperationalGroupSettings {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		response := a.readBefore(t, member, deadline)
		state := response.Groups[int(group)-1]
		if state.Group != group || state.ChangeMode != currentOperationalGroups[int(group)-1].mode || state.GetDesired().GetGeneration() > generation {
			t.Fatal("operational group or generation advanced unexpectedly", state)
		}
		if group == pb.OperationalGroup_OPERATIONAL_GROUP_SEARCH {
			if state.EffectiveCompatibilityFingerprint != a.nodes[member].Primary.SearchConfigFingerprint() {
				t.Fatal("settings fingerprint differs from actual installed Search engine")
			}
		} else if state.EffectiveCompatibilityFingerprint != "" {
			t.Fatal("non-Search group exposed a compatibility fingerprint", group)
		}
		if state.GetDesired().GetGeneration() == generation && (!applied || state.AppliedGeneration == generation) {
			if state.Error != pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_NONE || state.Desired == nil || state.Preparation == nil || state.Adoption != pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_MANAGED {
				t.Fatal("committed group lost actual owner/preparation evidence", state)
			}
			return state
		}
		if time.Now().After(deadline) {
			t.Fatal("operational desired/actual generation did not converge", member+1, group, generation)
		}
		a.pause(t)
	}
}

func (a *currentOperationalAcceptance) commit(t *testing.T, group pb.OperationalGroup, values *pb.OperationalPolicyValues, loseResponse bool) {
	t.Helper()
	current := a.read(t, 0)
	prior := a.desired[group]
	generation := prior.GetGeneration()
	change := &pb.OperationalPolicyChange{Version: 1, ExpectedGeneration: generation, Values: values}
	if group == pb.OperationalGroup_OPERATIONAL_GROUP_SEARCH {
		preview, err := a.controls[0].PreviewOperationalPolicy(a.ctx, securityWireRequest(a.token, &pb.PreviewOperationalPolicyRequest{Values: values}))
		if err != nil || preview.Msg.Group != group || len(preview.Msg.CompatibilityFingerprint) != 64 || !proto.Equal(preview.Msg.Version.CurrentCut, current.Version.CurrentCut) {
			t.Fatal("Search candidate preview lacks current full compatibility binding", preview, err)
		}
		change.CompatibilityFingerprint = preview.Msg.CompatibilityFingerprint
	}
	prepared, err := a.controls[0].PrepareSecurityChanges(a.ctx, securityWireRequest(a.token, currentOperationalPrepare(current.Version, change)))
	if err != nil || prepared.Msg.CurrentReview == nil || prepared.Msg.Requirement != pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY {
		t.Fatal("ordinary closed operational review failed", group, prepared, err)
	}
	review := prepared.Msg.CurrentReview
	if loseResponse {
		a.loseApplyResponse(t, review)
	} else {
		// One dispatch only. A missing response is resolved by status below.
		_, _ = a.controls[0].ApplySecurityChanges(a.ctx, securityWireRequest(a.token, &pb.ApplySecurityChangesRequest{CurrentReview: review}))
	}
	original := awaitCurrentPublicOriginal(t, a.ctx, a.controls[0], a.token, review)
	if original.Disposition != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED || !proto.Equal(original.ChangeId, review.ChangeId) || !bytes.Equal(original.IntentDigest, review.IntentDigest) || len(original.HandoffDigest) != 32 || original.ResultingCut == nil {
		t.Fatal("operational original did not commit", group, original)
	}
	a.reviews, a.originals = append(a.reviews, proto.Clone(review).(*pb.CurrentSecurityReview)), append(a.originals, proto.Clone(original).(*pb.CurrentSecurityOriginalOutcome))
	expected := &pb.ManagedOperationalPolicy{Version: 1, Generation: generation + 1, Values: proto.Clone(values).(*pb.OperationalPolicyValues), CompatibilityFingerprint: change.CompatibilityFingerprint}
	a.desired[group] = expected
	restart := group == pb.OperationalGroup_OPERATIONAL_GROUP_SEARCH || group == pb.OperationalGroup_OPERATIONAL_GROUP_REPLICATION
	for member := range a.controls {
		state := a.await(t, member, group, generation+1, !restart)
		if !proto.Equal(state.Desired, expected) {
			t.Fatal("atomic group desired values differ from reviewed complete policy", member+1, state)
		}
		if restart {
			if state.AppliedGeneration != 0 || state.AppliedCut != nil || !state.RestartRequired || state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_PENDING || !proto.Equal(state.Effective, a.bootstrap[group]) {
				t.Fatal("committed restart policy was falsely reported installed", state)
			}
			for _, source := range state.Provenance {
				if source.Source == pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_MANAGED {
					t.Fatal("unapplied restart policy invented managed effective provenance", source)
				}
			}
			a.assertActual(t, a.nodes[member], group, a.bootstrap[group], 0)
		} else {
			if state.AppliedCut == nil || state.AppliedCut.Sequence < original.ResultingCut.Sequence || state.RestartRequired || state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED || !proto.Equal(state.Effective, values) {
				t.Fatal("hot application lacks actual value/generation/cut evidence", state)
			}
			if state.AppliedCut.Sequence == original.ResultingCut.Sequence && !proto.Equal(state.AppliedCut, original.ResultingCut) {
				t.Fatal("same-sequence applied evidence changed the complete native cut")
			}
			for _, source := range state.Provenance {
				if source.Source != pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_MANAGED {
					t.Fatal("adopted effective field retained bootstrap precedence", source)
				}
			}
			a.assertActual(t, a.nodes[member], group, values, generation+1)
		}
	}
	// Deliberate exact replay occurs only after the original is known committed.
	// It is an idempotency assertion, never an ambiguous-response retry.
	replayed, err := a.controls[0].ApplySecurityChanges(a.ctx, securityWireRequest(a.token, &pb.ApplySecurityChangesRequest{CurrentReview: review}))
	if err != nil || !proto.Equal(replayed.Msg.GetCurrentResult().GetOriginal(), original) {
		t.Fatal("operational replay changed the immutable original", group, replayed, err)
	}
}

func (a *currentOperationalAcceptance) loseApplyResponse(t *testing.T, review *pb.CurrentSecurityReview) {
	t.Helper()
	dispatches, receivedStatus := 0, 0
	var bodyErr error
	transport := currentWireTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure {
			return nil, errors.New("unexpected request on operational response-loss transport")
		}
		dispatches++
		response, err := a.transport.Transport.RoundTrip(request)
		if err != nil {
			return nil, err
		}
		receivedStatus = response.StatusCode
		_, readErr := io.Copy(io.Discard, response.Body)
		bodyErr = errors.Join(readErr, response.Body.Close())
		return nil, errors.New("fixture lost completed operational Apply response")
	}, closeIdle: func() {}}
	control := graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: transport}, a.servers[0].URL)
	if _, err := control.ApplySecurityChanges(a.ctx, securityWireRequest(a.token, &pb.ApplySecurityChangesRequest{CurrentReview: review})); err == nil || dispatches != 1 || receivedStatus != http.StatusOK || bodyErr != nil {
		t.Fatal("operational response was not lost after one actual dispatch", dispatches, receivedStatus, bodyErr, err)
	}
}

func (a *currentOperationalAcceptance) assertActual(t *testing.T, node *currentOperationalFixture, group pb.OperationalGroup, values *pb.OperationalPolicyValues, generation uint64) {
	t.Helper()
	switch group {
	case pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC:
		actual := node.Slow.Snapshot()
		if actual.Generation != generation || actual.Threshold.Milliseconds() != values.GetSlowRpc().ThresholdMs {
			t.Fatal("actual resident Slow owner differs from management status", actual)
		}
	case pb.OperationalGroup_OPERATIONAL_GROUP_RATE:
		actual := node.Rate.Snapshot()
		if actual.Error != nil || actual.Generation != generation || actual.Config.RPS != values.GetRate().Rps || int64(actual.Config.Burst) != values.GetRate().Burst {
			t.Fatal("actual atomic Rate pair differs from management status", actual)
		}
	case pb.OperationalGroup_OPERATIONAL_GROUP_REQUEST:
		actual, err := node.RequestObserve()
		if err != nil || actual.Effective == nil || actual.Effective.Request == nil || actual.EffectiveGeneration != generation {
			t.Fatal("actual request owner unavailable", actual, err)
		}
		v := actual.Effective.Request
		encoded := &pb.OperationalRequestValues{DeleteByPrefixDefaultLimit: v.DeleteByPrefixDefaultLimit, DeleteByPrefixMaxLimit: v.DeleteByPrefixMaxLimit, IlluminateMaxK: v.IlluminateMaxK, IlluminateMaxStep: v.IlluminateMaxStep,
			MaxBatchSize: v.MaxBatchSize, MaxKeyLen: v.MaxKeyLen, ScanDefaultLimit: v.ScanDefaultLimit, ScanMaxLimit: v.ScanMaxLimit, TraversalMaxPushes: v.TraversalMaxPushes, TraversalMaxResults: v.TraversalMaxResults, TraversalMaxTouchedEdges: v.TraversalMaxTouchedEdges, TraversalTimeoutMs: v.TraversalTimeoutMS}
		if !proto.Equal(encoded, values.GetRequest()) {
			t.Fatal("actual full request policy differs from management status")
		}
	case pb.OperationalGroup_OPERATIONAL_GROUP_BACKUP:
		actual := node.Backupper.Production()
		v := values.GetBackup()
		if actual.Generation != generation || actual.Config.Enabled != v.Enabled || int64(actual.Config.Interval) != v.IntervalNanos || int64(actual.Config.Retain) != v.Retain {
			t.Fatal("actual backup producer differs from management status", actual)
		}
	case pb.OperationalGroup_OPERATIONAL_GROUP_CAPACITY:
		actual, actualGeneration := node.CapacitySnapshot()
		if actualGeneration != generation || !proto.Equal(actual, values.GetCapacity()) {
			t.Fatal("actual graph/GC owner differs from management status", actual)
		}
	case pb.OperationalGroup_OPERATIONAL_GROUP_LIFECYCLE:
		actual := node.ShutdownPlan.Current() // Observation never captures shutdown.
		v := values.GetLifecycle()
		if actual.Generation != generation || actual.Policy.DrainDelay != time.Duration(v.DrainDelaySeconds)*time.Second || actual.Policy.ShutdownTimeout != time.Duration(v.ShutdownTimeoutSeconds)*time.Second || actual.Policy.MaxReplicationLag != v.MaxReplicationLag || node.Gate.MaxLag() != v.MaxReplicationLag {
			t.Fatal("actual future lifecycle plan/readiness owner differs", actual)
		}
	case pb.OperationalGroup_OPERATIONAL_GROUP_SEARCH:
		baseline := node.Primary.SearchPolicyBaseline()
		v := values.GetSearch()
		mode := pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_UNSPECIFIED
		switch baseline.DefaultMode {
		case coresearch.MatchAny:
			mode = pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_ANY
		case coresearch.MatchAll:
			mode = pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_ALL
		case coresearch.MatchMinShould:
			mode = pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_MIN_SHOULD
		}
		if int64(baseline.DefaultLimit) != v.DefaultLimit || int64(baseline.MaxLimit) != v.MaxLimit || mode != v.DefaultMode || int64(baseline.DefaultMinShould) != v.DefaultMinShould || baseline.Timeout.Milliseconds() != v.TimeoutMs || int64(baseline.MaxQueryBytes) != v.MaxQueryBytes || baseline.WorkBudget.MaxQueryTerms != v.MaxQueryTerms || baseline.WorkBudget.MaxDictionaryVisits != v.MaxDictionaryVisits || baseline.WorkBudget.MaxPostingVisits != v.MaxPostingVisits || baseline.WorkBudget.MaxPositionVisits != v.MaxPositionVisits || baseline.WorkBudget.MaxExpirationVisits != v.MaxExpirationVisits {
			t.Fatal("actual Search engine values differ from management status")
		}
	case pb.OperationalGroup_OPERATIONAL_GROUP_REPLICATION:
		pump, pOK := node.Pump.Scheduling()
		anti, aOK := node.Anti.Scheduling()
		v := values.GetReplication()
		if !pOK || !aOK || anti.Pump != node.Pump || pump.BackoffMin.Milliseconds() != v.PumpBackoffMinMs || pump.BackoffMax.Milliseconds() != v.PumpBackoffMaxMs || anti.Interval.Milliseconds() != v.AntiEntropyIntervalMs || anti.SubscribeTimeout.Milliseconds() != v.AntiEntropySubscribeTimeoutMs || anti.GapWarnThreshold != uint64(v.AntiEntropyGapWarnThreshold) {
			t.Fatal("actual immutable worker pair differs from management status")
		}
	default:
		t.Fatal("unknown operational group in closed acceptance matrix", group)
	}
}

func (a *currentOperationalAcceptance) assertAudit(t *testing.T) {
	t.Helper()
	var records []*pb.CurrentSecurityAuditRecord
	cursor := ""
	seen := map[string]bool{"": true}
	complete := false
	for page := 0; page < 3; page++ {
		response, err := a.controls[0].ListSecurityAudit(a.ctx, securityWireRequest(a.token, &pb.ListSecurityAuditRequest{Limit: 100, Cursor: cursor}))
		if err != nil || len(response.Msg.CurrentRecords) > 100 {
			t.Fatal("bounded operational audit page unavailable", response, err)
		}
		records = append(records, response.Msg.CurrentRecords...)
		cursor = response.Msg.NextCursor
		if cursor == "" {
			complete = true
			break
		}
		if seen[cursor] {
			t.Fatal("operational audit cursor did not advance")
		}
		seen[cursor] = true
	}
	if !complete {
		t.Fatal("operational audit exceeded its declared three-page sample")
	}
	for i, review := range a.reviews {
		count := 0
		for _, record := range records {
			if proto.Equal(record.GetResult().GetChangeId(), review.ChangeId) {
				count++
				if !proto.Equal(record.GetResult().GetOriginal(), a.originals[i]) || len(record.ActorDigest) != 32 || record.Operation == "" {
					t.Fatal("operational audit lost its exact original/actor", record)
				}
			}
		}
		if count != 1 {
			t.Fatal("exact replay created duplicate or absent audit original", i, count)
		}
	}
}
