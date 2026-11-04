package securitytransport

import (
	"context"
	"encoding/hex"
	"reflect"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// UnavailableOperationalSettingsObservation explicitly disables an uninstalled
// group. It contains no fabricated effective values or application evidence.
func UnavailableOperationalSettingsObservation() OperationalSettingsObservation {
	return OperationalSettingsObservation{Mode: OperationalModeUnavailable, Error: OperationalErrorOwnerUnavailable}
}

func operationalSettingsFields(group operationalpolicy.Group) []pb.OperationalSetting {
	bounds := map[operationalpolicy.Group][2]int32{
		operationalpolicy.GroupSlowRPC: {1, 1}, operationalpolicy.GroupRate: {2, 3}, operationalpolicy.GroupRequest: {4, 15},
		operationalpolicy.GroupBackup: {16, 18}, operationalpolicy.GroupCapacity: {19, 24}, operationalpolicy.GroupLifecycle: {25, 27},
		operationalpolicy.GroupSearch: {28, 38}, operationalpolicy.GroupReplication: {39, 43},
	}
	bound, ok := bounds[group]
	if !ok {
		return nil
	}
	fields := make([]pb.OperationalSetting, 0, bound[1]-bound[0]+1)
	for i := bound[0]; i <= bound[1]; i++ {
		fields = append(fields, pb.OperationalSetting(i))
	}
	return fields
}

func encodeOperationalSettings(p security.OperationalPolicyProjection, observations map[operationalpolicy.Group]OperationalSettingsObservation) (*pb.GetOperationalSettingsResponse, error) {
	if p.Member == 0 || p.Policies.ValidateNilAllowed() != nil || len(observations) != 8 || len(p.Preparations) > 8 {
		return nil, security.ErrAuthorityUnavailable
	}
	response := &pb.GetOperationalSettingsResponse{SchemaVersion: 1, Member: p.Member}
	preparations := make(map[operationalpolicy.Group]*pb.LogPolicyPreparationReference)
	for _, preparation := range p.Preparations {
		id := preparation.ChangeID
		wire := encodeCurrentID(id)
		decoded, err := decodeCurrentID(wire)
		if !preparation.Group.Valid() || preparation.Version != operationalpolicy.Version || preparations[preparation.Group] != nil || err != nil || decoded != id || id.Domain != p.Cut.Domain || id.Cohort != p.Cut.Cohort || preparation.IntentDigest == [32]byte{} || preparation.ActorDigest == [32]byte{} {
			return nil, security.ErrAuthorityUnavailable
		}
		preparations[preparation.Group] = &pb.LogPolicyPreparationReference{ChangeId: wire, IntentDigest: append([]byte(nil), preparation.IntentDigest[:]...), ActorDigest: append([]byte(nil), preparation.ActorDigest[:]...)}
	}
	for _, group := range operationalpolicy.Groups() {
		observed, ok := observations[group]
		if !ok {
			return nil, security.ErrAuthorityUnavailable
		}
		state, err := encodeOperationalGroupSettings(p, group, preparations[group], observed)
		if err != nil {
			return nil, err
		}
		response.Groups = append(response.Groups, state)
	}
	return response, nil
}

func encodeOperationalGroupSettings(p security.OperationalPolicyProjection, group operationalpolicy.Group, preparation *pb.LogPolicyPreparationReference, observed OperationalSettingsObservation) (*pb.OperationalGroupSettings, error) {
	state := &pb.OperationalGroupSettings{Group: encodeOperationalGroup(group), ChangeMode: observed.Mode, Error: observed.Error, RestartRequired: observed.RestartRequired, AppliedGeneration: observed.AppliedGeneration, EffectiveCompatibilityFingerprint: observed.EffectiveCompatibilityFingerprint, Preparation: preparation, Adoption: pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_UNADOPTED}
	if preparation != nil {
		state.Adoption = pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_FENCED_UNADOPTED
	}
	policy := p.Policies.Get(group)
	if policy != nil {
		if preparation == nil || policy.Validate() != nil {
			return nil, security.ErrAuthorityUnavailable
		}
		state.Desired = &pb.ManagedOperationalPolicy{Version: policy.Version, Generation: policy.Generation, Values: encodeOperationalValues(policy.Values), CompatibilityFingerprint: policy.CompatibilityFingerprint}
		state.Adoption = pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_MANAGED
	}
	if observed.RestartRequired && (policy == nil || observed.Mode != OperationalModeProcessRestart && observed.Mode != OperationalModeCoordinatedRestart) {
		return nil, security.ErrAuthorityUnavailable
	}
	if observed.Effective == nil {
		if observed.AppliedGeneration != 0 || observed.AppliedCut != (security.SemanticCut{}) || (observed.Pending && policy == nil) {
			return nil, security.ErrAuthorityUnavailable
		}
		if observed.Mode == OperationalModeUnavailable && observed.Error == OperationalErrorOwnerUnavailable && len(observed.Sources) == 0 && !observed.RestartRequired && !observed.Pending && observed.EffectiveCompatibilityFingerprint == "" {
			state.Application = pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_UNAVAILABLE
			return state, nil
		}
		if observed.Mode < OperationalModeHot || observed.Mode > OperationalModeCoordinatedRestart || observed.Error != OperationalErrorInvalidPolicy {
			return nil, security.ErrAuthorityUnavailable
		}
		// The actual Search engine can be known even when legacy selected
		// values cannot be represented by the managed schema. This metadata
		// neither substitutes typed values nor establishes an applied policy.
		if observed.EffectiveCompatibilityFingerprint != "" && (group != operationalpolicy.GroupSearch || !validOperationalPreviewFingerprint(observed.EffectiveCompatibilityFingerprint, true)) {
			return nil, security.ErrAuthorityUnavailable
		}
		provenance, err := encodeOperationalProvenance(group, observed)
		if err != nil {
			return nil, err
		}
		state.Provenance = provenance
		state.Application = pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR
		return state, nil
	}

	effectiveGroup, err := observed.Effective.Group()
	if err != nil || effectiveGroup != group || observed.Effective.Validate() != nil || observed.Mode < OperationalModeHot || observed.Mode > OperationalModeCoordinatedRestart || observed.Error < OperationalErrorNone || observed.Error > OperationalErrorCompatibilityMismatch {
		return nil, security.ErrAuthorityUnavailable
	}
	if group == operationalpolicy.GroupSearch {
		if (&operationalpolicy.Policy{Version: 1, Generation: 1, Values: observed.Effective.Clone(), CompatibilityFingerprint: observed.EffectiveCompatibilityFingerprint}).Validate() != nil {
			return nil, security.ErrAuthorityUnavailable
		}
	} else if observed.EffectiveCompatibilityFingerprint != "" {
		return nil, security.ErrAuthorityUnavailable
	}
	state.Effective = encodeOperationalValues(observed.Effective.Clone())
	provenance, err := encodeOperationalProvenance(group, observed)
	if err != nil {
		return nil, err
	}
	state.Provenance = provenance

	desiredGeneration := uint64(0)
	if policy != nil {
		desiredGeneration = policy.Generation
	}
	if observed.AppliedGeneration > desiredGeneration {
		return nil, security.ErrAuthorityUnavailable
	}
	if observed.AppliedGeneration != 0 {
		cut := observed.AppliedCut
		if _, err := decodeCurrentCut(encodeCurrentCut(cut)); err != nil || cut.Domain != p.Cut.Domain || cut.Cohort != p.Cut.Cohort || cut.Generation != p.Cut.Generation || cut.Sequence > p.Cut.Sequence || cut.Sequence == p.Cut.Sequence && cut != p.Cut {
			return nil, security.ErrAuthorityUnavailable
		}
		state.AppliedCut = encodeCurrentCut(cut)
		if observed.AppliedGeneration == desiredGeneration && (!reflect.DeepEqual(*observed.Effective, policy.Values) || observed.EffectiveCompatibilityFingerprint != policy.CompatibilityFingerprint) {
			return nil, security.ErrAuthorityUnavailable
		}
	} else if observed.AppliedCut != (security.SemanticCut{}) {
		return nil, security.ErrAuthorityUnavailable
	}
	switch {
	case observed.Error != OperationalErrorNone:
		state.Application = pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR
	case observed.Pending:
		if policy == nil || observed.AppliedGeneration >= desiredGeneration || (observed.RestartRequired && observed.Mode != OperationalModeProcessRestart && observed.Mode != OperationalModeCoordinatedRestart) {
			return nil, security.ErrAuthorityUnavailable
		}
		state.Application = pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_PENDING
	default:
		if observed.AppliedGeneration != desiredGeneration || observed.RestartRequired {
			return nil, security.ErrAuthorityUnavailable
		}
		state.Application = pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED
	}
	return state, nil
}

func (h *SecurityConnectHandler) GetOperationalSettings(ctx context.Context, req *connect.Request[pb.GetOperationalSettingsRequest]) (*connect.Response[pb.GetOperationalSettingsResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	a, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	projection, err := h.current.OperationalPoliciesForAdmission(ctx, a)
	cut, current := a.CurrentCut()
	if err != nil || !current || projection.Cut != cut || h.operationalSettingsObserver == nil {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	detached := projection
	detached.Policies = projection.Policies.Clone()
	detached.Preparations = append([]security.OperationalPolicyPreparation(nil), projection.Preparations...)
	observed, err := h.operationalSettingsObserver(detached)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	response, err := encodeOperationalSettings(projection, observed)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	response.Version = SecurityVersionForAdmission(a)
	return securityReadResponse(ctx, a, h.now(), response)
}

func (h *SecurityConnectHandler) PreviewOperationalPolicy(ctx context.Context, req *connect.Request[pb.PreviewOperationalPolicyRequest]) (*connect.Response[pb.PreviewOperationalPolicyResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	values, err := decodeOperationalValues(req.Msg.Values)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	a, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	fingerprint, err := h.current.PreviewOperationalPolicy(ctx, a, values)
	if err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	group, _ := values.Group()
	response := &pb.PreviewOperationalPolicyResponse{SchemaVersion: 1, Group: encodeOperationalGroup(group), CompatibilityFingerprint: fingerprint, Version: SecurityVersionForAdmission(a)}
	if group == operationalpolicy.GroupBackup {
		if h.operationalBackupPreviewer == nil {
			return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
		}
		preview, previewErr := h.operationalBackupPreviewer(ctx, values.Backup.Retain)
		if previewErr != nil || preview.PruneBackups > preview.ValidBackups || preview.PruneBytes > preview.ValidBytes || !validOperationalPreviewFingerprint(preview.Fingerprint, preview.Complete) {
			return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
		}
		response.BackupRetention = &pb.OperationalBackupRetentionPreview{ValidBackups: preview.ValidBackups, ValidBytes: preview.ValidBytes, PruneBackups: preview.PruneBackups, PruneBytes: preview.PruneBytes, Complete: preview.Complete, Fingerprint: preview.Fingerprint}
	}
	if group == operationalpolicy.GroupCapacity {
		if h.operationalCapacityPreviewer == nil {
			return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
		}
		preview, previewErr := h.operationalCapacityPreviewer(ctx, *values.Capacity)
		if previewErr != nil {
			return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
		}
		response.CapacityUsage, err = encodeOperationalCapacityPreview(preview)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
		}
	}
	return securityReadResponse(ctx, a, h.now(), response)
}

func encodeOperationalCapacityPreview(preview OperationalCapacityPreview) (*pb.OperationalCapacityPreview, error) {
	if preview.Member == 0 {
		return nil, security.ErrAuthorityUnavailable
	}
	return &pb.OperationalCapacityPreview{
		Member: preview.Member, EffectiveGeneration: preview.EffectiveGeneration,
		VertexFootprint: preview.VertexFootprint, EdgeFootprint: preview.EdgeFootprint,
		VertexCausalEntries: preview.VertexCausalEntries, EdgeCausalEntries: preview.EdgeCausalEntries,
		VerticesOverLimit: preview.VerticesOverLimit, EdgesOverLimit: preview.EdgesOverLimit,
		VertexCausalOverLimit: preview.VertexCausalOverLimit, EdgeCausalOverLimit: preview.EdgeCausalOverLimit,
	}, nil
}

func validOperationalPreviewFingerprint(value string, required bool) bool {
	if value == "" {
		return !required
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}

func encodeOperationalProvenance(group operationalpolicy.Group, observed OperationalSettingsObservation) ([]*pb.OperationalSettingProvenance, error) {
	fields := operationalSettingsFields(group)
	if len(observed.Sources) != len(fields) {
		return nil, security.ErrAuthorityUnavailable
	}
	result := make([]*pb.OperationalSettingProvenance, 0, len(fields))
	for _, field := range fields {
		source, ok := observed.Sources[field]
		if !ok || source < OperationalSourceDefault || source > OperationalSourceManaged || (source == OperationalSourceManaged) != (observed.AppliedGeneration != 0) {
			return nil, security.ErrAuthorityUnavailable
		}
		result = append(result, &pb.OperationalSettingProvenance{Setting: field, Source: source})
	}
	return result, nil
}
