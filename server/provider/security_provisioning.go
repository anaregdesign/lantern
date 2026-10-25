package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
)

var errSecurityProvisioning = errors.New("security provisioning is invalid, incomplete or already exists; preserve existing files and inspect the setup")

// SecurityProvisionOptions controls the explicit creation ceremony. Operator
// signing custody must live outside all serving input and state directories.
type SecurityProvisionOptions struct {
	OutputDirectory, OperatorDirectory string
	ValidFor                           time.Duration
}

// provisionedSecurityDocument is generated private input. The original setup
// records the seed policy, never a command to overwrite the current projection.
type provisionedSecurityDocument struct {
	Version       uint32                   `json:"version"`
	Setup         securitySetupJSON        `json:"setup"`
	Node          string                   `json:"node"`
	Authority     json.RawMessage          `json:"authority"`
	CDC           json.RawMessage          `json:"cdc"`
	Peer          provisionedSecurityPeer  `json:"peer"`
	SnapshotSpool GraphSnapshotSpoolConfig `json:"snapshot_spool"`
}

type provisionedSecurityPeer struct {
	Domain                                                                  peerauth.Domain   `json:"domain"`
	Members                                                                 []peerauth.Member `json:"members"`
	VerificationKey                                                         []byte            `json:"verification_key"`
	Listen, Identity, Certificate, Key, Roots, OperatorKey, Manifest, State string
}

// ProvisionSecurity resolves intent once. It never opens native time, a
// listener, serving custody, or existing trust, including across target OSes.
func ProvisionSecurity(ctx context.Context, options SecurityProvisionOptions) error {
	return provisionSecurityWithWriter(ctx, options, writePrivateNewFile)
}

