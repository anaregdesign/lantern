package provider

import (
	"errors"
	"os"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

var ErrOIDCRuntimeUnavailable = errors.New("oidc/rbac runtime is not yet available")

// validateAuthMode prevents staged OIDC settings from being ignored by the
// legacy environment loader and accidentally starting an anonymous server.
// This guard is replaced by certified runtime construction when the dependent
// namespace, durability and authorization deliveries are installed.
func validateAuthMode() error {
	mode := envconfig.String("LANTERN_AUTH_MODE", "off")
	_, explicit := os.LookupEnv("LANTERN_AUTH_MODE")
	newSettings := false
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_") ||
			strings.HasPrefix(upper, "LANTERN_AUTH_") && name != "LANTERN_AUTH_MODE" &&
				name != "LANTERN_AUTH_TOKENS" && name != "LANTERN_AUTH_EXEMPT_REFLECTION" {
			newSettings = true
		}
	}
	if !explicit && newSettings {
		return errors.New("oidc/rbac configuration requires explicit LANTERN_AUTH_MODE=oidc")
	}
	switch mode {
	case "off":
		if newSettings || explicit && len(splitTokens(os.Getenv("LANTERN_AUTH_TOKENS"))) != 0 {
			return errors.New("LANTERN_AUTH_MODE=off conflicts with configured authentication settings")
		}
		return nil
	case "oidc":
		// Partial foundations must not enable a listener until the full
		// authenticated serving boundary can be constructed.
		return ErrOIDCRuntimeUnavailable
	default:
		return errors.New("LANTERN_AUTH_MODE must be off or oidc; an explicitly empty value is invalid")
	}
}
