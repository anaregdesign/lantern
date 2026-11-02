package securitytransport

import (
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func TestLogPolicyCodecRejectsNoncanonicalIntent(t *testing.T) {
	for _, level := range []pb.ManagedLogLevel{pb.ManagedLogLevel_MANAGED_LOG_LEVEL_DEBUG, pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO, pb.ManagedLogLevel_MANAGED_LOG_LEVEL_WARN, pb.ManagedLogLevel_MANAGED_LOG_LEVEL_ERROR} {
		policy := &pb.ManagedLogPolicy{Version: 1, Level: level}
		decoded, err := decodeLogPolicy(policy)
		if err != nil || encodeLogLevel(decoded.Level) != level || decoded.Version != security.LogPolicyVersion {
			t.Fatalf("log policy roundtrip: %+v, %v", decoded, err)
		}
		change, err := decodeSecurityChange(&pb.SecurityChange{Operation: &pb.SecurityChange_PutLogPolicy{PutLogPolicy: policy}})
		if err != nil || change.Kind != security.PutLogPolicy || change.LogPolicy == nil || *change.LogPolicy != *decoded {
			t.Fatalf("typed log change: %+v, %v", change, err)
		}
	}
	for name, policy := range map[string]*pb.ManagedLogPolicy{
		"absent":         nil,
		"unknown schema": {Version: 2, Level: pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO},
		"missing level":  {Version: 1},
		"unknown level":  {Version: 1, Level: pb.ManagedLogLevel(99)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeLogPolicy(policy); err == nil {
				t.Fatal("noncanonical log intent accepted")
			}
		})
	}
	policy := &pb.ManagedLogPolicy{Version: 1, Level: pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO}
	// Generation is Server-owned; an unknown field cannot smuggle it into intent.
	policy.ProtoReflect().SetUnknown([]byte{0x18, 0x07})
	if _, err := decodeLogPolicy(policy); err == nil {
		t.Fatal("unknown policy field accepted")
	}
	if encodeLogLevel(security.LogLevel("unknown")) != pb.ManagedLogLevel_MANAGED_LOG_LEVEL_UNSPECIFIED {
		t.Fatal("unknown domain level acquired a default")
	}
}

func logSettingsProjection() security.LogPolicyProjection {
	return security.LogPolicyProjection{Member: 1, Cut: security.SemanticCut{
		Version: security.S1Version, Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{3}, Sequence: 9,
		Projection: [32]byte{4}, Frontier: [32]byte{5}, Fences: [32]byte{6}, Policy: [32]byte{7},
	}}
}

func logSettingsPreparation(p security.LogPolicyProjection) *security.LogPolicyPreparation {
	return &security.LogPolicyPreparation{ChangeID: security.FullChangeID{Version: security.S1Version, Domain: p.Cut.Domain, Cohort: p.Cut.Cohort, Namespace: 11, Nonce: [16]byte{3}}, IntentDigest: [32]byte{4}, ActorDigest: [32]byte{5}}
}

