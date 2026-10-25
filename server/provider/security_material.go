package provider

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// securityMaterialDocument is immutable original trust, never a saved startup
// configuration. The historical genesis Image is an independent replay anchor;
// current provider configuration must arrive separately on every invocation.
type securityMaterialDocument struct {
	Version         uint32                     `json:"version"`
	Node            string                     `json:"node"`
	TargetPlatform  string                     `json:"target_platform"`
	Nodes           []securityMaterialNode     `json:"nodes"`
	InitialAdmin    securityInitialAdminJSON   `json:"initial_admin"`
	StatePaths      securityMaterialStatePaths `json:"state_paths"`
	Authority       json.RawMessage            `json:"authority"`
	CDC             json.RawMessage            `json:"cdc"`
	Peer            provisionedSecurityPeer    `json:"peer"`
	SnapshotSpool   GraphSnapshotSpoolConfig   `json:"snapshot_spool"`
	historicalImage security.Image
	controlManifest []byte
	peerIdentity    PeerIdentityConfig
}

// SecurityMaterial describes the single immutable original-material format.
type SecurityMaterial = securityMaterialDocument

// SecurityMaterialNode describes one fixed workload endpoint pair.
type SecurityMaterialNode = securityMaterialNode

type securityMaterialNode struct {
	Name          string `json:"name"`
	ControlOrigin string `json:"control_origin"`
	DataOrigin    string `json:"data_origin"`
}

func securityMaterialNodes(nodes []securityNodeJSON) []securityMaterialNode {
	endpoints := make([]securityMaterialNode, len(nodes))
	for i, node := range nodes {
		endpoints[i] = securityMaterialNode{Name: node.Name, ControlOrigin: node.ControlOrigin, DataOrigin: node.DataOrigin}
	}
	return endpoints
}

// SecurityMaterialAdministrator identifies the immutable historical seed.
type SecurityMaterialAdministrator = securityInitialAdminJSON

// SecurityMaterialStatePaths binds journal and custody locations without
// duplicating the individual native paths already contained in Authority.
type SecurityMaterialStatePaths = securityMaterialStatePaths

type securityMaterialStatePaths struct {
	PersistenceRoot  string `json:"persistence_root"`
	Directory        string `json:"directory"`
	CustodyDirectory string `json:"custody_directory"`
}

// SecurityPeerMaterial pins original data membership and verification trust.
type SecurityPeerMaterial = provisionedSecurityPeer

func securityMaterialPath(root, node string) string {
	return filepath.Join(root, "nodes", node, "identity.json")
}

