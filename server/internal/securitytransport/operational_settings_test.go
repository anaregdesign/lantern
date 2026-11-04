package securitytransport

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func operationalProjection() security.OperationalPolicyProjection {
	log := logSettingsProjection()
	return security.OperationalPolicyProjection{Cut: log.Cut, Member: log.Member}
}
func operationalObservations() map[operationalpolicy.Group]OperationalSettingsObservation {
	observations := make(map[operationalpolicy.Group]OperationalSettingsObservation)
	for _, group := range operationalpolicy.Groups() {
		observations[group] = UnavailableOperationalSettingsObservation()
	}
	return observations
}
func operationalObservation(group operationalpolicy.Group, values operationalpolicy.Values) OperationalSettingsObservation {
	sources := make(map[OperationalSetting]OperationalSettingsSource)
	for _, field := range operationalSettingsFields(group) {
		sources[field] = OperationalSourceEnvironment
	}
	o := OperationalSettingsObservation{Effective: &values, Sources: sources, Mode: OperationalModeHot, Error: OperationalErrorNone}
	if group == operationalpolicy.GroupSearch {
		o.EffectiveCompatibilityFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		o.Mode = OperationalModeCoordinatedRestart
	}
	return o
}
func operationalPreparation(p security.OperationalPolicyProjection, group operationalpolicy.Group) security.OperationalPolicyPreparation {
	log := logSettingsProjection()
	log.VersionFloor = true
	// The exact current ID fixture used by the preserved logging contract.
	prep := logSettingsPreparation(log)
	return security.OperationalPolicyPreparation{Group: group, Version: 1, ChangeID: prep.ChangeID, IntentDigest: prep.IntentDigest, ActorDigest: prep.ActorDigest}
}

