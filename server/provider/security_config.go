package provider

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// SecurityConfig is a focused operator-owned startup slice. It never supplies
// an anonymous fallback for an invalid or unavailable OIDC runtime.
type SecurityConfig struct {
	Mode                                    string
	Profile, CurrentConfigFile              string
	StoreMode, StorePath                    string
	Generation                              [16]byte
	WriterKeyFile, WriterPublicKeyFile      string
	MachineBootstrapFile                    string
	NodeRole, WriterEndpoint, BrowserOrigin string
	Bootstrap                               security.Bootstrap
	PrivateOrigins                          map[string][]netip.Prefix
	SecretBindings                          map[string]oidc.SecretBinding
	RootCAFile                              string
	TrustedProxyIPs                         []netip.Addr
	MaxJournalBytes                         int64
	ClockQualified                          bool
	// Clock is a trusted composition seam for deterministic protocol gates.
	// The environment loader never sets it.
	Clock func() time.Time
}

func LoadSecurityConfig() (SecurityConfig, error)  { return loadSecurityConfig() }
func NewSecurityConfig(cfg *Config) SecurityConfig { return cfg.Security }

var securityEnvironmentNames = []string{
	"LANTERN_SECURITY_PROFILE", "LANTERN_SECURITY_CURRENT_CONFIG_FILE",
	"LANTERN_OIDC_ADMIN_ISSUER", "LANTERN_OIDC_ADMIN_SUBJECTS", "LANTERN_OIDC_CLIENT_ID", "LANTERN_OIDC_API_AUDIENCE",
	"LANTERN_OIDC_BROWSER_ORIGIN", "LANTERN_OIDC_REDIRECT_URI", "LANTERN_OIDC_ALGORITHMS", "LANTERN_OIDC_SECRET_REF",
	"LANTERN_OIDC_SECRET_BINDINGS", "LANTERN_OIDC_PRIVATE_ORIGINS", "LANTERN_OIDC_ROOT_CA_FILE", "LANTERN_OIDC_TRUSTED_PROXY_IPS",
	"LANTERN_SECURITY_STORE_MODE", "LANTERN_SECURITY_STORE_PATH", "LANTERN_SECURITY_GENERATION", "LANTERN_SECURITY_WRITER_KEY_FILE",
	"LANTERN_SECURITY_WRITER_PUBLIC_KEY_FILE", "LANTERN_SECURITY_NODE_ROLE", "LANTERN_SECURITY_WRITER_ENDPOINT",
	"LANTERN_SECURITY_BOOTSTRAP_REVISION", "LANTERN_SECURITY_BOOTSTRAP_ROLES", "LANTERN_SECURITY_MAX_JOURNAL_BYTES", "LANTERN_SECURITY_CLOCK_QUALIFIED",
	"LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE",
	"LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED",
}

