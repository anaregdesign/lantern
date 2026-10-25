package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
)

type fixtureStartupNode struct {
	Name          string `json:"name"`
	ControlOrigin string `json:"control_origin"`
	DataOrigin    string `json:"data_origin"`
	ControlListen string `json:"control_listen"`
	DataListen    string `json:"data_listen"`
}

// Fixture material construction preserves the independently authored historical
// image. It never uses the default production seed builder or opens native state.
func attachFixtureSecurity(f *fixture, directory string) error {
	if f == nil || len(f.Nodes) == 0 {
		return errors.New("original fixture nodes required")
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	root := filepath.Join(directory, "authority")
	materialRoot := filepath.Join(directory, "identity")
	runtimeMaterialRoot, runtimeStateRoot, target := materialRoot, filepath.Join(root, "state"), runtime.GOOS
	if !f.compose {
		if err := os.Mkdir(runtimeStateRoot, 0700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	if f.compose {
		runtimeMaterialRoot, runtimeStateRoot, target = "/run/lantern-config/identity", "/state", "linux"
	}
	type authorityInput struct {
		Membership struct {
			Self         peerauth.Member
			PublicKey    []byte `json:"public_key"`
			ManifestFile string `json:"manifest_file"`
		}
		Listen string `json:"listen_address"`
	}
	inputs := make([]authorityInput, len(f.Nodes))
	authorities := make([][]byte, len(f.Nodes))
	nodes := make([]fixtureStartupNode, len(f.Nodes))
	var profile security.CurrentProfile
	var initial provider.SecurityMaterialAdministrator
	for i := range f.Nodes {
		raw, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("node-%d", i+1), "authority.json"))
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, &inputs[i]) != nil {
			return errors.New("invalid original fixture authority")
		}
		manifest, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("node-%d", i+1), "membership.signed"))
		if err != nil {
			return err
		}
		originalProfile, err := security.ProvisioningAuthorityProfile(raw, manifest, target)
		if err != nil {
			return err
		}
		// Resolve state and renewable paths before the first native open. The
		// historical genesis and every original key remain byte-equivalent.
		raw, err = fixtureAuthorityPaths(raw, filepath.Join(runtimeStateRoot, fmt.Sprintf("node-%d", i+1)), filepath.Join(runtimeMaterialRoot, "nodes", fmt.Sprintf("node-%d", i+1)))
		if err != nil {
			return err
		}
		authorities[i] = raw
		if i == 0 {
			profile = originalProfile
			image, err := security.ProvisioningInitialImage(raw)
			if err != nil {
				return err
			}
			snapshot, err := security.CompileImage(image, security.DefaultPolicyLimits())
			if err != nil {
				return err
			}
			for _, actor := range image.Principals {
				access, active := snapshot.AccessFor(actor.Identity)
				if active && actor.Identity.Kind == security.OIDCPrincipal && snapshot.HumanIdentity(actor.Identity) && access.AllowsGlobal(security.SecurityManage) {
					initial = provider.SecurityMaterialAdministrator{Issuer: actor.Identity.Issuer, Subject: actor.Identity.Subject}
					break
				}
			}
			if initial.Subject == "" {
				return errors.New("historical fixture administrator required")
			}
		} else if originalProfile != profile {
			return errors.New("original fixture cohort drift")
		}
		nodes[i] = fixtureStartupNode{Name: fmt.Sprintf("node-%d", i+1), ControlOrigin: inputs[i].Membership.Self.Origin, ControlListen: inputs[i].Listen}
	}
	// A fixed standalone deployment still has independently signed data trust.
	if len(f.dataMembership.Members) == 0 {
		if len(f.dataOperator) != ed25519.PrivateKeySize {
			return errors.New("fixture data operator required")
		}
		f.dataMembership = peerauth.Manifest{Version: 1, Domain: peerauth.Domain{Deployment: [16]byte{99}, NamespaceFormat: "namespaced-v1", AuthMode: "oidc"}}
		var envelope struct {
			Payload peerauth.ControlManifest `json:"payload"`
		}
		control, err := os.ReadFile(inputs[0].Membership.ManifestFile)
		if err != nil || json.Unmarshal(control, &envelope) != nil {
			return errors.New("fixture control membership required")
		}
		f.dataMembership.IssuedAt, f.dataMembership.ExpiresAt = envelope.Payload.IssuedAt, envelope.Payload.ExpiresAt
		for i, input := range inputs {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return err
			}
			address := listener.Addr().String()
			_ = listener.Close()
			member := input.Membership.Self
			member.Origin = "https://" + address
			f.dataMembership.Members = append(f.dataMembership.Members, member)
			env := f.Nodes[i].Environment
			env["LANTERN_PEER_LISTEN_ADDR"] = address
			source := filepath.Join(root, fmt.Sprintf("node-%d", i+1))
			env["LANTERN_PEER_CERT_FILE"], env["LANTERN_PEER_KEY_FILE"], env["LANTERN_PEER_TRUST_CA_FILE"] = filepath.Join(source, "cert.pem"), filepath.Join(source, "tls.key"), filepath.Join(source, "roots.pem")
			env["LANTERN_PEER_WORKLOAD_ID"] = member.Identity
			env["LANTERN_PEER_OPERATOR_PUBLIC_KEY_FILE"] = filepath.Join(directory, "operator.pub")
		}
	}
	f.dataMembership.Domain.AuthMode = "oidc"
	f.dataMembership.Domain.CurrentProfile = profile.Binding()
	f.dataMembership.Domain.SecurityGeneration = profile.Generation
	roots, err := os.ReadFile(f.Nodes[0].Environment["LANTERN_PEER_TRUST_CA_FILE"])
	if err != nil {
		return err
	}
	f.dataMembership.Domain.TrustDigest = sha256.Sum256(roots)
	data, err := peerauth.SignManifest(f.dataMembership, f.dataOperator)
	if err != nil {
		return err
	}
	for i := range f.Nodes {
		nodes[i].DataOrigin = f.dataMembership.Members[i].Origin
		nodes[i].DataListen = f.Nodes[i].Environment["LANTERN_PEER_LISTEN_ADDR"]
		if f.compose {
			nodes[i].DataListen = "0.0.0.0:6381"
		}
	}
	materialNodes := make([]provider.SecurityMaterialNode, len(nodes))
	for i, n := range nodes {
		materialNodes[i] = provider.SecurityMaterialNode{Name: n.Name, ControlOrigin: n.ControlOrigin, DataOrigin: n.DataOrigin}
	}
	operator, err := os.ReadFile(filepath.Join(root, "control-operator.key"))
	if err != nil {
		return err
	}
	operatorDigest := sha256.Sum256(operator)
	for i := range f.Nodes {
		node := &f.Nodes[i]
		env := node.Environment
		name := nodes[i].Name
		dir := filepath.Join(materialRoot, "nodes", name)
		runtimeDir := filepath.Join(runtimeMaterialRoot, "nodes", name)
		if err := os.MkdirAll(filepath.Join(dir, "membership"), 0700); err != nil {
			return err
		}
		pubRaw, err := os.ReadFile(env["LANTERN_PEER_OPERATOR_PUBLIC_KEY_FILE"])
		if err != nil {
			return err
		}
		block, _ := pem.Decode(pubRaw)
		if block == nil {
			return errors.New("fixture data verification key required")
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		pub, ok := parsed.(ed25519.PublicKey)
		if err != nil || !ok {
			return errors.New("fixture data verification key invalid")
		}
		cdcRaw, err := os.ReadFile(env["LANTERN_CDC_CURSOR_KEY_RING_FILE"])
		if err != nil {
			return err
		}
		var cdc map[string]any
		if json.Unmarshal(cdcRaw, &cdc) != nil {
			return errors.New("fixture cursor material invalid")
		}
		cdc["enabled"] = true
		cdcRaw, _ = json.Marshal(cdc)
		material := provider.SecurityMaterial{StatePaths: provider.SecurityMaterialStatePaths{PersistenceRoot: runtimeStateRoot, Directory: filepath.Join(runtimeStateRoot, name), CustodyDirectory: filepath.Join(runtimeStateRoot, name)}, Version: 1, Node: name, TargetPlatform: target, Nodes: materialNodes, InitialAdmin: initial, Authority: authorities[i], CDC: cdcRaw, SnapshotSpool: provider.GraphSnapshotSpoolConfig{Directory: filepath.Join(runtimeStateRoot, name, "snapshot-spool"), MaxDiskBytes: 128 << 20, MaxFrameBytes: 32 << 20}, Peer: provider.SecurityPeerMaterial{Domain: f.dataMembership.Domain, Members: f.dataMembership.Members, VerificationKey: pub, Identity: f.dataMembership.Members[i].Identity, Certificate: filepath.Join(runtimeDir, "workload.pem"), Key: filepath.Join(runtimeDir, "workload-key.pem"), Roots: filepath.Join(runtimeDir, "roots.pem"), OperatorKey: filepath.Join(runtimeDir, "data-operator.pem"), Manifest: filepath.Join(runtimeDir, "membership", "data.json"), State: filepath.Join(runtimeStateRoot, name, "data-membership")}}
		materialRaw, err := json.Marshal(material)
		if err != nil {
			return err
		}
		complete, err := provider.SecurityMaterialCompletion(materialRaw, operatorDigest, operator)
		if err != nil {
			return err
		}
		files := map[string][]byte{"identity.json": materialRaw, "COMPLETE": complete, "data-operator.pem": pubRaw, "membership/data.json": data}
		for source, target := range map[string]string{env["LANTERN_PEER_CERT_FILE"]: "workload.pem", env["LANTERN_PEER_KEY_FILE"]: "workload-key.pem", env["LANTERN_PEER_TRUST_CA_FILE"]: "roots.pem", filepath.Join(root, name, "membership.signed"): "membership/control.json"} {
			raw, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			files[target] = raw
		}
		for path, raw := range files {
			if _, err := writeFile(dir, path, raw); err != nil {
				return err
			}
		}
		image, err := security.ProvisioningInitialImage(authorities[i])
		if err != nil {
			return err
		}
		var issuers []map[string]any = make([]map[string]any, 0)
		for _, issuer := range image.Issuers {
			if issuer.EnvOwned {
				issuers = append(issuers, fixtureIssuerIntent(issuer))
			}
		}
		tls := provider.SecurityTLSConfig{}
		for path, target := range map[string]*string{env["LANTERN_TLS_CERT_FILE"]: &tls.CertPEM, env["LANTERN_TLS_KEY_FILE"]: &tls.KeyPEM, env["LANTERN_TLS_CLIENT_CA_FILE"]: &tls.ClientCAPEM} {
			if path != "" {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				*target = string(raw)
			}
		}
		ca, err := os.ReadFile(f.CAFile)
		if err != nil {
			return err
		}
		origin := "https://admin.example"
		if f.security != nil && f.security.Issuer != nil {
			u, err := url.Parse(f.security.Issuer.RedirectURI)
			if err != nil {
				return err
			}
			origin = u.Scheme + "://" + u.Host
		}
		private := map[string][]string{}
		for _, issuer := range image.Issuers {
			u, _ := url.Parse(issuer.URL)
			if u != nil && u.Hostname() == "127.0.0.1" {
				private[issuer.URL] = []string{"127.0.0.1/32"}
			}
		}
		startup := map[string]any{"public_origin": origin, "material_root": runtimeMaterialRoot, "persistence_root": runtimeStateRoot, "node": name, "target_platform": target, "tls_termination": "server", "initial_admin": initial, "nodes": nodes, "issuers": issuers, "root_ca_pem": string(ca), "private_origins": private, "trusted_proxy_ips": []string{}, "tls": tls}
		path, err := writeJSON(filepath.Join(root, name), "startup.json", startup)
		if err != nil {
			return err
		}
		node.StartupFile = path
		for key := range env {
			upper := strings.ToUpper(key)
			if strings.HasPrefix(upper, "LANTERN_SECURITY_") || strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_PEER_") || strings.HasPrefix(upper, "LANTERN_CDC_") || strings.HasPrefix(upper, "LANTERN_TLS_") || strings.HasPrefix(upper, "LANTERN_GRAPH_SNAPSHOT_SPOOL_") {
				delete(env, key)
			}
		}
		env["LANTERN_AUTH_MODE"] = "oidc"
		node.originalMember = uint32(i + 1)
	}
	return nil
}

