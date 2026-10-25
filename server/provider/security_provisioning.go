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
	"strconv"
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
	OperatorDirectory string
	ValidFor          time.Duration
}

type provisionedSecurityPeer struct {
	Domain                                                          peerauth.Domain   `json:"domain"`
	Members                                                         []peerauth.Member `json:"members"`
	VerificationKey                                                 []byte            `json:"verification_key"`
	Identity, Certificate, Key, Roots, OperatorKey, Manifest, State string
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
	mode, raw, err := securityConfigurationEnvironment()
	if err != nil {
		return err
	}
	if mode != "oidc" {
		return errSecurityProvisioning
	}
	config, err := parseSecurityConfigJSON(raw)
	if err != nil {
		return err
	}
	if options.ValidFor == 0 {
		options.ValidFor = 30 * 24 * time.Hour
	}
	if options.ValidFor < time.Hour || options.ValidFor > 365*24*time.Hour ||
		!privateNewDirectory(config.MaterialRoot) || !privateNewDirectory(options.OperatorDirectory) ||
		pathsOverlap(config.MaterialRoot, options.OperatorDirectory) ||
		pathsOverlap(config.input.PersistenceRoot, options.OperatorDirectory) ||
		pathsOverlap(config.input.PersistenceRoot, config.MaterialRoot) {
		return errSecurityProvisioning
	}
	image, err := initialSecurityImage(config)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	intent := security.ProvisioningOptions{Target: config.input.TargetPlatform, Now: now, End: now.Add(options.ValidFor), CleanupBy: now.Add(options.ValidFor + time.Minute), Image: image}
	for _, n := range config.input.Nodes {
		// The historical authority retains a direct advertised-port default.
		// Current local binding is applied separately on every invocation.
		controlOrigin, _ := url.Parse(n.ControlOrigin)
		intent.Nodes = append(intent.Nodes, security.ProvisioningNode{Name: n.Name, ControlOrigin: n.ControlOrigin, DataOrigin: n.DataOrigin, ControlListen: net.JoinHostPort("", controlOrigin.Port()),
			StateDir: filepath.Join(config.input.PersistenceRoot, n.Name), RenewableDir: filepath.Join(config.MaterialRoot, "nodes", n.Name, "membership")})
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
	if err := createPrivateDirectory(config.MaterialRoot); err != nil {
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
	for _, node := range bundle.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(config.MaterialRoot, "nodes", node.Name)
		for _, d := range []string{filepath.Dir(dir), dir, filepath.Join(dir, "membership"), filepath.Join(operator, "certificates")} {
			if err := ensurePrivateDirectory(d); err != nil {
				return err
			}
		}
		dataKey, err := parseOperatorPublicKeyPEM(node.DataOperatorPublicKey)
		if err != nil {
			return errSecurityProvisioning
		}
		d := securityMaterialDocument{Version: 1, Node: node.Name, TargetPlatform: config.TargetPlatform, Nodes: securityMaterialNodes(config.input.Nodes), InitialAdmin: config.input.InitialAdmin, Authority: node.AuthorityJSON, CDC: cdc,
			StatePaths:    securityMaterialStatePaths{PersistenceRoot: config.PersistenceRoot, Directory: filepath.Join(config.PersistenceRoot, node.Name), CustodyDirectory: filepath.Join(config.PersistenceRoot, node.Name)},
			SnapshotSpool: provisionedSnapshotSpool(filepath.Join(config.input.PersistenceRoot, node.Name)),
			Peer: provisionedSecurityPeer{Domain: node.DataDomain, Members: slices.Clone(dataMembers), VerificationKey: slices.Clone(dataKey), Identity: node.Identity, Certificate: filepath.Join(dir, "workload.pem"),
				Key: filepath.Join(dir, "workload-key.pem"), Roots: filepath.Join(dir, "roots.pem"), OperatorKey: filepath.Join(dir, "data-operator.pem"),
				Manifest: filepath.Join(dir, "membership", "data.json"), State: filepath.Join(config.input.PersistenceRoot, node.Name, "data-membership")}}
		raw, err := json.Marshal(d)
		if err != nil {
			return errSecurityProvisioning
		}
		files := map[string][]byte{"identity.json": raw, "workload.pem": node.CertificatePEM, "workload-key.pem": node.TLSKeyPEM, "roots.pem": node.RootsPEM, "data-operator.pem": node.DataOperatorPublicKey,
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
	return syncSecurityDirectory(config.MaterialRoot)
}

// validateSecurityInput applies every-start structural and local trust-boundary
// checks. Fresh administrator usability is deliberately checked only at creation.
func validateSecurityInput(config SecurityConfig) error {
	s := config.input
	if s == nil || s.TargetPlatform != "linux" && s.TargetPlatform != "darwin" || !cleanAbsolutePath(s.PersistenceRoot) || !cleanAbsolutePath(s.MaterialRoot) ||
		pathsOverlap(s.PersistenceRoot, s.MaterialRoot) || len(s.Nodes) < 1 || len(s.Nodes) > 31 ||
		s.InitialAdmin.Issuer == "" || s.InitialAdmin.Subject == "" || len(s.InitialAdmin.Subject) > 255 ||
		s.TLSTermination != "proxy" && s.TLSTermination != "server" || s.TLSTermination == "server" && s.TLS.CertPEM == "" ||
		s.TLSTermination == "proxy" && (len(config.TrustedProxyIPs) == 0 || s.TLS.CertPEM != "" || s.TLS.KeyPEM != "" || s.TLS.ClientCAPEM != "") {
		return errSecurityProvisioning
	}
	seen := map[string]bool{}
	found := false
	for _, node := range s.Nodes {
		if !validSecurityNodeName(node.Name) || seen[node.Name] || !validSecurityPrivateOrigin(node.DataOrigin) || !validSecurityPrivateOrigin(node.ControlOrigin) || !validSecurityLocalListen(node.DataListen) || !matchingSecurityControlListen(node.ControlOrigin, node.ControlListen) {
			return errSecurityProvisioning
		}
		seen[node.Name] = true
		found = found || node.Name == s.Node
	}
	if !found {
		return errors.New("security configuration must select one explicit fixed node")
	}
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "LANTERN_PEER_") || upper == "LANTERN_PEERS" || strings.HasPrefix(upper, "LANTERN_GRAPH_SNAPSHOT_SPOOL_") || strings.HasPrefix(upper, "LANTERN_CDC_") {
			return errors.New("security JSON cannot be combined with separate peer, Snapshot or CDC settings")
		}
	}
	return nil
}

func initialSecurityImage(config SecurityConfig) (security.Image, error) {
	admin := config.input.InitialAdmin
	if admin.Issuer == "google" {
		admin.Issuer = security.GoogleIssuer
	}
	found := false
	for _, issuer := range config.Issuers {
		found = found || issuer.URL == admin.Issuer && issuer.Enabled && issuer.BrowserEnabled()
	}
	if !found || admin.Subject == "" {
		return security.Image{}, errSecurityProvisioning
	}
	image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1, Issuers: config.Issuers,
		Roles: []security.Role{{ID: "security_admin", Name: "Security administrator", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}},
		Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: admin.Issuer, Subject: admin.Subject}, State: security.Active, HumanIssuerConfigRevision: 1,
			Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}}}
	if _, err := security.CompileImage(image, security.DefaultPolicyLimits()); err != nil {
		return security.Image{}, errSecurityProvisioning
	}
	return image, nil
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

func validSecurityPrivateOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || !canonicalSecurityOrigin(origin) || u.Port() == "" {
		return false
	}
	n, err := strconv.Atoi(u.Port())
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == u.Port()
}

func validSecurityLocalListen(listen string) bool {
	host, port, err := net.SplitHostPort(listen)
	n, numberErr := strconv.Atoi(port)
	return err == nil && numberErr == nil && n >= 0 && n <= 65535 && strconv.Itoa(n) == port && !strings.ContainsAny(host, " \t\r\n/\\@?#%")
}

func matchingSecurityControlListen(origin, listen string) bool {
	if !validSecurityLocalListen(listen) {
		return false
	}
	u, err := url.Parse(origin)
	_, port, listenErr := net.SplitHostPort(listen)
	return err == nil && listenErr == nil && port == u.Port()
}

func cleanAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && !strings.ContainsAny(path, "\x00\r\n")
}
func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func securityTargetMatches(target string) bool {
	return target == runtime.GOOS && (runtime.GOARCH == "arm64" || target == "linux" && runtime.GOARCH == "amd64")
}
