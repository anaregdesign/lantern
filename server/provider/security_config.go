package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// SecurityConfig is a focused operator-owned startup slice. It never supplies
// an anonymous fallback for an invalid or unavailable OIDC runtime.
type SecurityConfig struct {
	Mode, ConfigFile, StoreMode, BrowserOrigin      string
	AuthorityJSON                                   json.RawMessage
	CDC                                             json.RawMessage
	InitialIssuers                                  []security.Issuer
	InitialImage                                    *security.Image
	PrivateOrigins                                  map[string][]netip.Prefix
	SecretBindings                                  map[string]oidc.SecretBinding
	RootCAFile                                      string
	RootCAPEM                                       string
	TrustedProxyIPs                                 []netip.Addr
	TLS                                             SecurityTLSConfig
	ProvisionedPeer                                 *PeerPlaneConfig
	ProvisionedSnapshotSpool                        *GraphSnapshotSpoolConfig
	ProvisionedFile, StateDirectory, TargetPlatform string
	setup                                           *securitySetupJSON
}

func LoadSecurityConfig() (SecurityConfig, error)  { return loadSecurityConfig() }
func NewSecurityConfig(cfg *Config) SecurityConfig { return cfg.Security }

var securityEnvironmentNames = []string{
	"LANTERN_SECURITY_CONFIG_JSON",
	"LANTERN_SECURITY_CONFIG_FILE", "LANTERN_SECURITY_STORE_MODE",
	"LANTERN_OIDC_BROWSER_ORIGIN", "LANTERN_OIDC_SECRET_BINDINGS", "LANTERN_OIDC_PRIVATE_ORIGINS",
	"LANTERN_OIDC_ROOT_CA_FILE", "LANTERN_OIDC_TRUSTED_PROXY_IPS",
}

// The environment selects OFF or one independently provisioned OIDC authority.
// Retired and misspelled inputs are rejected by presence, including empty values.
func loadSecurityConfig() (SecurityConfig, error) {
	if err := validateAuthMode(); err != nil {
		return SecurityConfig{}, err
	}
	known := make(map[string]bool, len(securityEnvironmentNames))
	for _, name := range securityEnvironmentNames {
		known[name] = true
		def := ""
		switch name {
		case "LANTERN_OIDC_SECRET_BINDINGS", "LANTERN_OIDC_PRIVATE_ORIGINS":
			def = "{}"
		case "LANTERN_OIDC_TRUSTED_PROXY_IPS":
			def = "[]"
		}
		envconfig.RegisterString(name, def)
	}
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if (strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_")) && !known[name] {
			return SecurityConfig{}, errors.New("unknown or retired OIDC/security environment setting")
		}
	}
	config := SecurityConfig{Mode: envconfig.String("LANTERN_AUTH_MODE", "off")}
	if config.Mode == "off" {
		return config, nil
	}
	if _, inline := os.LookupEnv("LANTERN_SECURITY_CONFIG_JSON"); inline {
		for _, name := range securityEnvironmentNames {
			if name != "LANTERN_SECURITY_CONFIG_JSON" {
				if _, exists := os.LookupEnv(name); exists {
					return SecurityConfig{}, errors.New("inline security configuration cannot be combined with file-based OIDC/security settings")
				}
			}
		}
		return loadSecurityConfigJSON(envconfig.String("LANTERN_SECURITY_CONFIG_JSON", ""))
	}
	config.ConfigFile = envconfig.String("LANTERN_SECURITY_CONFIG_FILE", "")
	config.StoreMode = envconfig.String("LANTERN_SECURITY_STORE_MODE", "")
	config.BrowserOrigin = envconfig.String("LANTERN_OIDC_BROWSER_ORIGIN", "")
	config.RootCAFile = envconfig.String("LANTERN_OIDC_ROOT_CA_FILE", "")
	if err := strictSecurityConfigJSON(envconfig.String("LANTERN_OIDC_SECRET_BINDINGS", "{}"), &config.SecretBindings); err != nil {
		return SecurityConfig{}, err
	}
	var origins map[string][]string
	if err := strictSecurityConfigJSON(envconfig.String("LANTERN_OIDC_PRIVATE_ORIGINS", "{}"), &origins); err != nil {
		return SecurityConfig{}, err
	}
	if len(origins) > 64 {
		return SecurityConfig{}, errors.New("too many OIDC private origins")
	}
	config.PrivateOrigins = make(map[string][]netip.Prefix, len(origins))
	for origin, ranges := range origins {
		if len(ranges) == 0 || len(ranges) > 32 {
			return SecurityConfig{}, errors.New("invalid OIDC private origin ranges")
		}
		for _, raw := range ranges {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || prefix != prefix.Masked() || prefix.Bits() == 0 {
				return SecurityConfig{}, errors.New("invalid OIDC private origin CIDR")
			}
			config.PrivateOrigins[origin] = append(config.PrivateOrigins[origin], prefix)
		}
	}
	var proxies []string
	if err := strictSecurityConfigJSON(envconfig.String("LANTERN_OIDC_TRUSTED_PROXY_IPS", "[]"), &proxies); err != nil {
		return SecurityConfig{}, err
	}
	if len(proxies) > 16 {
		return SecurityConfig{}, errors.New("too many trusted browser proxies")
	}
	for _, raw := range proxies {
		ip, err := netip.ParseAddr(raw)
		if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return SecurityConfig{}, errors.New("trusted proxy must be an exact IP address")
		}
		config.TrustedProxyIPs = append(config.TrustedProxyIPs, ip.Unmap())
	}
	if err := validateSecurityConfig(config); err != nil {
		return SecurityConfig{}, err
	}
	return config, nil
}
func strictSecurityConfigJSON(raw string, value any) error {
	return decodeSecurityConfigJSON(raw, value, oidc.ValidateJSON)
}