// loadSecurityConfig validates operator trust before the production serving
// dependencies certify the exact runtime.
func loadSecurityConfig() (SecurityConfig, error) {
	for _, name := range securityEnvironmentNames {
		def := ""
		switch name {
		case "LANTERN_OIDC_ALGORITHMS":
			def = `["RS256"]`
		case "LANTERN_OIDC_SECRET_BINDINGS", "LANTERN_OIDC_PRIVATE_ORIGINS":
			def = "{}"
		case "LANTERN_OIDC_TRUSTED_PROXY_IPS", "LANTERN_SECURITY_BOOTSTRAP_ROLES":
			def = "[]"
		case "LANTERN_SECURITY_MAX_JOURNAL_BYTES":
			def = strconv.FormatInt(security.DefaultSystemJournalMax, 10)
		}
		envconfig.RegisterString(name, def)
	}
	config := SecurityConfig{Mode: envconfig.String("LANTERN_AUTH_MODE", "off")}
	if config.Mode == "off" {
		for _, setting := range os.Environ() {
			name, value, _ := strings.Cut(setting, "=")
			upper := strings.ToUpper(name)
			if value != "" && (strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_")) {
				return SecurityConfig{}, errors.New("OIDC/security settings require explicit OIDC mode")
			}
		}
		return config, nil
	}
	if config.Mode != "oidc" {
		return SecurityConfig{}, errors.New("invalid authentication mode")
	}
	known := make(map[string]bool, len(securityEnvironmentNames))
	for _, name := range securityEnvironmentNames {
		known[name] = true
	}
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if (strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_")) && !known[name] {
			return SecurityConfig{}, errors.New("unknown or misspelled OIDC/security environment setting")
		}
	}
	required := func(name string) string { return envconfig.String(name, "") }
	config.Profile = required("LANTERN_SECURITY_PROFILE")
	config.CurrentConfigFile = required("LANTERN_SECURITY_CURRENT_CONFIG_FILE")
	if config.Profile != "current-v2" && config.Profile != "legacy-v1" {
		return SecurityConfig{}, errors.New("OIDC requires an explicit current-v2 or legacy-v1 profile")
	}
	config.StoreMode = required("LANTERN_SECURITY_STORE_MODE")
	config.BrowserOrigin = required("LANTERN_OIDC_BROWSER_ORIGIN")
	if config.Profile == "current-v2" {
		allowed := map[string]bool{"LANTERN_SECURITY_PROFILE": true, "LANTERN_SECURITY_CURRENT_CONFIG_FILE": true, "LANTERN_SECURITY_STORE_MODE": true, "LANTERN_OIDC_BROWSER_ORIGIN": true, "LANTERN_OIDC_ROOT_CA_FILE": true, "LANTERN_OIDC_SECRET_BINDINGS": true, "LANTERN_OIDC_PRIVATE_ORIGINS": true, "LANTERN_OIDC_TRUSTED_PROXY_IPS": true}
		for _, name := range securityEnvironmentNames {
			if value, set := os.LookupEnv(name); set && value != "" && !allowed[name] {
				return SecurityConfig{}, errors.New("current-v2 cannot consume legacy writer/bootstrap/clock settings")
			}
		}
	} else {
		config.StorePath = required("LANTERN_SECURITY_STORE_PATH")
		config.NodeRole = required("LANTERN_SECURITY_NODE_ROLE")
		config.WriterKeyFile = required("LANTERN_SECURITY_WRITER_KEY_FILE")
		config.WriterPublicKeyFile = required("LANTERN_SECURITY_WRITER_PUBLIC_KEY_FILE")
		config.WriterEndpoint = required("LANTERN_SECURITY_WRITER_ENDPOINT")
		config.BrowserOrigin = required("LANTERN_OIDC_BROWSER_ORIGIN")
		issuer := security.Issuer{URL: required("LANTERN_OIDC_ADMIN_ISSUER"), Enabled: true, ClientID: required("LANTERN_OIDC_CLIENT_ID"), APIAudience: required("LANTERN_OIDC_API_AUDIENCE"), RedirectURI: required("LANTERN_OIDC_REDIRECT_URI"), SecretRef: envconfig.String("LANTERN_OIDC_SECRET_REF", "")}
		qualification := envconfig.String("LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED", "")
		if qualification != "" && qualification != "true" && qualification != "false" {
			return SecurityConfig{}, errors.New("invalid human subject namespace qualification")
		}
		issuer.HumanSubjectNamespaceQualified = qualification == "true"
		if err := strictSecurityConfigJSON(envconfig.String("LANTERN_OIDC_ALGORITHMS", `["RS256"]`), &issuer.Algorithms); err != nil {
			return SecurityConfig{}, err
		}
		if err := strictSecurityConfigJSON(required("LANTERN_OIDC_ADMIN_SUBJECTS"), &config.Bootstrap.AdminSubjects); err != nil {
			return SecurityConfig{}, err
		}
		revision, err := strconv.ParseUint(required("LANTERN_SECURITY_BOOTSTRAP_REVISION"), 10, 64)
		if err != nil || revision == 0 {
			return SecurityConfig{}, errors.New("security bootstrap revision must be a positive integer")
		}
		config.Bootstrap.Revision = revision
		config.Bootstrap.Issuer = issuer
		if err := strictSecurityConfigJSON(envconfig.String("LANTERN_SECURITY_BOOTSTRAP_ROLES", "[]"), &config.Bootstrap.Roles); err != nil {
			return SecurityConfig{}, err
		}
		config.MachineBootstrapFile = envconfig.String("LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE", "")
		if config.MachineBootstrapFile != "" {
			if config.NodeRole != "writer" {
				return SecurityConfig{}, errors.New("only the security writer may read machine bootstrap credentials")
			}
			config.Bootstrap.Machines, err = loadSecurityMachines(config.MachineBootstrapFile)
			if err != nil {
				return SecurityConfig{}, err
			}
		}
		rawGeneration := required("LANTERN_SECURITY_GENERATION")
		generation, err := hex.DecodeString(rawGeneration)
		if err != nil || len(generation) != 16 || rawGeneration != hex.EncodeToString(generation) {
			return SecurityConfig{}, errors.New("security generation must be 32 lowercase hexadecimal characters")
		}
		copy(config.Generation[:], generation)
	}
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
	if config.Profile == "legacy-v1" {
		capText := envconfig.String("LANTERN_SECURITY_MAX_JOURNAL_BYTES", strconv.FormatInt(security.DefaultSystemJournalMax, 10))
		var err error
		config.MaxJournalBytes, err = strconv.ParseInt(capText, 10, 64)
		if err != nil || config.MaxJournalBytes < 20<<20 || config.MaxJournalBytes > security.MaxSystemJournalBytes {
			return SecurityConfig{}, errors.New("security journal cap must be between 20 and 512 MiB")
		}
		config.ClockQualified = envconfig.String("LANTERN_SECURITY_CLOCK_QUALIFIED", "") == "true"
	}
	if err := validateSecurityConfig(config); err != nil {
		return SecurityConfig{}, err
	}
	return config, nil
}
func strictSecurityConfigJSON(raw string, value any) error {
	if len(raw) == 0 || len(raw) > security.MaxImageBytes {
		return errors.New("missing or oversized security JSON setting")
	}
	// OIDC's structural scanner also rejects duplicates at every nesting level.
	if err := oidc.ValidateJSON([]byte(raw)); err != nil {
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
		config.Mode, config.Clock = "", nil
		if !reflect.DeepEqual(config, SecurityConfig{}) {
			return errors.New("OFF mode cannot ignore OIDC/security configuration")
		}
		return nil
	}
	if config.Profile == "current-v2" {
		return validateCurrentSecurityConfig(config)
	}
	if config.Profile != "legacy-v1" || config.CurrentConfigFile != "" {
		return errors.New("OIDC requires an explicit isolated security profile")
	}
	if config.Mode != "oidc" || (config.StoreMode != "fresh" && config.StoreMode != "restart") || !filepath.IsAbs(config.StorePath) || config.Generation == [16]byte{} || !filepath.IsAbs(config.WriterPublicKeyFile) || !config.ClockQualified {
		return errors.New("OIDC requires explicit durable store, generation, pinned writer key and qualified clock")
	}
	if config.NodeRole != "writer" && config.NodeRole != "replica" {
		return errors.New("security node role must be writer or replica")
	}
	if config.NodeRole == "writer" && !filepath.IsAbs(config.WriterKeyFile) || config.NodeRole == "replica" && config.WriterKeyFile != "" {
		return errors.New("only the pinned security writer may configure a signing key")
	}
	if config.MachineBootstrapFile != "" && (config.NodeRole != "writer" || !filepath.IsAbs(config.MachineBootstrapFile)) {
		return errors.New("machine bootstrap credentials require the writer and an absolute private file")
	}
	if !canonicalSecurityOrigin(config.BrowserOrigin) || config.Bootstrap.Issuer.RedirectURI != config.BrowserOrigin+oidc.CallbackPath(config.Bootstrap.Issuer.URL) {
		return errors.New("OIDC requires one exact HTTPS browser origin and Issuer-specific registered callback")
	}
	endpoint, err := url.Parse(config.WriterEndpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" || endpoint.Path != "" && endpoint.Path != "/" {
		return errors.New("security writer endpoint must be an exact HTTPS peer endpoint")
	}
	if config.RootCAFile != "" && !filepath.IsAbs(config.RootCAFile) {
		return errors.New("OIDC root CA must be an absolute operator file")
	}
	// Validate operator bindings without reading or returning any secret value.
	if _, err := oidc.NewSecretRegistry(config.SecretBindings); err != nil {
		return errors.New("invalid OIDC operator secret bindings")
	}
	if _, err := oidc.NewFetcher(oidc.FetcherOptions{PrivateOrigins: config.PrivateOrigins}); err != nil {
		return errors.New("invalid OIDC private origin bindings")
	}
	return config.Bootstrap.Validate()
}
func canonicalSecurityOrigin(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Opaque == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == "" && raw == parsed.Scheme+"://"+parsed.Host
}