func fixtureIssuerIntent(issuer security.Issuer) map[string]any {
	native := make([]map[string]any, 0, len(issuer.NativeClients))
	for _, c := range issuer.NativeClients {
		native = append(native, map[string]any{"id": c.ID, "kind": c.Kind, "client_id": c.ClientID, "presenters": c.Presenters, "android_package": c.AndroidPackage})
	}
	return map[string]any{"issuer": issuer.URL, "enabled": issuer.Enabled, "client_id": issuer.ClientID, "api_audience": issuer.APIAudience, "algorithms": issuer.Algorithms, "human_subject_namespace_qualified": issuer.HumanSubjectNamespaceQualified, "native_clients": native}
}

func fixtureAuthorityPaths(raw []byte, state, material string) ([]byte, error) {
	var d map[string]any
	if json.Unmarshal(raw, &d) != nil {
		return nil, errors.New("original authority document invalid")
	}
	participant, ok := d["participant"].(map[string]any)
	if !ok {
		return nil, errors.New("original participant required")
	}
	membership, ok := d["membership"].(map[string]any)
	if !ok {
		return nil, errors.New("original membership required")
	}
	participant["PPath"], participant["BPath"] = filepath.Join(state, "control-protocol.wal"), filepath.Join(state, "control-materialized.wal")
	membership["path"], membership["manifest_file"] = filepath.Join(state, "control-membership"), filepath.Join(material, "membership", "control.json")
	d["floors_file"] = filepath.Join(state, "control-floors.json")
	return json.Marshal(d)
}

