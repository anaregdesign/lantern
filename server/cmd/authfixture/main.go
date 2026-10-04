// Command authfixture generates isolated, short-lived local conformance trust.
// It can supervise a production Server; it does not bypass clock fences or qualify a provider.
package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"

	"github.com/anaregdesign/lantern/core/privatefile"
)

type fixtureNode struct {
	Name         string            `json:"name"`
	PublicOrigin string            `json:"public_origin"`
	PeerOrigin   string            `json:"peer_origin"`
	Environment  map[string]string `json:"environment"`
}
type fixture struct {
	Nodes               []fixtureNode `json:"nodes"`
	CAFile              string        `json:"ca_file"`
	TokenFile           string        `json:"token_file"`
	HeadWriterTokenFile string        `json:"head_writer_token_file,omitempty"`
	ClientCertFile      string        `json:"client_cert_file"`
	ClientKeyFile       string        `json:"client_key_file"`
}

func main() {
	privateInput := flag.String("private-input", "", "write one bounded stdin fixture input into a new private file")
	directory := flag.String("directory", "", "new private local fixture directory")
	compose := flag.Bool("compose", false, "generate the fixed three-node Compose benchmark topology")
	renew := flag.Bool("renew", false, "renew only the existing operator-signed Compose membership")
	restartNode := flag.String("restart-node", "", "explicitly prepare one owned Compose workload for restart")
	renewEvery := flag.Duration("renew-every", 0, "optional owned renewal interval (30s through 2m)")
	publicPorts := flag.String("public-ports", "", "comma-separated public loopback ports")
	peerPorts := flag.String("peer-ports", "", "comma-separated private loopback ports; empty disables HA")
	mode := flag.String("mode", "oidc", "off or oidc")
	tokensFile := flag.String("tokens-file", "", "optional private JSON array of canonical machine tokens")
	serverBinary := flag.String("serve", "", "supervise the production Server binary until shutdown")
	overridesFile := flag.String("overrides-file", "", "optional per-node JSON configuration overrides")
	publicMTLS := flag.Bool("public-mtls", false, "require a separate local client certificate on public listeners")
	receiptHA := flag.Bool("receipt-ha", false, "four-node native receipt HA with a separate policy writer and controlled data relay")
	edgeCreate := flag.Bool("edge-create", false, "qualify standalone existing-endpoint Create under Vertex-derived Head authority")
	headEdge := flag.Bool("head-edge", false, "qualify standalone Head write-only handling with a separate machine Role")
	transportProbe := flag.Bool("transport-probe", false, "standalone scoped transport probe with a localhost-only certificate")
	receipt := flag.Bool("receipt", false, "enable native receipt WAL for each OIDC node")
	readyTimeout := flag.Duration("ready-timeout", time.Minute, "bounded verified-TLS production readiness wait")
	flag.Parse()
	if *privateInput != "" {
		valid := flag.NArg() == 0
		flag.Visit(func(value *flag.Flag) {
			if value.Name != "private-input" {
				valid = false
			}
		})
		if !valid || writePrivateInput(*privateInput, os.Stdin) != nil {
			fmt.Fprintln(os.Stderr, "authfixture: private_input_failed")
			os.Exit(1)
		}
		return
	}
	if *restartNode != "" {
		if !*compose || *directory == "" || *renew || *renewEvery != 0 || *publicPorts != "" || *peerPorts != "" || *serverBinary != "" || *tokensFile != "" || *publicMTLS || *receipt || *receiptHA || *edgeCreate || *headEdge || *transportProbe || *overridesFile != "" || flag.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "authfixture: restart requires only -compose -restart-node -directory")
			os.Exit(1)
		}
		if err := prepareComposeRestart(*directory, *restartNode); err != nil {
			fmt.Fprintln(os.Stderr, "authfixture: explicit workload restart preparation failed")
			os.Exit(1)
		}
		return
	}
	if *renew {
		if !*compose || *directory == "" || *publicPorts != "" || *peerPorts != "" || *serverBinary != "" || *tokensFile != "" || *publicMTLS || *receipt || *receiptHA || *edgeCreate || *headEdge || *transportProbe || *overridesFile != "" || flag.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "authfixture: renewal requires only -compose -renew -directory")
			os.Exit(1)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := runComposeRenewal(ctx, *directory, *renewEvery); err != nil {
			fmt.Fprintln(os.Stderr, "authfixture: membership renewal failed")
			os.Exit(1)
		}
		return
	}
	public, err := ports(*publicPorts)
	if *renewEvery != 0 {
		err = errors.New("renew-every requires -compose -renew")
	}
	if err == nil && len(public) == 0 {
		err = errors.New("public ports required")
	}
	var peer []int
	if err == nil && *peerPorts != "" {
		peer, err = ports(*peerPorts)
	}
	if err == nil && len(peer) != 0 && len(peer) != len(public) {
		err = errors.New("peer/public port count differs")
	}
	if err == nil && *compose {
		if len(public) != 3 || len(peer) != 0 || *serverBinary != "" || *publicMTLS || *overridesFile != "" || flag.NArg() != 0 {
			err = errors.New("Compose requires three public ports and no local peer ports/supervision/overrides")
		} else {
			peer = []int{6381, 6382, 6383}
		}
	}
	if err == nil && len(public) > 1 && len(peer) == 0 {
		err = errors.New("multiple nodes require the private workload plane")
	}
	if err == nil && *receiptHA && (*compose || *mode != "oidc" || !*receipt || len(public) != 4 || len(peer) != 4 || *serverBinary == "" || *publicMTLS || *overridesFile != "") {
		err = errors.New("receipt-ha requires four OIDC public/private ports, receipts and production supervision")
	}
	if err == nil && *edgeCreate && (*compose || *mode != "oidc" || len(public) != 1 || len(peer) != 0 || *serverBinary == "") {
		err = errors.New("edge-create requires one supervised standalone OIDC node")
	}
	if err == nil && *headEdge && (*compose || *mode != "oidc" || !*receipt || len(public) != 1 || len(peer) != 0 || *serverBinary == "" || *edgeCreate) {
		err = errors.New("head-edge requires one supervised standalone OIDC receipt node")
	}
	if err == nil && *transportProbe && (*compose || *mode != "oidc" || len(public) != 1 || len(peer) != 0 || *serverBinary == "" || *publicMTLS || *receipt || *edgeCreate || *headEdge || *overridesFile != "") {
		err = errors.New("transport probe requires one supervised standalone OIDC node")
	}
	var result fixture
	if err == nil {
		result, err = generateTopologyProfile(*directory, public, peer, *mode, *tokensFile, *compose, *transportProbe)
	}
	if err == nil && *publicMTLS {
		for i := range result.Nodes {
			result.Nodes[i].Environment["LANTERN_TLS_CLIENT_CA_FILE"] = result.CAFile
		}
	}
	if err == nil && *receipt {
		err = addFixtureReceipts(&result, *directory)
	}
	if err == nil && *edgeCreate {
		err = addFixtureEdgeCreate(&result)
	}
	if err == nil && *headEdge {
		err = addFixtureHeadEdge(&result, *directory)
	}
	if err == nil && *compose {
		err = exportComposeFixture(&result, *directory)
	}
	if err == nil && *serverBinary != "" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if *receiptHA {
			err = serveReceiptHAFixture(ctx, result, *serverBinary, *directory, os.Stdin, os.Stdout, *readyTimeout)
		} else {
			err = serveFixture(ctx, result, *serverBinary, *directory, *overridesFile, os.Stdin, os.Stdout, *readyTimeout)
		}
		if err == nil {
			return
		}
	}
	if err != nil {
		if *serverBinary != "" {
			fmt.Fprintln(os.Stderr, "authfixture_failure:"+fixtureFailureCategory(err))
		} else {
			fmt.Fprintln(os.Stderr, "authfixture:", err)
		}
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}
func ports(raw string) ([]int, error) {
	if raw == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	var result []int
	for _, part := range strings.Split(raw, ",") {
		number, err := strconv.Atoi(part)
		if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != part || seen[number] {
			return nil, errors.New("ports must be distinct canonical numbers")
		}
		seen[number] = true
		result = append(result, number)
	}
	if len(result) > 8 {
		return nil, errors.New("at most eight fixture nodes")
	}
	return result, nil
}
func writeFile(dir, name string, raw []byte) (string, error) {
	path := filepath.Join(dir, name)
	file, err := privatefile.Create(path, os.O_WRONLY)
	if err != nil {
		return "", err
	}
	_, err = file.Write(raw)
	err = errors.Join(err, file.Close())
	return path, err
}
func writeJSON(dir, name string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return writeFile(dir, name, raw)
}
func writeKey(dir, name string, key any, private bool) (string, error) {
	var raw []byte
	var err error
	kind := "PUBLIC KEY"
	if private {
		kind = "PRIVATE KEY"
		raw, err = x509.MarshalPKCS8PrivateKey(key)
	} else {
		raw, err = x509.MarshalPKIXPublicKey(key)
	}
	if err != nil {
		return "", err
	}
	return writeFile(dir, name, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: raw}))
}
func certificate(dir, name, identity string, ca *x509.Certificate, signer crypto.Signer, dns ...string) (string, string, [32]byte, error) {
	return certificateWithAddresses(dir, name, identity, ca, signer, []net.IP{net.ParseIP("127.0.0.1")}, dns...)
}

