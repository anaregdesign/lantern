package provider

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

var errManagedLogRuntime = errors.New("managed log runtime unavailable")

type logPolicyAuthority interface {
	CurrentLogPolicy(context.Context) (security.LogPolicyProjection, error)
	ReconcileLogPolicy(context.Context) (security.LogPolicyProjection, error)
}

// managedLogCertified forces the actual logger owner and verified initial
// projection to exist before public listener/security certification.
type managedLogCertified struct {
	runtime *SecurityRuntime
	owner   *ServingLogger
	adapter *managedLogRuntime
}

// managedLogRuntime is a trusted local adapter, never a management request
// capability. Native committed state supplies desired policy; this owner records
// only what was actually installed in this process.
type managedLogRuntime struct {
	refresh    sync.Mutex
	mu         sync.Mutex
	owner      *ServingLogger
	authority  logPolicyAuthority
	member     uint32
	scope      security.SemanticCut
	desired    security.LogPolicyProjection
	applied    security.LogPolicy
	appliedCut security.SemanticCut
	source     securitytransport.LogSettingsSource
	lastError  securitytransport.LogSettingsError
}

func NewManagedLogRuntime(ctx context.Context, runtime *SecurityRuntime, owner *ServingLogger) (managedLogCertified, error) {
	if err := ctx.Err(); err != nil {
		return managedLogCertified{}, err
	}
	if runtime == nil || runtime.managedLog != nil {
		return managedLogCertified{}, errManagedLogRuntime
	}
	adapter := &managedLogRuntime{owner: owner, source: securitytransport.LogSettingsSourceDefault}
	if owner != nil && owner.fromEnvironment {
		adapter.source = securitytransport.LogSettingsSourceEnvironment
	}
	if err := adapter.ownerReady(); err != nil {
		return managedLogCertified{}, err
	}
	switch runtime.mode {
	case "off":
		// OFF remains startup-owned and has no control-plane policy writer.
	case "oidc":
		if runtime.current == nil {
			return managedLogCertified{}, errManagedLogRuntime
		}
		adapter.authority = runtime.current
		// Local reconstruction was verified by OpenCurrentAuthority. Do not wait
		// for fresh quorum here: its private listener must remain available for
		// the other fixed members to start. Public readiness still requires the
		// existing native current-eligibility checks below.
		projection, err := runtime.current.CurrentLogPolicy(ctx)
		if err != nil {
			return managedLogCertified{}, err
		}
		if err := adapter.observeProjection(projection); err != nil {
			return managedLogCertified{}, err
		}
		if projection.Policy != nil {
			if err := adapter.ApplyLogPolicy(*projection.Policy); err != nil {
				return managedLogCertified{}, err
			}
		}
		// Support cannot be advertised while this owner is still uninitialized.
		// Bind only after the locally verified recovered value is installed.
		if err := runtime.current.BindLogPolicyRuntime(adapter); err != nil {
			return managedLogCertified{}, err
		}
		if err := adapter.reconcile(ctx); err != nil {
			return managedLogCertified{}, err
		}
	default:
		return managedLogCertified{}, errManagedLogRuntime
	}
	if err := ctx.Err(); err != nil {
		return managedLogCertified{}, err
	}
	runtime.managedLog = adapter
	return managedLogCertified{runtime, owner, adapter}, nil
}