func fixtureDataListen(node fixtureNode) (string, error) {
	raw, err := os.ReadFile(node.StartupFile)
	if err != nil {
		return "", err
	}
	var input struct {
		Node  string               `json:"node"`
		Nodes []fixtureStartupNode `json:"nodes"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return "", errors.New("current fixture startup invalid")
	}
	for _, n := range input.Nodes {
		if n.Name == input.Node {
			return n.DataListen, nil
		}
	}
	return "", errors.New("selected fixture node missing")
}

func fixturePeerMaterial(node fixtureNode) (provider.PeerIdentityConfig, error) {
	if node.StartupFile == "" {
		return provider.PeerIdentityConfig{CertFile: node.Environment["LANTERN_PEER_CERT_FILE"], KeyFile: node.Environment["LANTERN_PEER_KEY_FILE"], SelfIdentity: node.Environment["LANTERN_PEER_WORKLOAD_ID"]}, nil
	}
	raw, err := os.ReadFile(node.StartupFile)
	if err != nil {
		return provider.PeerIdentityConfig{}, err
	}
	config, err := provider.LoadSecurityConfigJSON(string(raw))
	if err != nil {
		return provider.PeerIdentityConfig{}, err
	}
	if config.ProvisionedPeer == nil {
		return provider.PeerIdentityConfig{}, errors.New("fixed fixture peer material missing")
	}
	return config.ProvisionedPeer.Identity, nil
}

func updateFixtureDataListen(node fixtureNode, address string) error {
	raw, err := os.ReadFile(node.StartupFile)
	if err != nil {
		return err
	}
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil {
		return errors.New("current fixture startup invalid")
	}
	selected, ok := input["node"].(string)
	if !ok {
		return errors.New("current selected fixture node missing")
	}
	nodes, ok := input["nodes"].([]any)
	if !ok {
		return errors.New("current fixture nodes missing")
	}
	found := false
	for _, entry := range nodes {
		n, ok := entry.(map[string]any)
		if !ok {
			return errors.New("current fixture node invalid")
		}
		if n["name"] == selected {
			n["data_listen"] = address
			found = true
		}
	}
	if !found {
		return errors.New("current selected fixture node missing")
	}
	raw, err = json.Marshal(input)
	if err != nil {
		return err
	}
	return replaceFixtureFile(node.StartupFile, raw, 0600)
}