func TestLogSettingsReportsLocalApplicationSeparately(t *testing.T) {
	p := logSettingsProjection()
	observed := LogSettingsObservation{EffectiveLevel: security.LogLevelInfo, Source: LogSettingsSourceEnvironment, Error: LogSettingsErrorNone}
	r, err := encodeLogSettings(p, observed)
	if err != nil || r.Adoption != pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_UNADOPTED || r.Desired != nil || r.DesiredGeneration != 0 || r.AppliedGeneration != 0 || r.AppliedCut != nil || r.Application != pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_APPLIED {
		t.Fatalf("unadopted: %+v, %v", r, err)
	}
	p.VersionFloor = true
	p.Preparation = logSettingsPreparation(p)
	r, err = encodeLogSettings(p, observed)
	if err != nil || r.Adoption != pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_FENCED_UNADOPTED || r.Preparation == nil || !proto.Equal(r.Preparation.ChangeId, encodeCurrentID(p.Preparation.ChangeID)) {
		t.Fatalf("fenced but unadopted: %+v, %v", r, err)
	}
	r.Preparation.ActorDigest[0]++
	if p.Preparation.ActorDigest != [32]byte{5} {
		t.Fatal("wire preparation aliases the admitted projection")
	}
	p.Policy = &security.LogPolicy{Version: 1, Generation: 2, Level: security.LogLevelWarn}
	observed.Pending = true
	r, err = encodeLogSettings(p, observed)
	if err != nil || r.Application != pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_PENDING || r.DesiredGeneration != 2 || r.AppliedGeneration != 0 || r.EffectiveLevel != pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO || r.Source != pb.LogSettingsSource_LOG_SETTINGS_SOURCE_ENVIRONMENT {
		t.Fatalf("committed pending: %+v, %v", r, err)
	}
	observed.Error = LogSettingsErrorApplyFailed
	r, err = encodeLogSettings(p, observed)
	if err != nil || r.Application != pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_ERROR || r.Error != pb.LogSettingsError_LOG_SETTINGS_ERROR_APPLY_FAILED || r.AppliedGeneration != 0 {
		t.Fatalf("local apply error: %+v, %v", r, err)
	}
	observed = LogSettingsObservation{EffectiveLevel: security.LogLevelWarn, Source: LogSettingsSourceManaged, AppliedGeneration: 2, AppliedCut: p.Cut, Error: LogSettingsErrorNone}
	r, err = encodeLogSettings(p, observed)
	if err != nil || r.Application != pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_APPLIED || r.AppliedGeneration != r.DesiredGeneration || !proto.Equal(r.AppliedCut, encodeCurrentCut(p.Cut)) {
		t.Fatalf("local installed: %+v, %v", r, err)
	}
	// An unrelated security commit preserves the already installed policy and
	// its historical provenance; it does not invent pending logger work.
	p.Cut.Sequence++
	r, err = encodeLogSettings(p, observed)
	if err != nil || r.Application != pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_APPLIED || !proto.Equal(r.AppliedCut, encodeCurrentCut(observed.AppliedCut)) {
		t.Fatalf("historical installation remains applied: %+v, %v", r, err)
	}
}

func TestLogSettingsPreparationReferenceRequiresExactBinding(t *testing.T) {
	for name, edit := range map[string]func(*security.LogPolicyProjection){
		"missing":            func(p *security.LogPolicyProjection) { p.Preparation = nil },
		"without floor":      func(p *security.LogPolicyProjection) { p.VersionFloor = false },
		"unknown ID version": func(p *security.LogPolicyProjection) { p.Preparation.ChangeID.Version++ },
		"foreign profile":    func(p *security.LogPolicyProjection) { p.Preparation.ChangeID.Cohort[0]++ },
		"empty nonce":        func(p *security.LogPolicyProjection) { p.Preparation.ChangeID.Nonce = [16]byte{} },
		"empty intent":       func(p *security.LogPolicyProjection) { p.Preparation.IntentDigest = [32]byte{} },
		"empty actor":        func(p *security.LogPolicyProjection) { p.Preparation.ActorDigest = [32]byte{} },
	} {
		t.Run(name, func(t *testing.T) {
			p := logSettingsProjection()
			p.VersionFloor = true
			p.Preparation = logSettingsPreparation(p)
			edit(&p)
			if _, err := encodeLogSettings(p, LogSettingsObservation{EffectiveLevel: security.LogLevelInfo, Source: LogSettingsSourceDefault, Error: LogSettingsErrorNone}); err == nil {
				t.Fatal("unbound preparation reference accepted")
			}
		})
	}
}

