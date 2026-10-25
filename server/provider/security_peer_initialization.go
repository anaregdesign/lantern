package provider

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// initializeSecurityPeer creates only the generated data membership custody.
// The caller retains ownership of the control authority and publishes CLEAN
// only after both independent state families have completed initialization.
func initializeSecurityPeer(ctx context.Context, config SecurityConfig, current *security.CurrentAuthority) error {
	if config.ProvisionedPeer == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.Mode != "oidc" || current == nil {
		return errors.New("peer initialization requires the owned OIDC authority")
	}
	cfg := *config.ProvisionedPeer
	cfg.Identity.AuthMode, cfg.Identity.StateMode = "oidc", "fresh"
	profile := current.Profile()
	if profile.Version != security.CurrentPublicVersion || cfg.Identity.WriterPublicKey != [32]byte{} ||
		cfg.Identity.Now != nil || cfg.Identity.TimeBounds != nil || cfg.Identity.Time != nil || cfg.ListenAddress == "" {
		return errors.New("peer initialization requires matching provisioned trust")
	}
	if cfg.Identity.CurrentProfile != "" && cfg.Identity.CurrentProfile != profile.Binding() ||
		cfg.Identity.SecurityGeneration != [16]byte{} && cfg.Identity.SecurityGeneration != profile.Generation {
		return errors.New("peer initialization belongs to a different current cohort")
	}
	cfg.Identity.CurrentProfile, cfg.Identity.SecurityGeneration = profile.Binding(), profile.Generation
	if err := validatePeerPlaneConfig(cfg); err != nil {
		return err
	}
	lease := current.WorkloadTime()
	defer lease.Close()
	cfg.Identity.Now, cfg.Identity.TimeBounds, cfg.Identity.Time = lease.VerificationTime, lease.TimeBounds, lease.Observe
	peer, _, err := NewConfiguredPeerIdentity(cfg)
	if err != nil {
		return err
	}
	return errors.Join(ctx.Err(), peer.Close())
}
