package provider

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// NewWorkloadPeerIdentity composes the private identity with its owned OFF or
// borrowed OIDC time lease. Disabled standalone OFF creates neither.
func NewWorkloadPeerIdentity(ctx context.Context, cfg PeerPlaneConfig, runtime *SecurityRuntime) (*PeerIdentityRuntime, func(), error) {
	if runtime == nil {
		return nil, nil, errors.New("missing security runtime")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := validatePeerPlaneConfig(cfg); err != nil {
		return nil, nil, err
	}
	if cfg.ListenAddress == "" {
		return NewConfiguredPeerIdentity(cfg)
	}
	if cfg.Identity.AuthMode != runtime.mode {
		return nil, nil, errors.New("data workload belongs to a different current cohort")
	}
	var lease *security.WorkloadTime
	if runtime.mode == "oidc" {
		if runtime.current == nil {
			return nil, nil, errors.New("OIDC requires its owned quorum authority")
		}
		p := runtime.current.Profile()
		if cfg.Identity.AuthMode != "oidc" || cfg.Identity.CurrentProfile != p.Binding() || cfg.Identity.SecurityGeneration != p.Generation || cfg.Identity.WriterPublicKey != [32]byte{} {
			return nil, nil, errors.New("data workload belongs to a different current cohort")
		}
		lease = runtime.current.WorkloadTime()
	} else if security.NativeWorkloadTimeTarget() {
		var err error
		lease, err = security.OpenWorkloadTime(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	if lease != nil {
		cfg.Identity.Now, cfg.Identity.TimeBounds, cfg.Identity.Time = lease.VerificationTime, lease.TimeBounds, lease.Observe
	}
	peer, _, err := NewConfiguredPeerIdentity(cfg)
	if err != nil {
		return nil, nil, errors.Join(err, lease.Close())
	}
	peer.timeLease = lease
	if err = ctx.Err(); err != nil {
		return nil, nil, errors.Join(err, peer.Close())
	}
	return peer, func() { _ = peer.Close() }, nil
}
