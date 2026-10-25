package integration_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/provider"
)

type authorityWireMaterialMetadata struct {
	Profile        struct{ Generation [16]byte }          `json:"profile"`
	ProfileBinding string                                 `json:"profile_binding"`
	InitialAdmin   provider.SecurityMaterialAdministrator `json:"initial_admin"`
	Nodes          []struct {
		AuthorityFile string `json:"authority_file"`
	} `json:"nodes"`
}

type authorityWireMaterialNode struct {
	Name          string `json:"name"`
	ControlOrigin string `json:"control_origin"`
	DataOrigin    string `json:"data_origin"`
	ControlListen string `json:"control_listen"`
	DataListen    string `json:"data_listen"`
}

type authorityWirePublisher struct {
	Version            uint32
	End, CleanupBy     time.Time
	Interval, Lifetime time.Duration
	Streams            []authorityWirePublisherStream
}

type authorityWirePublisherStream struct {
	Kind, KeyFile, RootsFile string
	Initial                  []byte
	Targets, Certificates    []string
}

// This is an independent operator fixture producer. It preserves the complete
// historical authority Image and keys, and supplies the same public material
// format that an independently provisioned deployment must satisfy.
func prepareAuthorityWireSecurityMaterial(t *testing.T, dir string) {
	t.Helper()
	metadata := readAuthorityWireMaterialMetadata(t, dir)
	materialRoot := filepath.Join(dir, "identity")
	if err := os.Mkdir(materialRoot, 0700); err != nil {
		t.Fatal("create-only independent identity material", err)
	}
	if err := os.Mkdir(filepath.Join(materialRoot, "nodes"), 0700); err != nil {
		t.Fatal(err)
	}
	var publisher *authorityWirePublisher
	var public ed25519.PublicKey
	var signing ed25519.PrivateKey
	var publicPEM, dataManifest []byte
	manifest := peerOperatorManifest{Version: 1, Domain: peerOperatorDomain{NamespaceFormat: "namespaced-v1", AuthMode: "oidc", SecurityGeneration: metadata.Profile.Generation, CurrentProfile: metadata.ProfileBinding}}
	publisherRaw, err := os.ReadFile(filepath.Join(dir, "publisher.json"))
	if err == nil {
		publisher = new(authorityWirePublisher)
		if json.Unmarshal(publisherRaw, publisher) != nil || len(publisher.Streams) != 2 {
			t.Fatal("complete original operator publication required")
		}
		for _, stream := range publisher.Streams {
			if stream.Kind == "data" {
				dataManifest = stream.Initial
			}
		}
		var envelope struct {
			Payload peerOperatorManifest `json:"payload"`
		}
		if len(dataManifest) == 0 || json.Unmarshal(dataManifest, &envelope) != nil {
			t.Fatal("original signed data topology required")
		}
		manifest = envelope.Payload
		publicPEM, err = os.ReadFile(filepath.Join(dir, "data-operator.pub"))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(publicPEM)
		if block == nil {
			t.Fatal("original data verification key required")
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		var ok bool
		public, ok = key.(ed25519.PublicKey)
		if err != nil || !ok {
			t.Fatal("original data verification key invalid", err)
		}
	} else if os.IsNotExist(err) {
		public, signing, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		defer clear(signing)
		publicDER, err := x509.MarshalPKIXPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
		publicPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
		if _, err := rand.Read(manifest.Domain.Deployment[:]); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}
	operator, err := os.ReadFile(filepath.Join(dir, "control-operator.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(operator)
	operatorDigest := sha256.Sum256(operator)
	var cursor [32]byte
	if _, err := rand.Read(cursor[:]); err != nil {
		t.Fatal(err)
	}
	cdc, _ := json.Marshal(map[string]any{"enabled": true, "current_version": 1, "keys": []any{map[string]any{"version": 1, "key": hex.EncodeToString(cursor[:])}}})
	clear(cursor[:])
	type originalAuthority struct {
		Membership struct {
			Self         peerOperatorMember `json:"self"`
			ManifestFile string             `json:"manifest_file"`
		} `json:"membership"`
		Listen      string                 `json:"listen_address"`
		Floors      string                 `json:"floors_file"`
		Participant struct{ PPath string } `json:"participant"`
	}
	originals := make([]originalAuthority, len(metadata.Nodes))
	materialNodes := make([]provider.SecurityMaterialNode, len(metadata.Nodes))
	for i := range metadata.Nodes {
		name := fmt.Sprintf("node-%d", i+1)
		raw, err := os.ReadFile(filepath.Join(dir, name, "authority.json"))
		if err != nil || json.Unmarshal(raw, &originals[i]) != nil {
			t.Fatal("original authority input", err)
		}
		member := originals[i].Membership.Self
		if publisher == nil {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal("reserve independent data endpoint", err)
			}
			member.Origin = "https://" + listener.Addr().String()
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			manifest.Members = append(manifest.Members, member)
		} else {
			found := false
			for _, original := range manifest.Members {
				if original.Identity == member.Identity && original.ID == member.ID && original.SPKI == member.SPKI {
					member, found = original, true
					break
				}
			}
			if !found {
				t.Fatal("original control/data workload identity drift")
			}
		}
		materialNodes[i] = provider.SecurityMaterialNode{Name: name, ControlOrigin: originals[i].Membership.Self.Origin, DataOrigin: member.Origin}
		control, err := os.ReadFile(originals[i].Membership.ManifestFile)
		var envelope struct {
			Payload struct {
				IssuedAt  time.Time `json:"issued_at"`
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"payload"`
		}
		if err != nil || json.Unmarshal(control, &envelope) != nil {
			t.Fatal("original control manifest", err)
		}
		if i == 0 && publisher == nil {
			manifest.IssuedAt, manifest.ExpiresAt = envelope.Payload.IssuedAt, envelope.Payload.ExpiresAt
			roots, err := os.ReadFile(filepath.Join(dir, name, "roots.pem"))
			if err != nil {
				t.Fatal(err)
			}
			manifest.Domain.TrustDigest = sha256.Sum256(roots)
		} else if !manifest.IssuedAt.Equal(envelope.Payload.IssuedAt) || !manifest.ExpiresAt.Equal(envelope.Payload.ExpiresAt) {
			t.Fatal("original fixed cohort manifest interval drift")
		}
	}
	if publisher == nil {
		dataManifest = peerSecuritySign(t, manifest, signing)
	}
	peerPin, _ := json.Marshal(map[string]any{"domain": manifest.Domain, "members": manifest.Members, "verification_key": public})
	for i, original := range originals {
		name := materialNodes[i].Name
		nodeDir := filepath.Join(materialRoot, "nodes", name)
		for _, path := range []string{nodeDir, filepath.Join(nodeDir, "membership")} {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		raw, err := os.ReadFile(filepath.Join(dir, name, "authority.json"))
		var authority map[string]json.RawMessage
		if err != nil || json.Unmarshal(raw, &authority) != nil {
			t.Fatal(err)
		}
		var membership map[string]json.RawMessage
		if err := json.Unmarshal(authority["membership"], &membership); err != nil {
			t.Fatal(err)
		}
		membership["manifest_file"], _ = json.Marshal(filepath.Join(nodeDir, "membership", "control.json"))
		authority["membership"], _ = json.Marshal(membership)
		raw, _ = json.Marshal(authority)
		var peer provider.SecurityPeerMaterial
		if err := json.Unmarshal(peerPin, &peer); err != nil {
			t.Fatal(err)
		}
		state := filepath.Dir(original.Participant.PPath)
		peer.Identity, peer.State = original.Membership.Self.Identity, filepath.Join(state, "data-membership")
		peer.Certificate, peer.Key, peer.Roots = filepath.Join(nodeDir, "workload.pem"), filepath.Join(nodeDir, "workload-key.pem"), filepath.Join(nodeDir, "roots.pem")
		peer.OperatorKey, peer.Manifest = filepath.Join(nodeDir, "data-operator.pem"), filepath.Join(nodeDir, "membership", "data.json")
		document := provider.SecurityMaterial{Version: 1, Node: name, TargetPlatform: runtime.GOOS, Nodes: materialNodes, InitialAdmin: metadata.InitialAdmin,
			StatePaths: provider.SecurityMaterialStatePaths{PersistenceRoot: filepath.Join(dir, "state"), Directory: state, CustodyDirectory: filepath.Dir(original.Floors)},
			Authority:  raw, CDC: cdc, Peer: peer, SnapshotSpool: provider.GraphSnapshotSpoolConfig{Directory: filepath.Join(state, "snapshot-spool"), MaxDiskBytes: 128 << 20, MaxFrameBytes: 32 << 20}}
		documentRaw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		for source, target := range map[string]string{"cert.pem": "workload.pem", "tls.key": "workload-key.pem", "roots.pem": "roots.pem", "membership.signed": "membership/control.json"} {
			value, err := os.ReadFile(filepath.Join(dir, name, source))
			if err != nil {
				t.Fatal(err)
			}
			if err := createPrivateTestFile(filepath.Join(nodeDir, target), value); err != nil {
				t.Fatal(err)
			}
		}
		for target, value := range map[string][]byte{"identity.json": documentRaw, "data-operator.pem": publicPEM, "membership/data.json": dataManifest} {
			if err := createPrivateTestFile(filepath.Join(nodeDir, target), value); err != nil {
				t.Fatal(err)
			}
		}
		complete, err := provider.SecurityMaterialCompletion(documentRaw, operatorDigest, operator)
		if err != nil {
			t.Fatal(err)
		}
		if err := createPrivateTestFile(filepath.Join(nodeDir, "COMPLETE"), complete); err != nil {
			t.Fatal(err)
		}
	}
	if publisher != nil {
		for i := range publisher.Streams {
			stream := &publisher.Streams[i]
			if stream.Kind != "control" && stream.Kind != "data" || len(stream.Targets) != len(materialNodes) || len(stream.Certificates) != len(materialNodes) {
				t.Fatal("original operator topology mismatch")
			}
			for j, node := range materialNodes {
				dir := filepath.Join(materialRoot, "nodes", node.Name)
				stream.Targets[j], stream.Certificates[j] = filepath.Join(dir, "membership", stream.Kind+".json"), filepath.Join(dir, "workload.pem")
			}
			stream.RootsFile = filepath.Join(materialRoot, "nodes", materialNodes[0].Name, "roots.pem")
		}
		updated, err := json.Marshal(publisher)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "publisher.json"), updated, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func readAuthorityWireMaterialMetadata(t *testing.T, dir string) authorityWireMaterialMetadata {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "provision.json"))
	var metadata authorityWireMaterialMetadata
	if err != nil || json.Unmarshal(raw, &metadata) != nil || len(metadata.Nodes) == 0 || metadata.ProfileBinding == "" || metadata.InitialAdmin.Issuer == "" || metadata.InitialAdmin.Subject == "" {
		t.Fatal("complete independent original material metadata required", err)
	}
	return metadata
}

func loadAuthorityWireSecurityConfig(t *testing.T, dir string, node int, providerURL, rootCAPEM string, configure ...func(map[string]any)) provider.SecurityConfig {
	t.Helper()
	metadata := readAuthorityWireMaterialMetadata(t, dir)
	name := fmt.Sprintf("node-%d", node)
	materialRoot := filepath.Join(dir, "identity")
	raw, err := os.ReadFile(filepath.Join(materialRoot, "nodes", name, "identity.json"))
	var document provider.SecurityMaterial
	if err != nil || json.Unmarshal(raw, &document) != nil || len(document.Nodes) != len(metadata.Nodes) {
		t.Fatal("complete original identity material required", err)
	}
	if !strings.Contains(rootCAPEM, "-----BEGIN CERTIFICATE-----") {
		value, err := os.ReadFile(rootCAPEM)
		if err != nil {
			t.Fatal(err)
		}
		rootCAPEM = string(value)
	}
	nodes := make([]authorityWireMaterialNode, len(document.Nodes))
	for i, endpoint := range document.Nodes {
		raw, err := os.ReadFile(filepath.Join(dir, endpoint.Name, "authority.json"))
		var original struct {
			Listen string `json:"listen_address"`
		}
		if err != nil || json.Unmarshal(raw, &original) != nil {
			t.Fatal(err)
		}
		data, err := url.Parse(endpoint.DataOrigin)
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = authorityWireMaterialNode{Name: endpoint.Name, ControlOrigin: endpoint.ControlOrigin, DataOrigin: endpoint.DataOrigin, ControlListen: original.Listen, DataListen: data.Host}
	}
	input := map[string]any{"public_origin": "https://admin.example", "material_root": materialRoot, "persistence_root": document.StatePaths.PersistenceRoot, "node": name, "target_platform": runtime.GOOS,
		"tls_termination": "proxy", "trusted_proxy_ips": []string{"127.0.0.1"}, "initial_admin": metadata.InitialAdmin, "nodes": nodes, "issuers": []any{}, "root_ca_pem": rootCAPEM,
		"private_origins": map[string][]string{providerURL: {"127.0.0.1/32"}}}
	for _, apply := range configure {
		if apply != nil {
			apply(input)
		}
	}
	current, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	config, err := provider.LoadSecurityConfigJSON(string(current))
	if err != nil {
		t.Fatal("same-input independent material validation", err)
	}
	return config
}
