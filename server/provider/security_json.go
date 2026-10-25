package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// SecurityTLSConfig carries public-listener material from the single security
// input. The composition root supplies it to the focused TLS provider.
type SecurityTLSConfig struct {
	CertPEM     string `json:"certificate_pem"`
	KeyPEM      string `json:"private_key_pem"`
	ClientCAPEM string `json:"client_ca_pem"`
}

// securityConfigJSON is required operator intent for provisioning, initialization
// and every ordinary start. Only generated identity/trust material is durable.
type securityConfigJSON struct {
	PublicOrigin    string                        `json:"public_origin"`
	MaterialRoot    string                        `json:"material_root"`
	Node            string                        `json:"node,omitempty"`
	PersistenceRoot string                        `json:"persistence_root"`
	TargetPlatform  string                        `json:"target_platform"`
	TLSTermination  string                        `json:"tls_termination"`
	InitialAdmin    securityInitialAdminJSON      `json:"initial_admin"`
	Nodes           []securityNodeJSON            `json:"nodes"`
	Issuers         []securityIssuerJSON          `json:"issuers"`
	SecretBindings  map[string]oidc.SecretBinding `json:"secret_bindings,omitempty"`
	RootCAPEM       string                        `json:"root_ca_pem"`
	PrivateOrigins  map[string][]string           `json:"private_origins"`
	TrustedProxyIPs []string                      `json:"trusted_proxy_ips"`
	TLS             SecurityTLSConfig             `json:"tls"`
}

type securityIssuerJSON struct {
	ID                             string                     `json:"id"`
	Issuer                         string                     `json:"issuer"`
	Enabled                        *bool                      `json:"enabled"`
	ClientID                       string                     `json:"client_id"`
	APIAudience                    string                     `json:"api_audience"`
	Algorithms                     []string                   `json:"algorithms"`
	TokenOrigin                    string                     `json:"token_origin"`
	ClientSecret                   string                     `json:"client_secret"`
	NativeClients                  []securityNativeClientJSON `json:"native_clients"`
	HumanSubjectNamespaceQualified bool                       `json:"human_subject_namespace_qualified"`
}

type securityNativeClientJSON struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	ClientID       string   `json:"client_id"`
	Presenters     []string `json:"presenters"`
	AndroidPackage string   `json:"android_package"`
	SecretID       string   `json:"secret_id"`
	TokenOrigin    string   `json:"token_origin"`
	ClientSecret   string   `json:"client_secret"`
}

type securityInitialAdminJSON struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

type securityNodeJSON struct {
	Name          string `json:"name"`
	ControlOrigin string `json:"control_origin"`
	DataOrigin    string `json:"data_origin"`
	ControlListen string `json:"control_listen"`
	DataListen    string `json:"data_listen"`
}

func loadSecurityConfigJSON(raw string) (SecurityConfig, error) {
	config, err := parseSecurityConfigJSON(raw)
	if err != nil {
		return SecurityConfig{}, err
	}
	return attachSecurityMaterial(config)
}

// LoadSecurityConfigJSON applies the same required configuration and material
// validation as the environment loader, without changing process environment.
func LoadSecurityConfigJSON(raw string) (SecurityConfig, error) {
	return loadSecurityConfigJSON(raw)
}

func selectedSecurityNode(config SecurityConfig) (securityNodeJSON, bool) {
	if config.input != nil {
		for _, node := range config.input.Nodes {
			if node.Name == config.Node {
				return node, true
			}
		}
	}
	return securityNodeJSON{}, false
}

func securityIssuerPreset(input securityIssuerJSON) (securityIssuerJSON, error) {
	if input.Issuer == "google" {
		input.Issuer = security.GoogleIssuer
	}
	if input.Issuer == security.GoogleIssuer {
		if len(input.Algorithms) != 0 && !slices.Equal(input.Algorithms, []string{"RS256"}) || input.TokenOrigin != "" && input.TokenOrigin != "https://oauth2.googleapis.com" {
			return securityIssuerJSON{}, errors.New("Google configuration requires its pinned algorithm and token origin")
		}
		input.Algorithms = []string{"RS256"}
		if input.ClientSecret != "" && input.TokenOrigin == "" {
			input.TokenOrigin = "https://oauth2.googleapis.com"
		}
	}
	return input, nil
}

