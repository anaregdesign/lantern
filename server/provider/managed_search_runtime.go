package provider

import (
	"context"
	"sync"
	"time"

	coresearch "github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

// managedSearchCertified pins the exact pre-worker engine installation. Live
// management callbacks stage desired policy; they cannot change this engine.
type managedSearchCertified struct {
	manager      *ManagedOperationalRuntime
	primary      *dataruntime.Executor
	runtime      *ManagedSearchRuntime
	installation dataruntime.SearchPolicyInstallation
}

func (c managedSearchCertified) validFor(manager *ManagedOperationalRuntime, primary *dataruntime.Executor) bool {
	if manager == nil || manager.runtime == nil || manager.runtime.managedOperational != manager || primary == nil || !primary.SharesServingRuntime(manager.runtime.data) || c.manager != manager || c.primary != primary || c.runtime == nil || c.runtime.manager != manager || c.runtime.primary != primary || !primary.UsesSearchPolicyInstallation(c.installation) {
		return false
	}
	manager.mu.Lock()
	entry := manager.search
	manager.mu.Unlock()
	if entry == nil || entry.owner != c.runtime {
		return false
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	return !c.runtime.booting && c.runtime.installation == c.installation && c.runtime.owner != nil && primary.UsesSearchPolicyOwner(c.runtime.owner)
}

// ManagedSearchRuntime owns the native adapter, not a public request capability.
// Its mutex serializes the transition out of pre-worker reconstruction with
// every Apply callback. None of the native callbacks re-enter authority.
type ManagedSearchRuntime struct {
	mu               sync.Mutex
	manager          *ManagedOperationalRuntime
	primary          *dataruntime.Executor
	owner            *dataruntime.SearchPolicyOwner
	baseline         dataruntime.SearchLimits
	bootstrapSources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
	booting          bool
	installation     dataruntime.SearchPolicyInstallation
}

func NewManagedSearchRuntime(ctx context.Context, manager *ManagedOperationalRuntime, primary *dataruntime.Executor) (managedSearchCertified, error) {
	if manager == nil || manager.runtime == nil || primary == nil || primary.HasSearchPolicyOwner() || !primary.SharesServingRuntime(manager.runtime.data) || ctx.Err() != nil {
		return managedSearchCertified{}, security.ErrOperationalPolicyUnavailable
	}
	sources, err := manager.BootstrapSources(operationalpolicy.GroupSearch)
	if err != nil || len(sources) != 11 {
		return managedSearchCertified{}, security.ErrOperationalPolicyUnavailable
	}
	baseline := primary.SearchPolicyBaseline()
	bootstrapVersion := dataruntime.SearchPolicyVersion{Fingerprint: primary.SearchConfigFingerprint()}
	var authority dataruntime.SearchPolicyAuthority
	switch manager.runtime.mode {
	case "off":
	case "oidc":
		manager.mu.Lock()
		scope := manager.projection.Cut
		manager.mu.Unlock()
		if scope.Domain == [32]byte{} || scope.Cohort == [32]byte{} || scope.Generation == [16]byte{} {
			return managedSearchCertified{}, security.ErrAuthorityUnavailable
		}
		authority = &managedSearchAdmissionAuthority{scope: scope, bootstrap: bootstrapVersion}
	default:
		return managedSearchCertified{}, security.ErrOperationalPolicyUnavailable
	}
	owner, err := dataruntime.NewBootstrapSearchPolicyOwner(baseline, authority)
	if err != nil {
		return managedSearchCertified{}, err
	}
	runtime := &ManagedSearchRuntime{manager: manager, primary: primary, owner: owner, baseline: baseline, bootstrapSources: sources, booting: true}
	primary.WithSearchPolicyOwner(owner)
	if err := manager.Register(ctx, operationalpolicy.GroupSearch, runtime); err != nil {
		return managedSearchCertified{}, err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	installation, ok := primary.CaptureSearchPolicyInstallation(owner)
	if !ok || ctx.Err() != nil {
		return managedSearchCertified{}, security.ErrOperationalPolicyUnavailable
	}
	// Future callbacks can only stage policy after this point. The immutable
	// installation identity is the dependency consumed by both peer workers.
	runtime.booting = false
	runtime.installation = installation
	return managedSearchCertified{manager, primary, runtime, installation}, nil
}

// A verified private data Admission supplies current desired policy without
// requiring the caller's management grants. A missing snapshot is never proof
// of unadopted state and never permits environment fallback.
type managedSearchAdmissionAuthority struct {
	scope     security.SemanticCut
	bootstrap dataruntime.SearchPolicyVersion
}

func (s *managedSearchAdmissionAuthority) DesiredSearchPolicy(ctx context.Context) (dataruntime.SearchPolicyVersion, error) {
	a, ok := security.AdmissionFromContext(ctx)
	if s == nil || !ok || a.Check(ctx, a.ExpiresAt()) != nil {
		return dataruntime.SearchPolicyVersion{}, security.ErrAuthorityUnavailable
	}
	cut, current := a.CurrentCut()
	snapshot := a.Snapshot()
	if !current || snapshot == nil || cut.Domain != s.scope.Domain || cut.Cohort != s.scope.Cohort || cut.Generation != s.scope.Generation {
		return dataruntime.SearchPolicyVersion{}, security.ErrAuthorityUnavailable
	}
	policies := snapshot.OperationalPolicies()
	if policies.ValidateNilAllowed() != nil {
		return dataruntime.SearchPolicyVersion{}, security.ErrAuthorityUnavailable
	}
	policy := policies.Get(operationalpolicy.GroupSearch)
	if policy == nil {
		return s.bootstrap, nil
	}
	if policy.Validate() != nil {
		return dataruntime.SearchPolicyVersion{}, security.ErrAuthorityUnavailable
	}
	return dataruntime.SearchPolicyVersion{Generation: policy.Generation, Fingerprint: policy.CompatibilityFingerprint}, nil
}

func (r *ManagedSearchRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if r == nil || group != operationalpolicy.GroupSearch || version != operationalpolicy.Version || r.manager == nil || r.manager.runtime == nil || r.primary == nil || r.owner == nil || !r.primary.SharesServingRuntime(r.manager.runtime.data) || !r.primary.UsesSearchPolicyOwner(r.owner) {
		return security.ErrOperationalPolicyUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.booting && !r.primary.UsesSearchPolicyInstallation(r.installation) {
		return security.ErrOperationalPolicyUnavailable
	}
	return nil
}

func searchRuntimePolicy(values operationalpolicy.Values) (dataruntime.SearchRequestPolicy, error) {
	group, err := values.Group()
	if err != nil || group != operationalpolicy.GroupSearch || values.Validate() != nil {
		return dataruntime.SearchRequestPolicy{}, operationalpolicy.ErrInvalid
	}
	v := values.Search
	if int64(int(v.MaxQueryBytes)) != v.MaxQueryBytes {
		return dataruntime.SearchRequestPolicy{}, operationalpolicy.ErrInvalid
	}
	var mode coresearch.MatchMode
	switch v.DefaultMode {
	case operationalpolicy.MatchAny:
		mode = coresearch.MatchAny
	case operationalpolicy.MatchAll:
		mode = coresearch.MatchAll
	case operationalpolicy.MatchMinShould:
		mode = coresearch.MatchMinShould
	default:
		return dataruntime.SearchRequestPolicy{}, operationalpolicy.ErrInvalid
	}
	return dataruntime.SearchRequestPolicy{DefaultLimit: uint32(v.DefaultLimit), MaxLimit: uint32(v.MaxLimit), DefaultMode: mode, DefaultMinShould: uint32(v.DefaultMinShould), Timeout: time.Duration(v.TimeoutMS) * time.Millisecond, MaxQueryBytes: int(v.MaxQueryBytes), WorkBudget: coresearch.Budget{MaxQueryTerms: v.MaxQueryTerms, MaxDictionaryVisits: v.MaxDictionaryVisits, MaxPostingVisits: v.MaxPostingVisits, MaxPositionVisits: v.MaxPositionVisits, MaxExpirationVisits: v.MaxExpirationVisits}}, nil
}

func (r *ManagedSearchRuntime) PreviewOperationalPolicy(values operationalpolicy.Values) (string, error) {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupSearch, operationalpolicy.Version) != nil {
		return "", security.ErrOperationalPolicyUnavailable
	}
	policy, err := searchRuntimePolicy(values)
	if err != nil {
		return "", err
	}
	resolved, err := dataruntime.ResolveSearchPolicy(r.baseline, 0, policy)
	return resolved.Version.Fingerprint, err
}

func (r *ManagedSearchRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if change.Validate() != nil {
		return operationalpolicy.ErrInvalid
	}
	fingerprint, err := r.PreviewOperationalPolicy(change.Values)
	if err != nil || fingerprint != change.CompatibilityFingerprint {
		return security.ErrOperationalPolicyUnavailable
	}
	return nil
}

func (r *ManagedSearchRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupSearch, operationalpolicy.Version) != nil || policy.Validate() != nil {
		return security.ErrOperationalPolicyUnavailable
	}
	values, err := searchRuntimePolicy(policy.Values)
	if err != nil {
		return err
	}
	resolved, err := dataruntime.ResolveSearchPolicy(r.baseline, policy.Generation, values)
	if err != nil || resolved.Version.Fingerprint != policy.CompatibilityFingerprint {
		return security.ErrOperationalPolicyUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.owner.StageDesired(resolved); err != nil {
		return err
	}
	if r.booting {
		return r.owner.Restart(resolved)
	}
	return nil
}

func (r *ManagedSearchRuntime) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupSearch, operationalpolicy.Version) != nil {
		return OperationalOwnerObservation{}, security.ErrOperationalPolicyUnavailable
	}
	actual := r.owner.Status().Effective
	observation := OperationalOwnerObservation{EffectiveGeneration: actual.Version.Generation, Sources: cloneOperationalSources(r.bootstrapSources), Fingerprint: actual.Version.Fingerprint, Mode: securitytransport.OperationalModeCoordinatedRestart, Error: securitytransport.OperationalErrorNone}
	if actual.Version.Generation != 0 {
		for key := range observation.Sources {
			observation.Sources[key] = securitytransport.OperationalSourceManaged
		}
	}
	p := actual.Policy
	var mode operationalpolicy.MatchMode
	switch p.DefaultMode {
	case coresearch.MatchAny:
		mode = operationalpolicy.MatchAny
	case coresearch.MatchAll:
		mode = operationalpolicy.MatchAll
	case coresearch.MatchMinShould:
		mode = operationalpolicy.MatchMinShould
	}
	values := operationalpolicy.Values{Search: &operationalpolicy.Search{DefaultLimit: int64(p.DefaultLimit), MaxLimit: int64(p.MaxLimit), DefaultMode: mode, DefaultMinShould: int64(p.DefaultMinShould), TimeoutMS: p.Timeout.Milliseconds(), MaxQueryBytes: int64(p.MaxQueryBytes), MaxQueryTerms: p.WorkBudget.MaxQueryTerms, MaxDictionaryVisits: p.WorkBudget.MaxDictionaryVisits, MaxPostingVisits: p.WorkBudget.MaxPostingVisits, MaxPositionVisits: p.WorkBudget.MaxPositionVisits, MaxExpirationVisits: p.WorkBudget.MaxExpirationVisits}}
	if p.Timeout%time.Millisecond != 0 || values.Validate() != nil {
		if actual.Version.Generation != 0 {
			return OperationalOwnerObservation{}, security.ErrOperationalPolicyUnavailable
		}
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	observation.Effective = &values
	return observation, nil
}