func provisionSecurityWithWriter(ctx context.Context, options SecurityProvisionOptions, write func(string, []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if os.Getenv("LANTERN_AUTH_MODE") != "oidc" {
		return errSecurityProvisioning
	}
	if err := validateAuthMode(); err != nil {
		return err
	}
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if name == "LANTERN_SECURITY_CONFIG_JSON" {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "LANTERN_SECURITY_") || strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_PEER_") || upper == "LANTERN_PEERS" || strings.HasPrefix(upper, "LANTERN_GRAPH_SNAPSHOT_SPOOL_") {
			return errSecurityProvisioning
		}
	}
	config, err := loadSecuritySetupJSON(os.Getenv("LANTERN_SECURITY_CONFIG_JSON"))
	if err != nil {
		return err
	}
	if err := validateSecuritySetup(config); err != nil {
		return err
	}
	if options.ValidFor == 0 {
		options.ValidFor = 30 * 24 * time.Hour
	}
	if options.ValidFor < time.Hour || options.ValidFor > 365*24*time.Hour ||
		!privateNewDirectory(options.OutputDirectory) || !privateNewDirectory(options.OperatorDirectory) ||
		pathsOverlap(options.OutputDirectory, options.OperatorDirectory) ||
		pathsOverlap(config.setup.PersistenceRoot, options.OperatorDirectory) ||
		pathsOverlap(config.setup.PersistenceRoot, options.OutputDirectory) {
		return errSecurityProvisioning
	}
	image, err := initialSecurityImage(config)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	intent := security.ProvisioningOptions{Target: config.setup.TargetPlatform, Now: now, End: now.Add(options.ValidFor), CleanupBy: now.Add(options.ValidFor + time.Minute), Image: image}
	for _, n := range config.setup.Nodes {
		intent.Nodes = append(intent.Nodes, security.ProvisioningNode{Name: n.Name, ControlOrigin: n.ControlOrigin, DataOrigin: n.DataOrigin, ControlListen: n.ControlListen,
			StateDir: filepath.Join(config.setup.PersistenceRoot, n.Name), RenewableDir: filepath.Join(options.OutputDirectory, "nodes", n.Name, "membership")})
	}
	bundle, err := security.GenerateProvisioning(intent)
	if err != nil {
		return errSecurityProvisioning
	}
	defer clear(bundle.Operator.ControlKeyPEM)
	defer clear(bundle.Operator.DataKeyPEM)
	defer clear(bundle.Operator.CAKeyPEM)
	var cursor [32]byte
	if _, err := rand.Read(cursor[:]); err != nil {
		return errSecurityProvisioning
	}
	cdc, _ := json.Marshal(struct {
		Enabled bool   `json:"enabled"`
		Current uint32 `json:"current_version"`
		Keys    []struct {
			Version uint32 `json:"version"`
			Key     string `json:"key"`
		} `json:"keys"`
	}{
		Enabled: true, Current: 1, Keys: []struct {
			Version uint32 `json:"version"`
			Key     string `json:"key"`
		}{{1, hex.EncodeToString(cursor[:])}}})
	clear(cursor[:])
	// Both roots are create-only. Failed or interrupted creation is retained;
	// no subsequent invocation may turn a partial output into fresh trust.
	if err := createPrivateDirectory(options.OutputDirectory); err != nil {
		return err
	}
	if err := createPrivateDirectory(options.OperatorDirectory); err != nil {
		return err
	}
	operator := options.OperatorDirectory
	publisher := membershippublisher.Config{Version: 1, End: intent.End, CleanupBy: intent.CleanupBy, Interval: time.Minute, Lifetime: peerauth.MaxMembershipLifetime}
	publisher.Streams = []membershippublisher.Stream{
		{Kind: "control", KeyFile: filepath.Join(operator, "control-signing-key.pem"), RootsFile: filepath.Join(operator, "roots.pem"), Initial: bundle.Operator.ControlManifest},
		{Kind: "data", KeyFile: filepath.Join(operator, "data-signing-key.pem"), RootsFile: filepath.Join(operator, "roots.pem"), Initial: bundle.Operator.DataManifest},
	}
	completedNodes := map[string][]byte{}
	dataMembers := make([]peerauth.Member, 0, len(bundle.Nodes))
	for _, node := range bundle.Nodes {
		dataMembers = append(dataMembers, node.DataSelf)
	}
	slices.SortFunc(dataMembers, func(a, b peerauth.Member) int { return strings.Compare(a.Identity, b.Identity) })
	for i, node := range bundle.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(options.OutputDirectory, "nodes", node.Name)
		for _, d := range []string{filepath.Dir(dir), dir, filepath.Join(dir, "membership"), filepath.Join(operator, "certificates")} {
			if err := ensurePrivateDirectory(d); err != nil {
				return err
			}
		}
		input := config.setup.Nodes[i]
		dataKey, err := parseOperatorPublicKeyPEM(node.DataOperatorPublicKey)
		if err != nil {
			return errSecurityProvisioning
		}
		d := provisionedSecurityDocument{Version: 1, Setup: *config.setup, Node: node.Name, Authority: node.AuthorityJSON, CDC: cdc,
			SnapshotSpool: provisionedSnapshotSpool(filepath.Join(config.setup.PersistenceRoot, node.Name)),
			Peer: provisionedSecurityPeer{Domain: node.DataDomain, Members: slices.Clone(dataMembers), VerificationKey: slices.Clone(dataKey), Listen: input.DataListen, Identity: node.Identity, Certificate: filepath.Join(dir, "workload.pem"),
				Key: filepath.Join(dir, "workload-key.pem"), Roots: filepath.Join(dir, "roots.pem"), OperatorKey: filepath.Join(dir, "data-operator.pem"),
				Manifest: filepath.Join(dir, "membership", "data.json"), State: filepath.Join(config.setup.PersistenceRoot, node.Name, "data-membership")}}
		raw, err := json.Marshal(d)
		if err != nil {
			return errSecurityProvisioning
		}
		files := map[string][]byte{"security.json": raw, "workload.pem": node.CertificatePEM, "workload-key.pem": node.TLSKeyPEM, "roots.pem": node.RootsPEM, "data-operator.pem": node.DataOperatorPublicKey,
			"membership/control.json": bundle.Operator.ControlManifest, "membership/data.json": node.DataManifest}
		for name, value := range files {
			if err := write(filepath.Join(dir, name), value); err != nil {
				return err
			}
		}
		completedNodes[dir] = raw
		certificate := filepath.Join(operator, "certificates", node.Name+".pem")
		if err := write(certificate, node.CertificatePEM); err != nil {
			return err
		}
		for j := range publisher.Streams {
			kind := publisher.Streams[j].Kind
			publisher.Streams[j].Targets = append(publisher.Streams[j].Targets, filepath.Join(dir, "membership", kind+".json"))
			publisher.Streams[j].Certificates = append(publisher.Streams[j].Certificates, certificate)
		}
	}
	publisherRaw, err := json.Marshal(publisher)
	if err != nil {
		return errSecurityProvisioning
	}
	for name, value := range map[string][]byte{"control-signing-key.pem": bundle.Operator.ControlKeyPEM, "data-signing-key.pem": bundle.Operator.DataKeyPEM, "roots.pem": bundle.Operator.RootsPEM, "ca-signing-key.pem": bundle.Operator.CAKeyPEM, "publisher.json": publisherRaw} {
		if err := write(filepath.Join(operator, name), value); err != nil {
			return err
		}
	}
	if err := syncSecurityDirectory(operator); err != nil {
		return err
	}
	for dir, raw := range completedNodes {
		marker, err := securityCompletionBytes(raw, sha256.Sum256(publisherRaw), bundle.Operator.ControlKeyPEM)
		if err != nil {
			return err
		}
		if err := write(filepath.Join(dir, "COMPLETE"), marker); err != nil {
			return err
		}
	}
	return syncSecurityDirectory(options.OutputDirectory)
}