func decodeSecurityConfigJSON(raw string, value any, validate func([]byte) error) error {
	if len(raw) == 0 || len(raw) > security.MaxImageBytes {
		return errors.New("missing or oversized security JSON setting")
	}
	// OIDC's structural scanner also rejects duplicates at every nesting level.
	if err := validate([]byte(raw)); err != nil {
		return errors.New("invalid security JSON setting")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid security JSON setting")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid security JSON setting")
	}
	return nil
}
func validateSecurityConfig(config SecurityConfig) error {
	if config.Mode == "off" {
		config.Mode = ""
		if !reflect.DeepEqual(config, SecurityConfig{}) {
			return errors.New("OFF mode cannot ignore OIDC/security configuration")
		}
		return nil
	}
	if config.ProvisionedFile != "" {
		if _, err := loadGraphSnapshotSpoolForSecurity(config); err != nil {
			return err
		}
	}
	inline := len(config.AuthorityJSON) > 0 || config.setup != nil
	if inline {
		for _, name := range []string{"LANTERN_TLS_CERT_FILE", "LANTERN_TLS_KEY_FILE", "LANTERN_TLS_CLIENT_CA_FILE"} {
			if _, present := os.LookupEnv(name); present {
				return errors.New("security JSON cannot be combined with TLS file settings")
			}
		}
	}
	validAuthority := inline && config.ConfigFile == "" || !inline && filepath.IsAbs(config.ConfigFile)
	if config.Mode != "oidc" || !validAuthority || config.StoreMode != "fresh" && config.StoreMode != "resume" || !canonicalSecurityOrigin(config.BrowserOrigin) {
		return errors.New("OIDC requires independent authority provisioning, fresh/resume mode and exact HTTPS browser origin")
	}
	if inline && len(config.InitialIssuers) == 0 {
		return errors.New("inline security configuration requires registered Issuer trust")
	}
	if config.RootCAFile != "" && !filepath.IsAbs(config.RootCAFile) {
		return errors.New("OIDC root CA must be an absolute operator file")
	}
	if config.RootCAFile != "" && config.RootCAPEM != "" {
		return errors.New("OIDC root CA cannot have multiple sources")
	}
	if config.RootCAPEM != "" {
		if _, err := loadSecurityRootsPEM(config.RootCAPEM); err != nil {
			return errors.New("invalid OIDC root CA PEM")
		}
	}
	if (config.TLS.CertPEM == "") != (config.TLS.KeyPEM == "") || config.TLS.ClientCAPEM != "" && config.TLS.CertPEM == "" {
		return errors.New("inline public TLS requires a certificate and matching private key")
	}
	if inline {
		if _, err := loadTLSConfig(TLSConfig{CertPEM: config.TLS.CertPEM, KeyPEM: config.TLS.KeyPEM, ClientCAPEM: config.TLS.ClientCAPEM}); err != nil {
			return errors.New("invalid inline public TLS configuration")
		}
		if _, err := loadJSONChangeConfig(config.CDC); err != nil {
			return errors.New("invalid inline CDC configuration")
		}
	}
	if _, err := oidc.NewSecretRegistry(config.SecretBindings); err != nil {
		return errors.New("invalid OIDC secret bindings")
	}
	if _, err := oidc.NewFetcher(oidc.FetcherOptions{PrivateOrigins: config.PrivateOrigins}); err != nil {
		return errors.New("invalid OIDC private origins")
	}
	return nil
}
func canonicalSecurityOrigin(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Opaque == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == "" && raw == parsed.Scheme+"://"+parsed.Host
}