func TestOperationalSettingsClosedStatusAndMixedProvenance(t *testing.T) {
	p := operationalProjection()
	observed := operationalObservations()
	values := operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: 1, Burst: 2}}
	rate := operationalObservation(operationalpolicy.GroupRate, values)
	rate.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_BURST] = OperationalSourceDerived
	observed[operationalpolicy.GroupRate] = rate
	response, err := encodeOperationalSettings(p, observed)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Groups) != 8 || response.Groups[1].Provenance[1].Source != OperationalSourceDerived || response.Groups[0].Effective != nil || response.Groups[0].Adoption != pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_UNADOPTED {
		t.Fatal("mixed or unavailable state collapsed")
	}
	rate.Effective.Rate.RPS = 99
	if response.Groups[1].Effective.GetRate().Rps != 1 {
		t.Fatal("observation aliases wire result")
	}
}
func TestOperationalSettingsPendingRestartDoesNotClaimApplication(t *testing.T) {
	p := operationalProjection()
	group := operationalpolicy.GroupSearch
	values, _ := operationalpolicy.Defaults(group)
	p.Preparations = []security.OperationalPolicyPreparation{operationalPreparation(p, group)}
	policy := &operationalpolicy.Policy{Version: 1, Generation: 2, Values: values, CompatibilityFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if err := p.Policies.Set(group, policy); err != nil {
		t.Fatal(err)
	}
	observed := operationalObservations()
	search := operationalObservation(group, values)
	search.Pending = true
	search.RestartRequired = true
	observed[group] = search
	response, err := encodeOperationalSettings(p, observed)
	if err != nil {
		t.Fatal(err)
	}
	state := response.Groups[6]
	if state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_PENDING || state.AppliedGeneration != 0 || !state.RestartRequired || state.EffectiveCompatibilityFingerprint == state.Desired.CompatibilityFingerprint {
		t.Fatal("pending state hidden")
	}
	search.AppliedGeneration = 2
	search.AppliedCut = p.Cut
	search.Pending = false
	search.RestartRequired = false
	for field := range search.Sources {
		search.Sources[field] = OperationalSourceManaged
	}
	observed[group] = search
	if _, err := encodeOperationalSettings(p, observed); err == nil {
		t.Fatal("same generation wrong actual fingerprint accepted")
	}
	search.EffectiveCompatibilityFingerprint = policy.CompatibilityFingerprint
	observed[group] = search
	if _, err := encodeOperationalSettings(p, observed); err != nil {
		t.Fatal(err)
	}
}
func TestOperationalSettingsRejectsInconsistentObservation(t *testing.T) {
	edits := map[string]func(*security.OperationalPolicyProjection, map[operationalpolicy.Group]OperationalSettingsObservation){
		"missing member": func(p *security.OperationalPolicyProjection, _ map[operationalpolicy.Group]OperationalSettingsObservation) {
			p.Member = 0
		},
		"missing group": func(_ *security.OperationalPolicyProjection, o map[operationalpolicy.Group]OperationalSettingsObservation) {
			delete(o, operationalpolicy.GroupRate)
		},
		"unsupported fabricated value": func(_ *security.OperationalPolicyProjection, o map[operationalpolicy.Group]OperationalSettingsObservation) {
			v, _ := operationalpolicy.Defaults(operationalpolicy.GroupRate)
			x := o[operationalpolicy.GroupRate]
			x.Effective = &v
			o[operationalpolicy.GroupRate] = x
		},
		"pending without desired": func(_ *security.OperationalPolicyProjection, o map[operationalpolicy.Group]OperationalSettingsObservation) {
			v, _ := operationalpolicy.Defaults(operationalpolicy.GroupRate)
			x := operationalObservation(operationalpolicy.GroupRate, v)
			x.Pending = true
			o[operationalpolicy.GroupRate] = x
		},
		"unknown provenance": func(_ *security.OperationalPolicyProjection, o map[operationalpolicy.Group]OperationalSettingsObservation) {
			v, _ := operationalpolicy.Defaults(operationalpolicy.GroupRate)
			x := operationalObservation(operationalpolicy.GroupRate, v)
			x.Sources[99] = OperationalSourceDefault
			o[operationalpolicy.GroupRate] = x
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			p := operationalProjection()
			o := operationalObservations()
			edit(&p, o)
			if _, err := encodeOperationalSettings(p, o); err == nil {
				t.Fatal("invalid local observation accepted")
			}
		})
	}
}
func TestOperationalReadsRequireCurrentComposition(t *testing.T) {
	called := false
	h, err := NewSecurityConnectHandler(SecurityServiceOptions{OperationalSettingsObserver: func(security.OperationalPolicyProjection) (map[operationalpolicy.Group]OperationalSettingsObservation, error) {
		called = true
		return operationalObservations(), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetOperationalSettings(t.Context(), connect.NewRequest(&pb.GetOperationalSettingsRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition || called {
		t.Fatal("OFF leaked observation", err)
	}
	request := &pb.GetOperationalSettingsRequest{}
	request.ProtoReflect().SetUnknown([]byte{0x08, 0x01})
	if _, err := h.GetOperationalSettings(t.Context(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown input accepted", err)
	}
}

func TestOperationalInvalidBootstrapPreservesSourceAndInstalledOwner(t *testing.T) {
	p := operationalProjection()
	observed := operationalObservations()
	observed[operationalpolicy.GroupRate] = OperationalSettingsObservation{Mode: OperationalModeHot, Error: OperationalErrorInvalidPolicy, Sources: map[OperationalSetting]OperationalSettingsSource{pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS: OperationalSourceEnvironment, pb.OperationalSetting_OPERATIONAL_SETTING_RATE_BURST: OperationalSourceDerived}}
	response, err := encodeOperationalSettings(p, observed)
	if err != nil {
		t.Fatal(err)
	}
	rate := response.Groups[1]
	if rate.Effective != nil || rate.ChangeMode != OperationalModeHot || rate.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR || rate.Adoption != pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_UNADOPTED || len(rate.Provenance) != 2 {
		t.Fatal("invalid bootstrap collapsed or replaced")
	}
	invalid := observed[operationalpolicy.GroupRate]
	invalid.RestartRequired = true
	observed[operationalpolicy.GroupRate] = invalid
	if _, err := encodeOperationalSettings(p, observed); err == nil {
		t.Fatal("error state invented hot restart requirement")
	}
}
func TestOperationalAppliedCutMustMatchAllSameSequenceDigests(t *testing.T) {
	p := operationalProjection()
	group := operationalpolicy.GroupSlowRPC
	values, _ := operationalpolicy.Defaults(group)
	p.Preparations = []security.OperationalPolicyPreparation{operationalPreparation(p, group)}
	if err := p.Policies.Set(group, &operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}); err != nil {
		t.Fatal(err)
	}
	observation := operationalObservation(group, values)
	observation.AppliedGeneration = 1
	observation.AppliedCut = p.Cut
	for field := range observation.Sources {
		observation.Sources[field] = OperationalSourceManaged
	}
	observations := operationalObservations()
	observations[group] = observation
	if _, err := encodeOperationalSettings(p, observations); err != nil {
		t.Fatal(err)
	}
	observation.AppliedCut.Projection[0]++
	observations[group] = observation
	if _, err := encodeOperationalSettings(p, observations); err == nil {
		t.Fatal("same-sequence foreign projection accepted")
	}
}

func TestOperationalInvalidBootstrapCanStageAValidRestartRepair(t *testing.T) {
	p := operationalProjection()
	group := operationalpolicy.GroupReplication
	values, _ := operationalpolicy.Defaults(group)
	observed := operationalObservation(group, values)
	observed.Effective = nil
	observed.Error = OperationalErrorInvalidPolicy
	observed.Pending = true
	observed.Mode = OperationalModeProcessRestart
	observed.RestartRequired = true
	observations := operationalObservations()
	observations[group] = observed
	if _, err := encodeOperationalSettings(p, observations); err == nil {
		t.Fatal("pending repair without a desired policy accepted")
	}
	p.Preparations = []security.OperationalPolicyPreparation{operationalPreparation(p, group)}
	if err := p.Policies.Set(group, &operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}); err != nil {
		t.Fatal(err)
	}
	response, err := encodeOperationalSettings(p, observations)
	if err != nil {
		t.Fatal(err)
	}
	state := response.Groups[7]
	if state.Effective != nil || state.Desired == nil || state.AppliedGeneration != 0 || !state.RestartRequired || state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR {
		t.Fatal("pending restart repair hidden or invented effective values")
	}
}

func TestOperationalInvalidSearchRetainsKnownActualFingerprintThroughRepair(t *testing.T) {
	p := operationalProjection()
	group := operationalpolicy.GroupSearch
	values, _ := operationalpolicy.Defaults(group)
	observed := operationalObservation(group, values)
	observed.Effective = nil
	observed.Error = OperationalErrorInvalidPolicy
	observations := operationalObservations()
	observations[group] = observed
	response, err := encodeOperationalSettings(p, observations)
	if err != nil {
		t.Fatal(err)
	}
	state := response.Groups[6]
	if state.Effective != nil || state.Desired != nil || state.EffectiveCompatibilityFingerprint != observed.EffectiveCompatibilityFingerprint || state.AppliedGeneration != 0 || state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR {
		t.Fatal("known engine metadata was hidden or treated as a typed installation")
	}
	p.Preparations = []security.OperationalPolicyPreparation{operationalPreparation(p, group)}
	desiredFingerprint := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := p.Policies.Set(group, &operationalpolicy.Policy{Version: 1, Generation: 1, Values: values, CompatibilityFingerprint: desiredFingerprint}); err != nil {
		t.Fatal(err)
	}
	observed.Pending = true
	observed.RestartRequired = true
	observations[group] = observed
	response, err = encodeOperationalSettings(p, observations)
	if err != nil {
		t.Fatal(err)
	}
	state = response.Groups[6]
	if state.Effective != nil || state.Desired == nil || state.Desired.CompatibilityFingerprint != desiredFingerprint || state.EffectiveCompatibilityFingerprint == desiredFingerprint || !state.RestartRequired || state.AppliedGeneration != 0 || state.Application != pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR {
		t.Fatal("pending repair lost actual or desired fingerprint, or invented application")
	}
}
func TestOperationalUnknownEffectiveFingerprintStaysSearchOnlyAndCanonical(t *testing.T) {
	p := operationalProjection()
	values, _ := operationalpolicy.Defaults(operationalpolicy.GroupSearch)
	search := operationalObservation(operationalpolicy.GroupSearch, values)
	search.Effective = nil
	search.Error = OperationalErrorInvalidPolicy
	observations := operationalObservations()
	for _, invalid := range []string{"abc", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"} {
		search.EffectiveCompatibilityFingerprint = invalid
		observations[operationalpolicy.GroupSearch] = search
		if _, err := encodeOperationalSettings(p, observations); err == nil {
			t.Fatal("malformed actual Search fingerprint accepted", invalid)
		}
	}
	observations = operationalObservations()
	unsupported := observations[operationalpolicy.GroupSearch]
	unsupported.EffectiveCompatibilityFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	observations[operationalpolicy.GroupSearch] = unsupported
	if _, err := encodeOperationalSettings(p, observations); err == nil {
		t.Fatal("unsupported owner invented an actual fingerprint")
	}
	observations = operationalObservations()
	rateValues, _ := operationalpolicy.Defaults(operationalpolicy.GroupRate)
	rate := operationalObservation(operationalpolicy.GroupRate, rateValues)
	rate.Effective = nil
	rate.Error = OperationalErrorInvalidPolicy
	rate.EffectiveCompatibilityFingerprint = unsupported.EffectiveCompatibilityFingerprint
	observations[operationalpolicy.GroupRate] = rate
	if _, err := encodeOperationalSettings(p, observations); err == nil {
		t.Fatal("non-Search fingerprint accepted with unknown values")
	}
}

func TestOperationalCapacityPreviewPayloadPreservesSampleLabelsAndCounts(t *testing.T) {
	preview := OperationalCapacityPreview{
		Member: 7, EffectiveGeneration: 19,
		VertexFootprint: ^uint64(0), EdgeFootprint: uint64(1) << 40,
		VertexCausalEntries: uint64(1) << 41, EdgeCausalEntries: uint64(1) << 42,
		VerticesOverLimit: true, EdgesOverLimit: false,
		VertexCausalOverLimit: false, EdgeCausalOverLimit: true,
	}
	actual, err := encodeOperationalCapacityPreview(preview)
	want := &pb.OperationalCapacityPreview{
		Member: 7, EffectiveGeneration: 19,
		VertexFootprint: ^uint64(0), EdgeFootprint: uint64(1) << 40,
		VertexCausalEntries: uint64(1) << 41, EdgeCausalEntries: uint64(1) << 42,
		VerticesOverLimit: true, EdgesOverLimit: false,
		VertexCausalOverLimit: false, EdgeCausalOverLimit: true,
	}
	if err != nil || !proto.Equal(actual, want) {
		t.Fatalf("capacity sample payload = %v, %v", actual, err)
	}
	preview.EffectiveGeneration = 0
	if actual, err := encodeOperationalCapacityPreview(preview); err != nil || actual.EffectiveGeneration != 0 {
		t.Fatal("bootstrap sample invented a managed generation", actual, err)
	}
	preview.Member = 0
	if _, err := encodeOperationalCapacityPreview(preview); err == nil {
		t.Fatal("capacity sample without a verified member accepted")
	}
}

func TestOperationalCapacityPreviewRequiresCurrentManagementBeforeSampling(t *testing.T) {
	called := 0
	newHandler := func(current bool) *SecurityConnectHandler {
		h, err := NewSecurityConnectHandler(SecurityServiceOptions{OperationalCapacityPreviewer: func(context.Context, operationalpolicy.Capacity) (OperationalCapacityPreview, error) {
			called++
			return OperationalCapacityPreview{Member: 1}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if h.operationalCapacityPreviewer == nil {
			t.Fatal("capacity callback was not installed")
		}
		if current {
			// This uninstalled facade only exercises refusal before any native
			// read. It cannot create an authenticated management admission.
			h.current = &security.CurrentAuthority{}
		}
		return h
	}
	values, err := operationalpolicy.Defaults(operationalpolicy.GroupCapacity)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		current bool
		ctx     context.Context
		code    connect.Code
	}{
		{"off", false, t.Context(), connect.CodeFailedPrecondition},
		{"unauthenticated", true, t.Context(), connect.CodeUnauthenticated},
		{"uninstalled admission", true, security.WithAdmission(t.Context(), &security.Admission{}), connect.CodeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := newHandler(test.current).PreviewOperationalPolicy(test.ctx, connect.NewRequest(&pb.PreviewOperationalPolicyRequest{Values: encodeOperationalValues(values)}))
			if connect.CodeOf(err) != test.code || response != nil || called != 0 {
				t.Fatal("capacity usage escaped management admission", response, err, called)
			}
		})
	}
	h := newHandler(true)
	unknown := &pb.PreviewOperationalPolicyRequest{Values: encodeOperationalValues(values)}
	unknown.ProtoReflect().SetUnknown([]byte{0x08, 0x01})
	if response, err := h.PreviewOperationalPolicy(t.Context(), connect.NewRequest(unknown)); connect.CodeOf(err) != connect.CodeInvalidArgument || response != nil || called != 0 {
		t.Fatal("unknown preview request reached capacity sampling", response, err, called)
	}
	values.Capacity.GCIntervalSeconds = 0
	if response, err := h.PreviewOperationalPolicy(t.Context(), connect.NewRequest(&pb.PreviewOperationalPolicyRequest{Values: encodeOperationalValues(values)})); connect.CodeOf(err) != connect.CodeInvalidArgument || response != nil || called != 0 {
		t.Fatal("invalid capacity candidate reached sampling", response, err, called)
	}
}
