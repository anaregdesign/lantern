package securitytransport

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func decodeLogLevel(level pb.ManagedLogLevel) (security.LogLevel, error) {
	switch level {
	case pb.ManagedLogLevel_MANAGED_LOG_LEVEL_DEBUG:
		return security.LogLevelDebug, nil
	case pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO:
		return security.LogLevelInfo, nil
	case pb.ManagedLogLevel_MANAGED_LOG_LEVEL_WARN:
		return security.LogLevelWarn, nil
	case pb.ManagedLogLevel_MANAGED_LOG_LEVEL_ERROR:
		return security.LogLevelError, nil
	default:
		return "", security.ErrInvalidImage
	}
}

func encodeLogLevel(level security.LogLevel) pb.ManagedLogLevel {
	switch level {
	case security.LogLevelDebug:
		return pb.ManagedLogLevel_MANAGED_LOG_LEVEL_DEBUG
	case security.LogLevelInfo:
		return pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO
	case security.LogLevelWarn:
		return pb.ManagedLogLevel_MANAGED_LOG_LEVEL_WARN
	case security.LogLevelError:
		return pb.ManagedLogLevel_MANAGED_LOG_LEVEL_ERROR
	default:
		return pb.ManagedLogLevel_MANAGED_LOG_LEVEL_UNSPECIFIED
	}
}

func decodeLogPolicy(policy *pb.ManagedLogPolicy) (*security.LogPolicyChange, error) {
	if policy == nil || strictSecurityMessage(policy) != nil || policy.Version != security.LogPolicyVersion {
		return nil, security.ErrInvalidImage
	}
	level, err := decodeLogLevel(policy.Level)
	if err != nil {
		return nil, err
	}
	return &security.LogPolicyChange{Version: security.LogPolicyVersion, Level: level}, nil
}

