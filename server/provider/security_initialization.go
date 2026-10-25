package provider

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// InitializeSecurity is the explicit one-time initialization operation for
// environment JSON configuration. It never opens the public data listener or
// guesses that missing state is a new deployment. Native custody owns refusal
// of existing, interrupted, copied or inconsistent state.
func InitializeSecurity(ctx context.Context, config SecurityConfig) (err error) {
	if config.Mode != "oidc" || len(config.AuthorityJSON) == 0 {
		return errors.New("security initialization requires OIDC environment JSON")
	}
	if err := validateSecurityConfig(config); err != nil {
		return err
	}
	if err := initializeSecurityStorage(config); err != nil {
		return err
	}
	p, err := loadSecurityProvisioning(config)
	if err != nil {
		return err
	}
	if err := p.ValidateInitialIssuers(config.InitialIssuers); err != nil {
		return err
	}
	owner, err := security.OpenCurrentAuthority(ctx, p, "fresh", config.BrowserOrigin)
	if err != nil {
		return err
	}
	orderly := false
	defer func() {
		if !orderly {
			err = errors.Join(err, owner.Close())
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := initializeSecurityPeer(ctx, config, owner); err != nil {
		return err
	}
	// Native construction durably creates all original families. Initialization
	// does not grant serving authority: waiting for quorum here would prevent
	// sequential initialization of a fixed-membership deployment. Shutdown
	// validates the owned local floors and publishes CLEAN only on success.
	err = owner.Shutdown()
	orderly = err == nil
	return err
}

func initializeSecurityStorage(config SecurityConfig) error {
	if config.ProvisionedFile == "" {
		return nil
	}
	if !securityTargetMatches(config.TargetPlatform) {
		return errors.New("security provisioning target does not match this native runtime")
	}
	if config.ProvisionedSnapshotSpool == nil || validateProvisionedSnapshotSpool(*config.ProvisionedSnapshotSpool, config.StateDirectory) != nil {
		return errSecurityProvisioning
	}
	if err := prepareSecurityStateDirectory(config.StateDirectory); err != nil {
		return err
	}
	return createPrivateDirectory(config.ProvisionedSnapshotSpool.Directory)
}
