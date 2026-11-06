package provider

import (
	"errors"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// validateAuthMode rejects ignored trust settings before configuration is
// published. Rejection depends on presence, including empty values and case
// variants; retired settings are never parsed or registered as live defaults.
// OIDC activation additionally requires the certified Wire graph.
func validateAuthMode(reader *envconfig.Reader) error {
	mode := reader.String("LANTERN_AUTH_MODE", "off")
	if mode != "off" && mode != "oidc" {
		return errors.New("LANTERN_AUTH_MODE must be off or oidc; an explicitly empty value is invalid")
	}
	for _, setting := range reader.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "LANTERN_AUTH_") && name != "LANTERN_AUTH_MODE" {
			return errors.New("unknown or retired authentication setting; remove it and configure LANTERN_AUTH_MODE=off or oidc")
		}
		if mode == "off" && (strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_")) {
			return errors.New("OIDC/security configuration requires explicit LANTERN_AUTH_MODE=oidc")
		}
	}
	return nil
}