func certificateWithAddresses(dir, name, identity string, ca *x509.Certificate, signer crypto.Signer, addresses []net.IP, dns ...string) (string, string, [32]byte, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", [32]byte{}, err
	}
	public := &private.PublicKey
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"localhost"}, IPAddresses: addresses, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	template.DNSNames = append(template.DNSNames, dns...)
	if identity != "" {
		uri, err := url.Parse(identity)
		if err != nil {
			return "", "", [32]byte{}, err
		}
		template.URIs = []*url.URL{uri}
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, ca, public, signer)
	if err != nil {
		return "", "", [32]byte{}, err
	}
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return "", "", [32]byte{}, err
	}
	certFile, err := writeFile(dir, name+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}))
	if err != nil {
		return "", "", [32]byte{}, err
	}
	keyFile, err := writeKey(dir, name+".key", private, true)
	return certFile, keyFile, sha256.Sum256(cert.RawSubjectPublicKeyInfo), err
}
func randomID() ([16]byte, error) {
	var result [16]byte
	_, err := rand.Read(result[:])
	return result, err
}
func generate(dir string, publicPorts, peerPorts []int, mode, tokensFile string) (fixture, error) {
	return generateTopology(dir, publicPorts, peerPorts, mode, tokensFile, false)
}
func generateTopology(dir string, publicPorts, peerPorts []int, mode, tokensFile string, compose bool) (fixture, error) {
	return generateTopologyProfile(dir, publicPorts, peerPorts, mode, tokensFile, compose, false)
}

