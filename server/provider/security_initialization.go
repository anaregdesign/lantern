package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// InitializeSecurity is the explicit one-time initialization operation for
// environment JSON configuration. It never opens the public data listener or
// guesses that missing state is a new deployment. Native custody owns refusal
// of existing, interrupted, copied or inconsistent state. Returned acquisition
// counts are passive local diagnostics captured after owner shutdown.
func InitializeSecurity(ctx context.Context, config SecurityConfig) (counts decisiontrace.AcquisitionCounts, err error) {
	if config.Mode != "oidc" || config.input == nil || len(config.AuthorityJSON) == 0 {
		return counts, errors.New("security initialization requires OIDC environment JSON")
	}
	if err := validateSecurityConfig(config); err != nil {
		return counts, fmt.Errorf("security initialization configuration: %w", err)
	}
	node, found := selectedSecurityNode(config)
	if !found || security.ValidateProvisioningLocalListen(config.AuthorityJSON, node.ControlListen) != nil {
		return counts, fmt.Errorf("security initialization local control binding: %w", errSecurityProvisioning)
	}
	if security.ValidateProvisioningInitialConfiguredTrust(config.AuthorityJSON, config.authorityManifest, config.TargetPlatform, config.configuredTrust) != nil {
		return counts, fmt.Errorf("security initialization initial configured trust: %w", errSecurityProvisioning)
	}
	if err := initializeSecurityStorage(config); err != nil {
		return counts, fmt.Errorf("security initialization storage preparation: %w", err)
	}
	p, err := loadSecurityProvisioning(config)
	if err != nil {
		return counts, fmt.Errorf("security initialization original authority: %w", err)
	}
	owner, err := security.OpenCurrentAuthority(ctx, p, "fresh", config.BrowserOrigin)
	if err != nil {
		var diagnostic interface {
			TimeAcquisitionCounts() decisiontrace.AcquisitionCounts
		}
		if errors.As(err, &diagnostic) {
			counts = diagnostic.TimeAcquisitionCounts()
		}
		return counts, err
	}
	orderly := false
	defer func() {
		if !orderly {
			err = errors.Join(err, owner.Close())
		}
		counts = owner.TimeAcquisitionCounts()
	}()
	if err := ctx.Err(); err != nil {
		return counts, err
	}
	if err := initializeSecurityPeer(ctx, config, owner); err != nil {
		return counts, err
	}
	// Native construction durably creates all original families. Initialization
	// does not grant serving authority: waiting for quorum here would prevent
	// sequential initialization of a fixed-membership deployment. Shutdown
	// validates the owned local floors and publishes CLEAN only on success.
	err = owner.Shutdown()
	orderly = err == nil
	return counts, err
}

func initializeSecurityStorage(config SecurityConfig) error {
	if config.MaterialFile == "" {
		return errSecurityProvisioning
	}
	if !securityTargetMatches(config.TargetPlatform) {
		return errors.New("security provisioning target does not match this native runtime")
	}
	if config.ProvisionedSnapshotSpool == nil || validateProvisionedSnapshotSpool(*config.ProvisionedSnapshotSpool, config.StateDirectory) != nil {
		return errSecurityProvisioning
	}
	spool, err := selectGraphSnapshotSpoolForSecurity(config, loadReceiptWALConfig().Mode)
	if err != nil {
		return err
	}
	if err := prepareSecurityStateDirectory(config.StateDirectory); err != nil {
		return err
	}
	if config.CustodyDirectory != config.StateDirectory {
		if err := prepareSecurityStateDirectory(config.CustodyDirectory); err != nil {
			return err
		}
	}
	if spool.Directory != "" {
		return createPrivateDirectory(spool.Directory)
	}
	return nil
}