func TestLogSettingsRejectsForeignOrMismatchedInstallation(t *testing.T) {
	for name, edit := range map[string]func(*LogSettingsObservation){
		"another profile":                 func(o *LogSettingsObservation) { o.AppliedCut.Generation[0]++ },
		"future cut":                      func(o *LogSettingsObservation) { o.AppliedCut.Sequence++ },
		"same generation different level": func(o *LogSettingsObservation) { o.EffectiveLevel = security.LogLevelInfo },
		"managed installation claims env": func(o *LogSettingsObservation) { o.Source = LogSettingsSourceEnvironment },
	} {
		t.Run(name, func(t *testing.T) {
			p := logSettingsProjection()
			p.VersionFloor = true
			p.Policy = &security.LogPolicy{Version: 1, Generation: 2, Level: security.LogLevelWarn}
			observed := LogSettingsObservation{EffectiveLevel: security.LogLevelWarn, Source: LogSettingsSourceManaged, AppliedGeneration: 2, AppliedCut: p.Cut, Error: LogSettingsErrorNone}
			edit(&observed)
			if _, err := encodeLogSettings(p, observed); err == nil {
				t.Fatal("foreign or mismatched installation accepted")
			}
		})
	}
}

func TestLogSettingsCannotInventApplicationOrFallback(t *testing.T) {
	for name, edit := range map[string]func(*security.LogPolicyProjection, *LogSettingsObservation){
		"missing member":               func(p *security.LogPolicyProjection, o *LogSettingsObservation) { p.Member = 0 },
		"unknown level":                func(p *security.LogPolicyProjection, o *LogSettingsObservation) { o.EffectiveLevel = "unknown" },
		"unknown source":               func(p *security.LogPolicyProjection, o *LogSettingsObservation) { o.Source = 0 },
		"unknown error":                func(p *security.LogPolicyProjection, o *LogSettingsObservation) { o.Error = 0 },
		"pending without desired":      func(p *security.LogPolicyProjection, o *LogSettingsObservation) { o.Pending = true },
		"managed without installation": func(p *security.LogPolicyProjection, o *LogSettingsObservation) { o.Source = LogSettingsSourceManaged },
		"future generation":            func(p *security.LogPolicyProjection, o *LogSettingsObservation) { o.AppliedGeneration = 1 },
		"cut without installation":     func(p *security.LogPolicyProjection, o *LogSettingsObservation) { o.AppliedCut = p.Cut },
		"unknown desired schema": func(p *security.LogPolicyProjection, o *LogSettingsObservation) {
			p.VersionFloor = true
			p.Policy = &security.LogPolicy{Version: 2, Generation: 1, Level: security.LogLevelInfo}
		},
		"missing floor": func(p *security.LogPolicyProjection, o *LogSettingsObservation) {
			p.Policy = &security.LogPolicy{Version: 1, Generation: 1, Level: security.LogLevelInfo}
		},
		"committed does not imply installed": func(p *security.LogPolicyProjection, o *LogSettingsObservation) {
			p.VersionFloor = true
			p.Policy = &security.LogPolicy{Version: 1, Generation: 1, Level: security.LogLevelInfo}
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := logSettingsProjection()
			observed := LogSettingsObservation{EffectiveLevel: security.LogLevelInfo, Source: LogSettingsSourceDefault, Error: LogSettingsErrorNone}
			edit(&p, &observed)
			if _, err := encodeLogSettings(p, observed); err == nil {
				t.Fatal("inconsistent local observation accepted")
			}
		})
	}
}

func TestGetLogSettingsRequiresCurrentComposition(t *testing.T) {
	called := false
	h, err := NewSecurityConnectHandler(SecurityServiceOptions{LogSettingsObserver: func(security.LogPolicyProjection) (LogSettingsObservation, error) {
		called = true
		return LogSettingsObservation{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetLogSettings(t.Context(), connect.NewRequest(&pb.GetLogSettingsRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition || called {
		t.Fatalf("OFF leaked a settings observation: %v, called=%v", err, called)
	}
	request := &pb.GetLogSettingsRequest{}
	request.ProtoReflect().SetUnknown([]byte{0x08, 0x01})
	if _, err := h.GetLogSettings(t.Context(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown request input accepted", err)
	}
}