func generateTopologyProfile(dir string, publicPorts, peerPorts []int, mode, tokensFile string, compose, transportProbe bool) (fixture, error) {
	if !filepath.IsAbs(dir) || mode != "oidc" && mode != "off" || compose && (len(publicPorts) != 3 || len(peerPorts) != 3) {
		return fixture{}, errors.New("absolute fixture directory and exact mode required")
	}
	if transportProbe && (mode != "oidc" || compose || len(publicPorts) != 1 || len(peerPorts) != 0) {
		return fixture{}, errors.New("transport probe requires standalone OIDC")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return fixture{}, errors.New("fixture directory must be new")
	}
	now := time.Now().UTC()
	caPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fixture{}, err
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Lantern local conformance CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour)}
	caRaw, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPrivate.PublicKey, caPrivate)
	if err != nil {
		return fixture{}, err
	}
	ca, err := x509.ParseCertificate(caRaw)
	if err != nil {
		return fixture{}, err
	}
	caFile, err := writeFile(dir, "ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caRaw}))
	if err != nil {
		return fixture{}, err
	}
	result := fixture{CAFile: caFile}
	result.ClientCertFile, result.ClientKeyFile, _, err = certificate(dir, "public-client", "", ca, caPrivate)
	if err != nil {
		return fixture{}, err
	}
	issuer := "https://fixture-idp.invalid"
	roles := fixtureRoles()
	if transportProbe {
		roles = transportProbeRoles()
	}
	var securityEnv map[string]string
	var writerPublic ed25519.PublicKey
	var generation [16]byte
	if mode == "oidc" {
		var writerPrivate ed25519.PrivateKey
		writerPublic, writerPrivate, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fixture{}, err
		}
		privatePath, err := writeKey(dir, "writer.key", writerPrivate, true)
		if err != nil {
			return fixture{}, err
		}
		publicPath, err := writeKey(dir, "writer.pub", writerPublic, false)
		if err != nil {
			return fixture{}, err
		}
		generation, err = randomID()
		if err != nil {
			return fixture{}, err
		}
		roleJSON, err := json.Marshal(roles)
		if err != nil {
			return fixture{}, err
		}
		securityEnv = map[string]string{"LANTERN_AUTH_MODE": "oidc", "LANTERN_SECURITY_STORE_MODE": "fresh", "LANTERN_SECURITY_GENERATION": hex.EncodeToString(generation[:]), "LANTERN_SECURITY_WRITER_KEY_FILE": privatePath, "LANTERN_SECURITY_WRITER_PUBLIC_KEY_FILE": publicPath, "LANTERN_SECURITY_BOOTSTRAP_REVISION": "1", "LANTERN_SECURITY_BOOTSTRAP_ROLES": string(roleJSON), "LANTERN_SECURITY_CLOCK_QUALIFIED": "true", "LANTERN_OIDC_ADMIN_ISSUER": issuer, "LANTERN_OIDC_ADMIN_SUBJECTS": `["fixture-admin"]`, "LANTERN_OIDC_CLIENT_ID": "fixture-admin", "LANTERN_OIDC_API_AUDIENCE": "lantern-fixture", "LANTERN_OIDC_ALGORITHMS": `["EdDSA"]`}
		var tokens []string
		if tokensFile != "" {
			raw, err := os.ReadFile(tokensFile)
			if err != nil {
				return fixture{}, err
			}
			if err = json.Unmarshal(raw, &tokens); err != nil {
				return fixture{}, err
			}
		} else {
			var raw [32]byte
			if _, err = rand.Read(raw[:]); err != nil {
				return fixture{}, err
			}
			tokens = []string{"lnt_m1_" + base64.RawURLEncoding.EncodeToString(raw[:])}
		}
		if len(tokens) == 0 || len(tokens) > 4 {
			return fixture{}, errors.New("one to four machine credentials required")
		}
		machine := map[string]any{"name": "fixture-client", "role_ids": []string{"fixture_data"}}
		var credentials []map[string]any
		for _, token := range tokens {
			if !strings.HasPrefix(token, "lnt_m1_") {
				return fixture{}, errors.New("canonical lnt_m1_ fixture credentials required")
			}
			raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(token, "lnt_m1_"))
			if err != nil || len(raw) != 32 {
				return fixture{}, errors.New("canonical machine credential required")
			}
			credentials = append(credentials, map[string]any{"token": token, "created_at": now.Add(-time.Minute), "expires_at": now.Add(time.Hour)})
		}
		machine["credentials"] = credentials
		machineFile, err := writeJSON(dir, "machines.json", []map[string]any{machine})
		if err != nil {
			return fixture{}, err
		}
		securityEnv["LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE"] = machineFile
		result.TokenFile, err = writeJSON(dir, "tokens.json", tokens)
		if err != nil {
			return fixture{}, err
		}
	}
	var cursorKey [32]byte
	if _, err := rand.Read(cursorKey[:]); err != nil {
		return fixture{}, err
	}
	cursorFile, err := writeJSON(dir, "cdc-keys.json", map[string]any{"current_version": 1, "keys": []any{map[string]any{"version": 1, "key": hex.EncodeToString(cursorKey[:])}}})
	if err != nil {
		return fixture{}, err
	}
	deployment, err := randomID()
	if err != nil {
		return fixture{}, err
	}
	operatorPublic, operatorPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fixture{}, err
	}
	if compose {
		if _, err := writeKey(dir, "operator.key", operatorPrivate, true); err != nil {
			return fixture{}, err
		}
	}
	operatorFile, err := writeKey(dir, "operator.pub", operatorPublic, false)
	if err != nil {
		return fixture{}, err
	}
	domain := peerauth.Domain{Deployment: deployment, NamespaceFormat: keyspace.Version, AuthMode: mode, SecurityGeneration: generation, TrustDigest: sha256.Sum256(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caRaw}))}
	copy(domain.WriterPublicKey[:], writerPublic)
	membership := peerauth.Manifest{Version: 1, Domain: domain, IssuedAt: now, ExpiresAt: now.Add(peerauth.MaxMembershipLifetime)}
	for index, port := range publicPorts {
		name := fmt.Sprintf("node-%d", index)
		publicOrigin := "https://localhost:" + strconv.Itoa(port)
		serviceName := "localhost"
		if compose {
			serviceName = fmt.Sprintf("lantern-%d", index)
		}
		var certFile, keyFile string
		if transportProbe {
			certFile, keyFile, _, err = certificateWithAddresses(dir, name+"-public", "", ca, caPrivate, nil)
		} else {
			certFile, keyFile, _, err = certificate(dir, name+"-public", "", ca, caPrivate, serviceName)
		}
		if err != nil {
			return fixture{}, err
		}
		env := map[string]string{"LANTERN_PORT": strconv.Itoa(port), "LANTERN_TLS_CERT_FILE": certFile, "LANTERN_TLS_KEY_FILE": keyFile, "LANTERN_METRICS_ADDR": "", "LANTERN_LOG_LEVEL": "warn", "LANTERN_CDC_CURSOR_KEY_RING_FILE": cursorFile}
		nodeID, err := randomID()
		if err != nil {
			return fixture{}, err
		}
		env["LANTERN_NODE_ID"] = hex.EncodeToString(nodeID[:])
		node := fixtureNode{Name: name, PublicOrigin: publicOrigin, Environment: env}
		if mode == "oidc" {
			for key, value := range securityEnv {
				env[key] = value
			}
			env["LANTERN_SECURITY_STORE_PATH"] = filepath.Join(dir, name+"-security.wal")
			env["LANTERN_SECURITY_NODE_ROLE"] = "writer"
			env["LANTERN_SECURITY_WRITER_ENDPOINT"] = publicOrigin
			env["LANTERN_OIDC_BROWSER_ORIGIN"] = publicOrigin
			env["LANTERN_OIDC_REDIRECT_URI"] = publicOrigin + oidc.CallbackPath(issuer)
			if index > 0 {
				env["LANTERN_SECURITY_NODE_ROLE"] = "replica"
				delete(env, "LANTERN_SECURITY_WRITER_KEY_FILE")
				delete(env, "LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE")
			}
		}
		if len(peerPorts) > 0 {
			identity := "spiffe://lantern.fixture/" + name
			peerCert, peerKey, spki, err := certificate(dir, name+"-peer", identity, ca, caPrivate, serviceName)
			if err != nil {
				return fixture{}, err
			}
			memberID, err := randomID()
			if err != nil {
				return fixture{}, err
			}
			node.PeerOrigin = "https://localhost:" + strconv.Itoa(peerPorts[index])
			if compose {
				node.PeerOrigin = "https://" + serviceName + ":6381"
			}
			membership.Members = append(membership.Members, peerauth.Member{ID: memberID, Identity: identity, SPKI: spki, Origin: node.PeerOrigin})
			env["LANTERN_PEER_LISTEN_ADDR"] = "127.0.0.1:" + strconv.Itoa(peerPorts[index])
			env["LANTERN_PEER_DEPLOYMENT"] = hex.EncodeToString(deployment[:])
			env["LANTERN_PEER_MEMBERSHIP_MODE"] = "fresh"
			env["LANTERN_PEER_MEMBERSHIP_FILE"] = filepath.Join(dir, "membership.json")
			env["LANTERN_PEER_MEMBERSHIP_STATE_FILE"] = filepath.Join(dir, name+"-membership.state")
			env["LANTERN_PEER_OPERATOR_PUBLIC_KEY_FILE"] = operatorFile
			env["LANTERN_PEER_WORKLOAD_ID"] = identity
			env["LANTERN_PEER_CERT_FILE"] = peerCert
			env["LANTERN_PEER_KEY_FILE"] = peerKey
			env["LANTERN_PEER_TRUST_CA_FILE"] = caFile
			if mode == "oidc" {
				env["LANTERN_SECURITY_WRITER_ENDPOINT"] = "https://localhost:" + strconv.Itoa(peerPorts[0])
				if compose {
					env["LANTERN_SECURITY_WRITER_ENDPOINT"] = "https://lantern-0:6381"
				}
			}
		}
		result.Nodes = append(result.Nodes, node)
	}
	if len(peerPorts) > 0 {
		signed, err := peerauth.SignManifest(membership, operatorPrivate)
		if err != nil {
			return fixture{}, err
		}
		if _, err = writeFile(dir, "membership.json", signed); err != nil {
			return fixture{}, err
		}
	}
	return result, nil
}
func fixtureRoles() []security.Role {
	prefix := ""
	role := security.Role{ID: "fixture_data", Name: "Local conformance data client"}
	for _, action := range []security.Action{security.VertexRead, security.VertexWrite, security.VertexDelete, security.Query, security.CDCIdentity, security.CDCValue, security.Export, security.ReceiptRead} {
		role.Rules = append(role.Rules, security.PermissionRule{ID: strings.ReplaceAll(string(action), ".", "_"), Action: action, Effect: security.Allow, Resource: security.DataResource, Prefix: &prefix})
	}
	for _, action := range []security.Action{security.OperationsRead, security.SchemaRead} {
		role.Rules = append(role.Rules, security.PermissionRule{ID: strings.ReplaceAll(string(action), ".", "_"), Action: action, Effect: security.Allow, Resource: security.GlobalResource})
	}
	return []security.Role{role}
}