func parseSecurityConfigJSON(raw string) (SecurityConfig, error) {
	// Leave room below Linux's per-environment-string bound. A deployment
	// platform can impose a smaller limit; no input is split into fragments.
	if len(raw) > 120<<10 {
		return SecurityConfig{}, errors.New("inline security configuration exceeds 120 KiB")
	}
	var input securityConfigJSON
	if err := decodeSecurityConfigJSON(raw, &input, oidc.ValidateOperatorJSON); err != nil {
		return SecurityConfig{}, err
	}
	if input.Issuers == nil || len(input.Issuers) > 64 {
		return SecurityConfig{}, errors.New("security configuration requires an explicit issuers array")
	}
	config := SecurityConfig{
		Mode: "oidc", BrowserOrigin: input.PublicOrigin,
		SecretBindings: map[string]oidc.SecretBinding{}, RootCAPEM: input.RootCAPEM, TLS: input.TLS, input: &input, Issuers: []security.Issuer{},
	}
	for handle, binding := range input.SecretBindings {
		config.SecretBindings[handle] = binding
	}
	if _, err := oidc.NewSecretRegistry(config.SecretBindings); err != nil {
		return SecurityConfig{}, errors.New("invalid configured secret binding")
	}
	if !canonicalSecurityOrigin(config.BrowserOrigin) {
		return SecurityConfig{}, errors.New("inline security configuration requires an exact HTTPS public origin")
	}
	seen := make(map[string]bool, len(input.Issuers))
	for _, item := range input.Issuers {
		var err error
		item, err = securityIssuerPreset(item)
		if err != nil {
			return SecurityConfig{}, err
		}
		if seen[item.Issuer] {
			return SecurityConfig{}, errors.New("duplicate configured Issuer")
		}
		seen[item.Issuer] = true
		issuer, err := compileSecurityIssuer(input.PublicOrigin, item, config.SecretBindings)
		if err != nil {
			return SecurityConfig{}, err
		}
		config.Issuers = append(config.Issuers, issuer)
	}
	if len(input.PrivateOrigins) > 64 {
		return SecurityConfig{}, errors.New("too many OIDC private origins")
	}
	config.PrivateOrigins = make(map[string][]netip.Prefix, len(input.PrivateOrigins))
	for origin, ranges := range input.PrivateOrigins {
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
	if len(input.TrustedProxyIPs) > 16 {
		return SecurityConfig{}, errors.New("too many trusted browser proxies")
	}
	for _, raw := range input.TrustedProxyIPs {
		ip, err := netip.ParseAddr(raw)
		if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return SecurityConfig{}, errors.New("trusted proxy must be an exact IP address")
		}
		config.TrustedProxyIPs = append(config.TrustedProxyIPs, ip.Unmap())
	}
	if input.InitialAdmin.Issuer == "google" {
		input.InitialAdmin.Issuer = security.GoogleIssuer
	}
	if input.Node == "" && len(input.Nodes) == 1 {
		input.Node = input.Nodes[0].Name
	}
	config.MaterialRoot, config.PersistenceRoot, config.Node, config.TargetPlatform = input.MaterialRoot, input.PersistenceRoot, input.Node, input.TargetPlatform
	config.MaterialFile = securityMaterialPath(input.MaterialRoot, input.Node)
	config.StateDirectory = filepath.Join(input.PersistenceRoot, input.Node)
	config.CustodyDirectory = config.StateDirectory
	desired, err := security.NewConfiguredTrust(config.Issuers)
	config.configuredTrust = desired
	if err != nil {
		return SecurityConfig{}, errors.New("invalid configured Issuer trust")
	}
	if err := validateSecurityConfig(config); err != nil {
		return SecurityConfig{}, err
	}
	return config, nil
}