func (m *managedLogRuntime) PrepareLogPolicy() error {
	if m == nil {
		return errManagedLogRuntime
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.authority != nil && m.member == 0 {
		return errManagedLogRuntime
	}
	return m.ownerReady()
}

func (m *managedLogRuntime) ownerReady() error {
	if m == nil || m.owner == nil || m.owner.logger == nil {
		return errManagedLogRuntime
	}
	if _, ok := managedLogLevel(m.owner.level.Level()); !ok {
		return errManagedLogRuntime
	}
	return nil
}

func managedLogLevel(level slog.Level) (security.LogLevel, bool) {
	switch level {
	case slog.LevelDebug:
		return security.LogLevelDebug, true
	case slog.LevelInfo:
		return security.LogLevelInfo, true
	case slog.LevelWarn:
		return security.LogLevelWarn, true
	case slog.LevelError:
		return security.LogLevelError, true
	default:
		return "", false
	}
}

func slogLogLevel(level security.LogLevel) (slog.Level, bool) {
	switch level {
	case security.LogLevelDebug:
		return slog.LevelDebug, true
	case security.LogLevelInfo:
		return slog.LevelInfo, true
	case security.LogLevelWarn:
		return slog.LevelWarn, true
	case security.LogLevelError:
		return slog.LevelError, true
	default:
		return 0, false
	}
}

func sameLogScope(a, b security.SemanticCut) bool {
	return a.Version == b.Version && a.Domain == b.Domain && a.Cohort == b.Cohort && a.Generation == b.Generation
}

// validateProjectionLocked rejects regressions instead of reinterpreting an
// adopted policy as environment-owned. Native validation remains authoritative;
// these checks protect the exact local owner/cut binding at the adapter seam.
func (m *managedLogRuntime) validateProjectionLocked(p security.LogPolicyProjection) error {
	if p.Member == 0 || p.Cut.Sequence == 0 || p.Cut.Version != security.S1Version || p.Cut.Domain == [32]byte{} || p.Cut.Cohort == [32]byte{} || p.Cut.Generation == [16]byte{} {
		return errManagedLogRuntime
	}
	if p.VersionFloor != (p.Preparation != nil) {
		return errManagedLogRuntime
	}
	if m.member != 0 {
		if p.Member != m.member || !sameLogScope(p.Cut, m.scope) {
			return errManagedLogRuntime
		}
		if p.Cut.Sequence < m.desired.Cut.Sequence || p.Cut.Sequence == m.desired.Cut.Sequence && p.Cut != m.desired.Cut {
			return errManagedLogRuntime
		}
		if m.desired.VersionFloor && !p.VersionFloor {
			return errManagedLogRuntime
		}
	}
	if p.Policy == nil {
		if m.applied.Generation != 0 || m.desired.Policy != nil {
			return errManagedLogRuntime
		}
		return nil
	}
	if !p.VersionFloor || security.ValidateLogPolicyConfiguration(p.Policy) != nil || p.Policy.Generation < m.applied.Generation {
		return errManagedLogRuntime
	}
	if m.desired.Policy != nil && p.Policy.Generation < m.desired.Policy.Generation {
		return errManagedLogRuntime
	}
	if m.applied.Generation == p.Policy.Generation && m.applied != *p.Policy {
		return errManagedLogRuntime
	}
	if m.desired.Policy != nil && m.desired.Policy.Generation == p.Policy.Generation && *m.desired.Policy != *p.Policy {
		return errManagedLogRuntime
	}
	return nil
}

func detachedLogProjection(p security.LogPolicyProjection) security.LogPolicyProjection {
	if p.Policy != nil {
		policy := *p.Policy
		p.Policy = &policy
	}
	if p.Preparation != nil {
		preparation := *p.Preparation
		p.Preparation = &preparation
	}
	return p
}

func (m *managedLogRuntime) observeProjection(p security.LogPolicyProjection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateProjectionLocked(p); err != nil {
		m.lastError = securitytransport.LogSettingsErrorInvalidPolicy
		return err
	}
	m.member, m.scope = p.Member, p.Cut
	m.desired = detachedLogProjection(p)
	m.lastError = securitytransport.LogSettingsErrorNone
	return nil
}

func (m *managedLogRuntime) reconcile(ctx context.Context) error {
	if m == nil || m.authority == nil {
		return errManagedLogRuntime
	}
	m.refresh.Lock()
	defer m.refresh.Unlock()
	p, err := m.authority.CurrentLogPolicy(ctx)
	if err != nil {
		// Loss of authoritative visibility fails readiness. It does not change
		// the local installation record or invent an application failure.
		return errManagedLogRuntime
	}
	if err = m.observeProjection(p); err != nil {
		return err
	}
	installed, err := m.authority.ReconcileLogPolicy(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if m.lastError == securitytransport.LogSettingsErrorNone && !errors.Is(err, security.ErrAuthorityUnavailable) {
			m.lastError = securitytransport.LogSettingsErrorOwnerUnavailable
		}
		return errManagedLogRuntime
	}
	if installed.Cut != p.Cut || installed.Member != p.Member || installed.VersionFloor != p.VersionFloor || (installed.Policy == nil) != (p.Policy == nil) || installed.Policy != nil && *installed.Policy != *p.Policy {
		m.lastError = securitytransport.LogSettingsErrorOwnerUnavailable
		return errManagedLogRuntime
	}
	if p.Policy != nil && m.applied != *p.Policy {
		m.lastError = securitytransport.LogSettingsErrorApplyFailed
		return errManagedLogRuntime
	}
	return nil
}

// ApplyLogPolicy is called by the bound native owner outside its kernel gate.
// Only the exact already-observed committed policy can be installed. A raced
// newer projection is retried by the bounded readiness worker, never guessed.
func (m *managedLogRuntime) ApplyLogPolicy(policy security.LogPolicy) error {
	if m == nil {
		return errManagedLogRuntime
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if security.ValidateLogPolicyConfiguration(&policy) != nil {
		m.lastError = securitytransport.LogSettingsErrorInvalidPolicy
		return errManagedLogRuntime
	}
	if m.desired.Policy == nil || policy.Generation > m.desired.Policy.Generation {
		// A new commit may win between observing desired state and native
		// reconciliation. Wait for its verified projection, rather than
		// mislabeling a valid newer policy as malformed or guessing its cut.
		m.lastError = securitytransport.LogSettingsErrorOwnerUnavailable
		return errManagedLogRuntime
	}
	if *m.desired.Policy != policy || policy.Generation < m.applied.Generation {
		m.lastError = securitytransport.LogSettingsErrorInvalidPolicy
		return errManagedLogRuntime
	}
	level, known := slogLogLevel(policy.Level)
	if !known || m.ownerReady() != nil {
		m.lastError = securitytransport.LogSettingsErrorApplyFailed
		return errManagedLogRuntime
	}
	if m.applied == policy && m.owner.level.Level() == level {
		// AppliedCut records the actual installation. An unrelated security
		// commit does not reinstall this policy or rewrite that provenance.
		m.lastError = securitytransport.LogSettingsErrorNone
		return nil
	}
	m.owner.applyLevel(level)
	m.applied, m.appliedCut = policy, m.desired.Cut
	m.source = securitytransport.LogSettingsSourceManaged
	m.lastError = securitytransport.LogSettingsErrorNone
	return nil
}

// observation combines a fresh admitted desired projection with the retained
// local installation record. It does not apply policy or claim peer progress.
func (m *managedLogRuntime) observation(p security.LogPolicyProjection) (securitytransport.LogSettingsObservation, error) {
	if m == nil {
		return securitytransport.LogSettingsObservation{}, errManagedLogRuntime
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.member == 0 || m.owner == nil || m.validateProjectionLocked(p) != nil {
		return securitytransport.LogSettingsObservation{}, errManagedLogRuntime
	}
	level, known := managedLogLevel(m.owner.level.Level())
	if !known {
		return securitytransport.LogSettingsObservation{}, errManagedLogRuntime
	}
	observation := securitytransport.LogSettingsObservation{
		EffectiveLevel: level, Source: m.source,
		AppliedGeneration: m.applied.Generation, AppliedCut: m.appliedCut,
		Error: m.lastError,
	}
	if p.Policy != nil {
		observation.Pending = m.applied != *p.Policy || level != p.Policy.Level || m.source != securitytransport.LogSettingsSourceManaged || m.ownerReady() != nil
		if !observation.Pending {
			// This read has fresh current admission. A previous loss of source
			// visibility does not undo a successful local installation.
			observation.Error = securitytransport.LogSettingsErrorNone
		} else if m.applied.Generation != 0 && level != m.applied.Level {
			observation.Error = securitytransport.LogSettingsErrorApplyFailed
		} else if observation.Error == securitytransport.LogSettingsErrorNone && m.ownerReady() != nil {
			observation.Error = securitytransport.LogSettingsErrorOwnerUnavailable
		}
	} else {
		// Fresh admitted absence verifies that startup ownership still applies.
		// A previous authority outage did not change its installed value.
		observation.Error = securitytransport.LogSettingsErrorNone
	}
	return observation, nil
}

func (r *SecurityRuntime) logSettingsObservation(p security.LogPolicyProjection) (securitytransport.LogSettingsObservation, error) {
	if r == nil || r.mode != "oidc" || r.managedLog == nil {
		return securitytransport.LogSettingsObservation{}, errManagedLogRuntime
	}
	return r.managedLog.observation(p)
}