func validateSecuritySetup(config SecurityConfig) error {
	s := config.setup
	if s == nil || s.TargetPlatform != "linux" && s.TargetPlatform != "darwin" || !cleanAbsolutePath(s.PersistenceRoot) || len(s.Nodes) < 1 || len(s.Nodes) > 31 ||
		s.TLSTermination != "proxy" && s.TLSTermination != "server" || s.TLSTermination == "server" && s.TLS.CertPEM == "" ||
		s.TLSTermination == "proxy" && (len(config.TrustedProxyIPs) == 0 || s.TLS.CertPEM != "" || s.TLS.KeyPEM != "" || s.TLS.ClientCAPEM != "") {
		return errSecurityProvisioning
	}
	seen := map[string]bool{}
	for _, node := range s.Nodes {
		if !validSecurityNodeName(node.Name) || seen[node.Name] || !matchingPrivateListener(node.DataOrigin, node.DataListen) || !matchingPrivateListener(node.ControlOrigin, node.ControlListen) {
			return errSecurityProvisioning
		}
		seen[node.Name] = true
	}
	_, err := initialSecurityImage(config)
	return err
}

func initialSecurityImage(config SecurityConfig) (security.Image, error) {
	admin := config.setup.InitialAdmin
	if admin.Issuer == "google" {
		admin.Issuer = security.GoogleIssuer
	}
	found := false
	for _, issuer := range config.InitialIssuers {
		found = found || issuer.URL == admin.Issuer && issuer.Enabled && issuer.BrowserEnabled()
	}
	if !found || admin.Subject == "" {
		return security.Image{}, errSecurityProvisioning
	}
	image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1, Issuers: config.InitialIssuers,
		Roles: []security.Role{{ID: "security_admin", Name: "Security administrator", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}},
		Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: admin.Issuer, Subject: admin.Subject}, State: security.Active, HumanIssuerConfigRevision: 1,
			Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}}}
	if _, err := security.CompileImage(image, security.DefaultPolicyLimits()); err != nil {
		return security.Image{}, errSecurityProvisioning
	}
	return image, nil
}

