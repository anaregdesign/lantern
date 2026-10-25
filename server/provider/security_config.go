package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// SecurityConfig is a focused operator-owned startup slice. It never supplies
// an anonymous fallback for an invalid or unavailable OIDC runtime.
type SecurityConfig struct {
	Mode, BrowserOrigin      string
	AuthorityJSON            json.RawMessage
	CDC                      json.RawMessage
	Issuers                  []security.Issuer
	HistoricalImage          *security.Image
	PrivateOrigins           map[string][]netip.Prefix
	SecretBindings           map[string]oidc.SecretBinding
	RootCAPEM                string
	TrustedProxyIPs          []netip.Addr
	TLS                      SecurityTLSConfig
	ProvisionedPeer          *PeerPlaneConfig
	ProvisionedSnapshotSpool *GraphSnapshotSpoolConfig
	MaterialRoot             string
	MaterialFile             string
	PersistenceRoot          string
	StateDirectory           string
	CustodyDirectory         string
	TargetPlatform           string
	Node                     string
	input                    *securityConfigJSON
	configuredTrust          *security.ConfiguredTrust
	authorityManifest        []byte
}

func LoadSecurityConfig() (SecurityConfig, error)  { return loadSecurityConfig() }
func NewSecurityConfig(cfg *Config) SecurityConfig { return cfg.Security }

func securityConfigurationEnvironment() (string, string, error) {
	if err := validateAuthMode(); err != nil {
		return "", "", err
	}
	envconfig.RegisterString("LANTERN_SECURITY_CONFIG_JSON", "")
	mode := envconfig.String("LANTERN_AUTH_MODE", "off")
	raw, present := os.LookupEnv("LANTERN_SECURITY_CONFIG_JSON")
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if (strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_")) && name != "LANTERN_SECURITY_CONFIG_JSON" {
			return "", "", errors.New("unknown or retired OIDC/security environment setting")
		}
	}
	if mode == "off" {
		if present {
			return "", "", errors.New("OFF mode cannot ignore OIDC/security configuration")
		}
		return mode, "", nil
	}
	if !present {
		return "", "", errors.New("OIDC requires LANTERN_SECURITY_CONFIG_JSON on every invocation")
	}
	return mode, raw, nil
}

func loadSecurityConfig() (SecurityConfig, error) {
	mode, raw, err := securityConfigurationEnvironment()
	if err != nil {
		return SecurityConfig{}, err
	}
	if mode == "off" {
		return SecurityConfig{Mode: "off"}, nil
	}
	return loadSecurityConfigJSON(raw)
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
	if config.Mode != "oidc" || config.input == nil || config.configuredTrust == nil || !canonicalSecurityOrigin(config.BrowserOrigin) {
		return errors.New("OIDC requires current configuration and an exact HTTPS public origin")
	}
	if err := validateSecurityInput(config); err != nil {
		return err
	}
	for _, name := range []string{"LANTERN_TLS_CERT_FILE", "LANTERN_TLS_KEY_FILE", "LANTERN_TLS_CLIENT_CA_FILE"} {
		if _, present := os.LookupEnv(name); present {
			return errors.New("security JSON cannot be combined with TLS file settings")
		}
	}
	if config.RootCAPEM != "" {
		if _, err := loadSecurityRootsPEM(config.RootCAPEM); err != nil {
			return errors.New("invalid OIDC root CA PEM")
		}
	}
	if (config.TLS.CertPEM == "") != (config.TLS.KeyPEM == "") || config.TLS.ClientCAPEM != "" && config.TLS.CertPEM == "" {
		return errors.New("inline public TLS requires a certificate and matching private key")
	}
	if _, err := loadTLSConfig(TLSConfig{CertPEM: config.TLS.CertPEM, KeyPEM: config.TLS.KeyPEM, ClientCAPEM: config.TLS.ClientCAPEM}); err != nil {
		return errors.New("invalid inline public TLS configuration")
	}
	if _, err := oidc.NewSecretRegistry(config.SecretBindings); err != nil {
		return errors.New("invalid OIDC secret bindings")
	}
	if _, err := oidc.NewFetcher(oidc.FetcherOptions{PrivateOrigins: config.PrivateOrigins}); err != nil {
		return errors.New("invalid OIDC private origins")
	}
	if config.ProvisionedSnapshotSpool != nil {
		if _, err := loadGraphSnapshotSpoolForSecurity(config); err != nil {
			return err
		}
	}
	if len(config.CDC) > 0 {
		if _, err := loadJSONChangeConfig(config.CDC); err != nil {
			return errors.New("invalid internal CDC key material")
		}
	}
	return nil
}
func canonicalSecurityOrigin(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Opaque == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == "" && raw == parsed.Scheme+"://"+parsed.Host
}