// readSecurityMaterial validates all original inputs without a current provider
// configuration, native state creation, trusted-time acquisition or listeners.
func readSecurityMaterial(path string) (securityMaterialDocument, []byte, error) {
	var d securityMaterialDocument
	if !cleanAbsolutePath(path) || filepath.Base(path) != "identity.json" {
		return d, nil, errSecurityProvisioning
	}
	raw, err := readSecurityOperatorFile(path, true, security.MaxImageBytes+(2<<20))
	if err != nil {
		return d, nil, errSecurityProvisioning
	}
	if decodeSecurityConfigJSON(string(raw), &d, security.ValidateProvisioningJSON) != nil || d.Version != 1 || len(d.Authority) == 0 ||
		d.TargetPlatform != "linux" && d.TargetPlatform != "darwin" || len(d.Nodes) < 1 || len(d.Nodes) > 31 ||
		d.InitialAdmin.Issuer == "" || d.InitialAdmin.Subject == "" || !validSecurityNodeName(d.Node) {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) != d.Node || filepath.Base(filepath.Dir(dir)) != "nodes" || verifySecurityCompletion(dir, raw, d.Authority) != nil {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	state := d.StatePaths.Directory
	materialRoot := filepath.Dir(filepath.Dir(dir))
	if !validSecurityMaterialStatePaths(d.StatePaths, d.Node, materialRoot) || validateProvisionedSnapshotSpool(d.SnapshotSpool, state) != nil ||
		d.Peer.Certificate != filepath.Join(dir, "workload.pem") || d.Peer.Key != filepath.Join(dir, "workload-key.pem") || d.Peer.Roots != filepath.Join(dir, "roots.pem") ||
		d.Peer.OperatorKey != filepath.Join(dir, "data-operator.pem") || d.Peer.Manifest != filepath.Join(dir, "membership", "data.json") || d.Peer.State != filepath.Join(state, "data-membership") {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	seen := map[string]bool{}
	found := false
	for _, node := range d.Nodes {
		if !validSecurityNodeName(node.Name) || seen[node.Name] || !validSecurityPrivateOrigin(node.ControlOrigin) || !validSecurityPrivateOrigin(node.DataOrigin) {
			return securityMaterialDocument{}, nil, errSecurityProvisioning
		}
		seen[node.Name] = true
		found = found || node.Name == d.Node
	}
	if !found {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	image, err := security.ProvisioningInitialImage(d.Authority)
	if err != nil || !historicalSeedIdentity(image, d.InitialAdmin) {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	manifest, err := readSecurityOperatorFile(filepath.Join(dir, "membership", "control.json"), true, peerauth.MaxManifestBytes)
	if err != nil || security.ValidateProvisioningAuthorityLocations(d.Authority, state, d.StatePaths.CustodyDirectory, filepath.Join(dir, "membership")) != nil ||
		!distinctSecurityMaterialFamilies(d) ||
		security.ValidateProvisioningAuthorityJSON(d.Authority, manifest, d.TargetPlatform, image) != nil {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	profile, err := security.ProvisioningAuthorityProfile(d.Authority, manifest, d.TargetPlatform)
	if err != nil {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	identity, err := loadProvisionedPeerIdentity(d.Peer, profile, len(d.Nodes))
	if err != nil {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	if _, err := loadJSONChangeConfig(d.CDC); err != nil {
		return securityMaterialDocument{}, nil, errSecurityProvisioning
	}
	d.historicalImage, d.controlManifest, d.peerIdentity = image, slices.Clone(manifest), identity
	return d, raw, nil
}

func distinctSecurityMaterialFamilies(d securityMaterialDocument) bool {
	// Native validation owns the complete typed authority. This projection adds
	// only collisions with the independently owned peer checkpoint and spool.
	var authority struct {
		Participant struct{ PPath, BPath string } `json:"participant"`
		Membership  struct{ Path string }         `json:"membership"`
		FloorsFile  string                        `json:"floors_file"`
	}
	if json.Unmarshal(d.Authority, &authority) != nil {
		return false
	}
	paths := []string{authority.FloorsFile, authority.FloorsFile + ".state", authority.FloorsFile + ".state.lease", d.Peer.State, d.Peer.State + ".lease"}
	for _, path := range []string{authority.Participant.PPath, authority.Participant.BPath, authority.Membership.Path} {
		paths = append(paths, path, path+".tip", path+".lease")
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] || pathsOverlap(path, d.SnapshotSpool.Directory) || strings.HasPrefix(path, d.Peer.State+".next-") {
			return false
		}
		seen[path] = true
	}
	return true
}

func validSecurityMaterialStatePaths(paths securityMaterialStatePaths, node, materialRoot string) bool {
	return cleanAbsolutePath(paths.PersistenceRoot) && cleanAbsolutePath(paths.Directory) && cleanAbsolutePath(paths.CustodyDirectory) &&
		(paths.Directory == paths.PersistenceRoot || paths.Directory == filepath.Join(paths.PersistenceRoot, node)) &&
		!pathsOverlap(paths.PersistenceRoot, materialRoot) && !pathsOverlap(paths.CustodyDirectory, materialRoot) &&
		(paths.CustodyDirectory == paths.Directory || !pathsOverlap(paths.CustodyDirectory, paths.Directory))
}

func historicalSeedIdentity(image security.Image, admin securityInitialAdminJSON) bool {
	snapshot, err := security.CompileImage(image, security.DefaultPolicyLimits())
	if err != nil {
		return false
	}
	identity := security.Identity{Kind: security.OIDCPrincipal, Issuer: admin.Issuer, Subject: admin.Subject}
	access, active := snapshot.AccessFor(identity)
	return active && snapshot.HumanIdentity(identity) && access.AllowsGlobal(security.SecurityManage)
}

func attachSecurityMaterial(config SecurityConfig) (SecurityConfig, error) {
	d, _, err := readSecurityMaterial(config.MaterialFile)
	if err != nil {
		return SecurityConfig{}, err
	}
	if config.input == nil || d.Node != config.Node || d.TargetPlatform != config.TargetPlatform || d.InitialAdmin != config.input.InitialAdmin ||
		!slices.Equal(d.Nodes, securityMaterialNodes(config.input.Nodes)) || d.StatePaths.PersistenceRoot != config.PersistenceRoot {
		return SecurityConfig{}, errSecurityProvisioning
	}
	node, found := selectedSecurityNode(config)
	if !found || security.ValidateProvisioningLocalListen(d.Authority, node.ControlListen) != nil ||
		security.ValidateProvisioningConfiguredTrust(d.Authority, d.controlManifest, config.TargetPlatform, config.configuredTrust) != nil {
		return SecurityConfig{}, errSecurityProvisioning
	}
	config.AuthorityJSON, config.CDC = slices.Clone(d.Authority), slices.Clone(d.CDC)
	config.StateDirectory, config.CustodyDirectory = d.StatePaths.Directory, d.StatePaths.CustodyDirectory
	config.authorityManifest = slices.Clone(d.controlManifest)
	config.HistoricalImage = &d.historicalImage
	config.ProvisionedSnapshotSpool = &d.SnapshotSpool
	config.ProvisionedPeer = &PeerPlaneConfig{ListenAddress: node.DataListen, Identity: d.peerIdentity}
	if err := validateSecurityConfig(config); err != nil {
		return SecurityConfig{}, err
	}
	return config, nil
}
