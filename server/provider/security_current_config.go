package provider

import (
	"errors"
	"path/filepath"
	"reflect"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// The current cohort accepts only independent versioned provisioning. Neither
// a legacy writer volume/bootstrap nor a qualification Boolean is a source.
func validateCurrentSecurityConfig(c SecurityConfig) error {
	if c.Mode != "oidc" || c.Profile != "current-v2" || !filepath.IsAbs(c.CurrentConfigFile) || c.StoreMode != "fresh" && c.StoreMode != "resume" || !canonicalSecurityOrigin(c.BrowserOrigin) {
		return errors.New("current-v2 requires independent provisioning, fresh/resume mode and exact HTTPS browser origin")
	}
	if c.StorePath != "" || c.Generation != [16]byte{} || c.WriterKeyFile != "" || c.WriterPublicKeyFile != "" || c.NodeRole != "" || c.WriterEndpoint != "" || c.MachineBootstrapFile != "" || !reflect.DeepEqual(c.Bootstrap, security.Bootstrap{}) || c.MaxJournalBytes != 0 || c.ClockQualified || c.Clock != nil {
		return errors.New("current-v2 refuses legacy state, writer identity, bootstrap and injected time")
	}
	if c.RootCAFile != "" && !filepath.IsAbs(c.RootCAFile) {
		return errors.New("OIDC root CA must be an absolute operator file")
	}
	if _, err := oidc.NewSecretRegistry(c.SecretBindings); err != nil {
		return errors.New("invalid OIDC secret bindings")
	}
	if _, err := oidc.NewFetcher(oidc.FetcherOptions{PrivateOrigins: c.PrivateOrigins}); err != nil {
		return errors.New("invalid OIDC private origins")
	}
	return nil
}
