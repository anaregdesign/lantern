package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/privatefile"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestTransportProbeScopeAndHostname(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "probe")
	result, err := generateTopologyProfile(dir, []int{16434}, nil, "oidc", "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	node := result.Nodes[0]
	certRaw, err := os.ReadFile(node.Environment["LANTERN_TLS_CERT_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certRaw)
	if block == nil {
		t.Fatal("missing public certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || cert.VerifyHostname("localhost") != nil || cert.VerifyHostname("127.0.0.1") == nil {
		t.Fatal("probe must verify localhost and reject the wrong IP hostname", err)
	}
	var roles []security.Role
	if err := json.Unmarshal([]byte(node.Environment["LANTERN_SECURITY_BOOTSTRAP_ROLES"]), &roles); err != nil {
		t.Fatal(err)
	}
	compiled, err := security.CompileRoles(roles, security.DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, err := compiled.ForRoles([]string{"fixture_data"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"probe/connect/value", "probe/grpc/ios-success/value"} {
		for _, action := range []security.Action{security.VertexRead, security.VertexWrite, security.Export} {
			if !access.Allows(action, key) {
				t.Fatal("required probe action denied", action)
			}
		}
		for _, action := range []security.Action{security.VertexDelete, security.Query, security.CDCValue, security.ReceiptRead} {
			if access.Allows(action, key) {
				t.Fatal("probe has unrelated capability", action)
			}
		}
	}
	if access.Allows(security.VertexRead, "probe/connect-other/value") || access.Allows(security.VertexWrite, "other/value") || access.AllowsGlobal(security.SecurityManage) {
		t.Fatal("probe escaped its literal prefixes or acquired administration")
	}
	file, err := os.Open(result.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := privatefile.Check(file); err != nil {
		t.Fatal(err)
	}
	tokens, err := os.ReadFile(result.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	var tokenValues []string
	if err := json.Unmarshal(tokens, &tokenValues); err != nil || len(tokenValues) != 1 {
		t.Fatal("machine credential missing", err)
	}
	metadata, err := json.Marshal(result)
	if err != nil || bytes.Contains(metadata, []byte(tokenValues[0])) {
		t.Fatal("machine credential leaked in metadata", err)
	}
	for _, mode := range []string{"off", "invalid"} {
		if _, err := generateTopologyProfile(filepath.Join(t.TempDir(), "invalid"), []int{16434}, nil, mode, "", false, true); err == nil {
			t.Fatal("probe accepted a non-OIDC configuration")
		}
	}
	if _, err := generateTopologyProfile(filepath.Join(t.TempDir(), "peer"), []int{16434}, []int{16435}, "oidc", "", false, true); err == nil {
		t.Fatal("probe accepted private replication")
	}
}