func compileSecurityIssuer(origin string, input securityIssuerJSON, bindings map[string]oidc.SecretBinding) (security.Issuer, error) {
	issuer := security.Issuer{
		URL: input.Issuer, Enabled: input.Enabled == nil || *input.Enabled, ClientID: input.ClientID,
		APIAudience: input.APIAudience, Algorithms: slices.Clone(input.Algorithms), EnvOwned: true, ConfigRevision: 1,
		HumanSubjectNamespaceQualified: input.HumanSubjectNamespaceQualified,
	}
	if len(issuer.Algorithms) == 0 {
		issuer.Algorithms = []string{"RS256"}
	}
	if input.ClientID != "" || input.ClientSecret != "" || input.TokenOrigin != "" {
		issuer.RedirectURI = origin + oidc.CallbackPath(input.Issuer)
		if input.ClientSecret != "" {
			issuer.SecretRef = inlineSecretID(input.ID, input.Issuer, "browser")
			if err := addInlineSecret(bindings, issuer.SecretRef, input.Issuer, input.ClientID, input.TokenOrigin, input.ClientSecret); err != nil {
				return security.Issuer{}, err
			}
		} else if input.TokenOrigin != "" || input.ID != "" {
			return security.Issuer{}, errors.New("configured browser secret requires a value and token origin")
		}
	} else if input.ID != "" {
		return security.Issuer{}, errors.New("browser secret handle requires a browser client")
	}
	for _, profile := range input.NativeClients {
		client := security.NativeClient{
			ID: profile.ID, Kind: profile.Kind, ClientID: profile.ClientID, ServiceOrigin: origin,
			Presenters: slices.Clone(profile.Presenters), AndroidPackage: profile.AndroidPackage,
		}
		slices.Sort(client.Presenters)
		switch profile.Kind {
		case security.GoogleIDToken:
			if profile.SecretID != "" || profile.TokenOrigin != "" || profile.ClientSecret != "" {
				return security.Issuer{}, errors.New("Google native clients cannot carry a client secret")
			}
		case security.AppleNative, security.AppleAndroidWeb:
			client.SecretRef = inlineSecretID(profile.SecretID, input.Issuer, profile.ID)
			if err := addInlineSecret(bindings, client.SecretRef, input.Issuer, profile.ClientID, profile.TokenOrigin, profile.ClientSecret); err != nil {
				return security.Issuer{}, err
			}
			if profile.Kind == security.AppleAndroidWeb {
				client.RedirectURI = origin + "/auth/native/apple/callback/" + profile.ID
			}
		default:
			return security.Issuer{}, errors.New("unknown native client kind")
		}
		issuer.NativeClients = append(issuer.NativeClients, client)
	}
	slices.SortFunc(issuer.NativeClients, func(a, b security.NativeClient) int { return strings.Compare(a.ID, b.ID) })
	if err := security.ValidateIssuerConfiguration(issuer); err != nil {
		return security.Issuer{}, errors.New("invalid configured Issuer")
	}
	return issuer, nil
}

func inlineSecretID(explicit, issuer, client string) string {
	if explicit != "" {
		return explicit
	}
	digest := sha256.Sum256([]byte(issuer + "\x00" + client))
	return hex.EncodeToString(digest[:])
}

func addInlineSecret(bindings map[string]oidc.SecretBinding, name, issuer, clientID, tokenOrigin, value string) error {
	if _, duplicate := bindings[name]; duplicate {
		return errors.New("duplicate configured secret handle")
	}
	bindings[name] = oidc.SecretBinding{Issuer: issuer, ClientID: clientID, TokenOrigin: tokenOrigin, Value: value}
	if _, err := oidc.NewSecretRegistry(bindings); err != nil {
		return errors.New("invalid configured secret binding")
	}
	return nil
}