func loadProvisionedSecurity(path string) (SecurityConfig, error) {
	raw, err := readSecurityOperatorFile(path, true, security.MaxImageBytes+(2<<20))
	if err != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	defer clear(raw)
	var d provisionedSecurityDocument
	if err := decodeSecurityConfigJSON(string(raw), &d, func(raw []byte) error { return security.ValidateProvisioningJSON(raw) }); err != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	if d.Version != 1 || len(d.Authority) == 0 {
		return SecurityConfig{}, errSecurityProvisioning
	}
	setupRaw, err := json.Marshal(d.Setup)
	if err != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	config, err := loadSecuritySetupJSON(string(setupRaw))
	if err != nil || validateSecuritySetup(config) != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	found := false
	for _, node := range d.Setup.Nodes {
		found = found || node.Name == d.Node && node.DataListen == d.Peer.Listen
	}
	if !found {
		return SecurityConfig{}, errSecurityProvisioning
	}
	dir := filepath.Dir(path)
	if verifySecurityCompletion(dir, raw, d.Authority) != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	state := filepath.Join(d.Setup.PersistenceRoot, d.Node)
	if validateProvisionedSnapshotSpool(d.SnapshotSpool, state) != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	if d.Peer.Certificate != filepath.Join(dir, "workload.pem") || d.Peer.Key != filepath.Join(dir, "workload-key.pem") || d.Peer.Roots != filepath.Join(dir, "roots.pem") ||
		d.Peer.OperatorKey != filepath.Join(dir, "data-operator.pem") || d.Peer.Manifest != filepath.Join(dir, "membership", "data.json") || d.Peer.State != filepath.Join(state, "data-membership") {
		return SecurityConfig{}, errSecurityProvisioning
	}
	image, err := initialSecurityImage(config)
	if err != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	manifest, err := readSecurityOperatorFile(filepath.Join(dir, "membership", "control.json"), true, peerauth.MaxManifestBytes)
	if err != nil || security.ValidateProvisioningAuthorityPaths(d.Authority, state, filepath.Join(dir, "membership")) != nil || security.ValidateProvisioningAuthorityJSON(d.Authority, manifest, d.Setup.TargetPlatform, image) != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	config.AuthorityJSON, config.CDC = append([]byte(nil), d.Authority...), append([]byte(nil), d.CDC...)
	config.InitialImage = &image
	config.ProvisionedFile, config.StateDirectory, config.TargetPlatform = path, filepath.Join(d.Setup.PersistenceRoot, d.Node), d.Setup.TargetPlatform
	config.ProvisionedSnapshotSpool = &d.SnapshotSpool
	profile, err := security.ProvisioningAuthorityProfile(d.Authority, manifest, d.Setup.TargetPlatform)
	if err != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	identity, err := loadProvisionedPeerIdentity(d.Peer, profile, len(d.Setup.Nodes))
	if err != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	config.ProvisionedPeer = &PeerPlaneConfig{ListenAddress: d.Peer.Listen, Identity: identity}
	config.setup = nil
	if err := validateSecurityConfig(config); err != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	return config, nil
}

func validSecurityNodeName(s string) bool {
	if len(s) < 1 || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return s[0] != '-' && s[len(s)-1] != '-'
}

func matchingPrivateListener(origin, listen string) bool {
	u, err := url.Parse(origin)
	_, port, e := net.SplitHostPort(listen)
	return err == nil && e == nil && u.Scheme == "https" && u.Port() != "" && u.Port() == port
}

func cleanAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/"
}
func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func securityTargetMatches(target string) bool {
	return target == runtime.GOOS && (runtime.GOARCH == "arm64" || target == "linux" && runtime.GOARCH == "amd64")
}
