package provider

import (
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// PeerPlaneConfig owns the separate workload listener and durable membership.
// Empty configuration disables the private plane; partial configuration fails.
type PeerPlaneConfig struct {
	ListenAddress string
	Identity      PeerIdentityConfig
}

func loadPeerPlaneConfig(auth SecurityConfig, legacy PeerConfig) (PeerPlaneConfig, error) {
	known := map[string]bool{}
	names := []string{"LANTERN_PEER_LISTEN_ADDR", "LANTERN_PEER_DEPLOYMENT", "LANTERN_PEER_MEMBERSHIP_MODE", "LANTERN_PEER_MEMBERSHIP_FILE", "LANTERN_PEER_MEMBERSHIP_STATE_FILE", "LANTERN_PEER_OPERATOR_PUBLIC_KEY_FILE", "LANTERN_PEER_WORKLOAD_ID", "LANTERN_PEER_CERT_FILE", "LANTERN_PEER_KEY_FILE", "LANTERN_PEER_TRUST_CA_FILE"}
	values := make(map[string]string, len(names))
	configured := false
	for _, name := range names {
		known[name] = true
		values[name] = envconfig.String(name, "")
		if _, set := os.LookupEnv(name); set {
			configured = true
		}
	}
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if auth.ProvisionedPeer != nil && (strings.HasPrefix(upper, "LANTERN_PEER_") || upper == "LANTERN_PEERS") {
			return PeerPlaneConfig{}, errors.New("provisioned peer trust cannot be combined with peer environment settings")
		}
		if strings.HasPrefix(upper, "LANTERN_PEER_MEMBERSHIP_") || strings.HasPrefix(upper, "LANTERN_PEER_WORKLOAD_") || strings.HasPrefix(upper, "LANTERN_PEER_OPERATOR_") || strings.HasPrefix(upper, "LANTERN_PEER_TRUST_") {
			if !known[name] {
				return PeerPlaneConfig{}, errors.New("unknown private peer setting")
			}
		}
	}
	if auth.ProvisionedPeer != nil {
		if configured || legacyPeerConfigured(legacy) {
			return PeerPlaneConfig{}, errors.New("provisioned peer trust cannot be combined with peer environment settings")
		}
		if auth.Mode != "oidc" {
			return PeerPlaneConfig{}, errors.New("provisioned peer trust requires OIDC authority")
		}
		cfg := *auth.ProvisionedPeer
		cfg.Identity.FixedMembers = slices.Clone(cfg.Identity.FixedMembers)
		cfg.Identity.AuthMode, cfg.Identity.StateMode = "oidc", "resume"
		if cfg.ListenAddress == "" || cfg.Identity.Now != nil || cfg.Identity.TimeBounds != nil || cfg.Identity.Time != nil {
			return PeerPlaneConfig{}, errors.New("invalid provisioned peer trust")
		}
		return bindPeerPlaneAuthority(auth, cfg)
	}
	if !configured {
		if legacyPeerConfigured(legacy) {
			return PeerPlaneConfig{}, errors.New("HA requires the explicit workload peer plane")
		}
		return PeerPlaneConfig{}, nil
	}
	if legacyPeerConfigured(legacy) {
		return PeerPlaneConfig{}, errors.New("signed membership cannot be combined with legacy peer origins, discovery or credentials")
	}
	for _, name := range names {
		if values[name] == "" {
			return PeerPlaneConfig{}, errors.New("private peer configuration must be complete")
		}
	}
	raw := values["LANTERN_PEER_DEPLOYMENT"]
	id, err := hex.DecodeString(raw)
	if err != nil || len(id) != 16 || raw != hex.EncodeToString(id) {
		return PeerPlaneConfig{}, errors.New("peer deployment must be 32 lowercase hexadecimal characters")
	}
	cfg := PeerPlaneConfig{ListenAddress: values["LANTERN_PEER_LISTEN_ADDR"], Identity: PeerIdentityConfig{
		AuthMode:  auth.Mode,
		StateMode: values["LANTERN_PEER_MEMBERSHIP_MODE"], StateFile: values["LANTERN_PEER_MEMBERSHIP_STATE_FILE"],
		ManifestFile: values["LANTERN_PEER_MEMBERSHIP_FILE"], OperatorKeyFile: values["LANTERN_PEER_OPERATOR_PUBLIC_KEY_FILE"],
		SelfIdentity: values["LANTERN_PEER_WORKLOAD_ID"], CertFile: values["LANTERN_PEER_CERT_FILE"], KeyFile: values["LANTERN_PEER_KEY_FILE"], CAFile: values["LANTERN_PEER_TRUST_CA_FILE"],
	}}
	copy(cfg.Identity.Deployment[:], id)
	return bindPeerPlaneAuthority(auth, cfg)
}

func legacyPeerConfigured(cfg PeerConfig) bool {
	return len(cfg.Peers) != 0 || cfg.Discovery != "static" || cfg.DNSName != "" || cfg.CAFile != "" || cfg.ClientCertFile != "" || cfg.ClientKeyFile != ""
}

func bindPeerPlaneAuthority(auth SecurityConfig, cfg PeerPlaneConfig) (PeerPlaneConfig, error) {
	if auth.Mode == "oidc" {
		provisioned, err := loadSecurityProvisioning(auth)
		if err != nil {
			return PeerPlaneConfig{}, err
		}
		profile := provisioned.Profile()
		if cfg.Identity.CurrentProfile != "" && cfg.Identity.CurrentProfile != profile.Binding() ||
			cfg.Identity.SecurityGeneration != [16]byte{} && cfg.Identity.SecurityGeneration != profile.Generation ||
			cfg.Identity.WriterPublicKey != [32]byte{} {
			return PeerPlaneConfig{}, errors.New("data workload belongs to a different current cohort")
		}
		cfg.Identity.CurrentProfile, cfg.Identity.SecurityGeneration = profile.Binding(), profile.Generation
	}
	if err := validatePeerPlaneConfig(cfg); err != nil {
		return PeerPlaneConfig{}, err
	}
	return cfg, nil
}
func validatePeerPlaneConfig(cfg PeerPlaneConfig) error {
	if cfg.ListenAddress == "" {
		if !reflect.DeepEqual(cfg, PeerPlaneConfig{}) {
			return errors.New("disabled peer plane cannot ignore configuration")
		}
		return nil
	}
	_, port, err := net.SplitHostPort(cfg.ListenAddress)
	n, numberErr := strconv.Atoi(port)
	if err != nil || numberErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("peer listener requires an explicit host:port")
	}
	if cfg.Identity.StateMode != "fresh" && cfg.Identity.StateMode != "resume" || cfg.Identity.Deployment == [16]byte{} {
		return errors.New("peer membership mode and nonzero deployment are required")
	}
	for _, path := range []string{cfg.Identity.StateFile, cfg.Identity.ManifestFile, cfg.Identity.OperatorKeyFile, cfg.Identity.CertFile, cfg.Identity.KeyFile, cfg.Identity.CAFile} {
		if !filepath.IsAbs(path) {
			return errors.New("peer operator files require absolute paths")
		}
	}
	return nil
}
func NewPeerPlaneConfig(cfg *Config) PeerPlaneConfig { return cfg.PeerPlane }
func NewConfiguredPeerIdentity(cfg PeerPlaneConfig) (*PeerIdentityRuntime, func(), error) {
	if err := validatePeerPlaneConfig(cfg); err != nil {
		return nil, nil, err
	}
	if cfg.ListenAddress == "" {
		return nil, func() {}, nil
	}
	return NewPeerIdentityRuntime(cfg.Identity)
}