func encodeLogSettings(p security.LogPolicyProjection, observed LogSettingsObservation) (*pb.GetLogSettingsResponse, error) {
	response := &pb.GetLogSettingsResponse{SchemaVersion: 1, Member: p.Member, EffectiveLevel: encodeLogLevel(observed.EffectiveLevel), AppliedGeneration: observed.AppliedGeneration, RestartRequired: observed.RestartRequired}
	if p.Member == 0 || response.EffectiveLevel == pb.ManagedLogLevel_MANAGED_LOG_LEVEL_UNSPECIFIED {
		return nil, security.ErrAuthorityUnavailable
	}
	switch observed.Source {
	case LogSettingsSourceDefault:
		response.Source = pb.LogSettingsSource_LOG_SETTINGS_SOURCE_DEFAULT
	case LogSettingsSourceEnvironment:
		response.Source = pb.LogSettingsSource_LOG_SETTINGS_SOURCE_ENVIRONMENT
	case LogSettingsSourceManaged:
		response.Source = pb.LogSettingsSource_LOG_SETTINGS_SOURCE_MANAGED
	default:
		return nil, security.ErrAuthorityUnavailable
	}
	switch observed.Error {
	case LogSettingsErrorNone:
		response.Error = pb.LogSettingsError_LOG_SETTINGS_ERROR_NONE
	case LogSettingsErrorInvalidPolicy:
		response.Error = pb.LogSettingsError_LOG_SETTINGS_ERROR_INVALID_POLICY
	case LogSettingsErrorOwnerUnavailable:
		response.Error = pb.LogSettingsError_LOG_SETTINGS_ERROR_OWNER_UNAVAILABLE
	case LogSettingsErrorApplyFailed:
		response.Error = pb.LogSettingsError_LOG_SETTINGS_ERROR_APPLY_FAILED
	default:
		return nil, security.ErrAuthorityUnavailable
	}
	response.Adoption = pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_UNADOPTED
	if p.VersionFloor {
		response.Adoption = pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_FENCED_UNADOPTED
	}
	if preparation := p.Preparation; preparation != nil {
		id := preparation.ChangeID
		wire := encodeCurrentID(id)
		decoded, err := decodeCurrentID(wire)
		if !p.VersionFloor || err != nil || decoded != id || id.Domain != p.Cut.Domain || id.Cohort != p.Cut.Cohort || id.Domain == [32]byte{} || id.Cohort == [32]byte{} || id.Nonce == [16]byte{} || preparation.IntentDigest == [32]byte{} || preparation.ActorDigest == [32]byte{} {
			return nil, security.ErrAuthorityUnavailable
		}
		response.Preparation = &pb.LogPolicyPreparationReference{ChangeId: wire, IntentDigest: append([]byte(nil), preparation.IntentDigest[:]...), ActorDigest: append([]byte(nil), preparation.ActorDigest[:]...)}
	} else if p.VersionFloor && p.Policy == nil {
		return nil, security.ErrAuthorityUnavailable
	}
	if policy := p.Policy; policy != nil {
		level := encodeLogLevel(policy.Level)
		if !p.VersionFloor || policy.Version != security.LogPolicyVersion || policy.Generation == 0 || level == pb.ManagedLogLevel_MANAGED_LOG_LEVEL_UNSPECIFIED {
			return nil, security.ErrAuthorityUnavailable
		}
		response.Desired = &pb.ManagedLogPolicy{Version: security.LogPolicyVersion, Level: level}
		response.DesiredGeneration = policy.Generation
		response.Adoption = pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_MANAGED
	}
	if observed.AppliedGeneration > response.DesiredGeneration || (observed.Source == LogSettingsSourceManaged) != (observed.AppliedGeneration != 0) {
		return nil, security.ErrAuthorityUnavailable
	}
	if observed.AppliedGeneration != 0 {
		cut := observed.AppliedCut
		if _, err := decodeCurrentCut(encodeCurrentCut(cut)); err != nil || cut.Domain != p.Cut.Domain || cut.Cohort != p.Cut.Cohort || cut.Generation != p.Cut.Generation || cut.Sequence > p.Cut.Sequence {
			return nil, security.ErrAuthorityUnavailable
		}
		response.AppliedCut = encodeCurrentCut(cut)
		if observed.AppliedGeneration == response.DesiredGeneration && response.EffectiveLevel != response.Desired.Level {
			return nil, security.ErrAuthorityUnavailable
		}
	} else if observed.AppliedCut != (security.SemanticCut{}) {
		return nil, security.ErrAuthorityUnavailable
	}
	response.Application = pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_APPLIED
	if observed.Error != LogSettingsErrorNone {
		response.Application = pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_ERROR
	} else if observed.Pending {
		if p.Policy == nil || observed.AppliedGeneration >= response.DesiredGeneration {
			return nil, security.ErrAuthorityUnavailable
		}
		response.Application = pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_PENDING
	} else if observed.AppliedGeneration != response.DesiredGeneration || observed.RestartRequired {
		return nil, security.ErrAuthorityUnavailable
	}
	return response, nil
}

func (h *SecurityConnectHandler) GetLogSettings(ctx context.Context, req *connect.Request[pb.GetLogSettingsRequest]) (*connect.Response[pb.GetLogSettingsResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	a, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	projection, err := h.current.LogPolicyForAdmission(ctx, a)
	cut, current := a.CurrentCut()
	if err != nil || !current || projection.Cut != cut || h.logSettingsObserver == nil {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	observedProjection := projection
	if projection.Policy != nil {
		policy := *projection.Policy
		observedProjection.Policy = &policy
	}
	if projection.Preparation != nil {
		preparation := *projection.Preparation
		observedProjection.Preparation = &preparation
	}
	observed, err := h.logSettingsObserver(observedProjection)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("local log settings unavailable"))
	}
	response, err := encodeLogSettings(projection, observed)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	response.Version = SecurityVersionForAdmission(a)
	return securityReadResponse(ctx, a, h.now(), response)
}
