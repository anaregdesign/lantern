package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func inlineSecurityExample() string {
	return `{"public_origin":"https://admin.example","persistence_root":"/var/lib/lantern","target_platform":"linux","tls_termination":"proxy","trusted_proxy_ips":["127.0.0.1"],"initial_admin":{"issuer":"google","subject":"initial-admin"},"nodes":[{"name":"node-1","control_origin":"https://node-1:6381","data_origin":"https://node-1:6382","control_listen":":6381","data_listen":":6382"}],"issuers":[{"id":"google_web","issuer":"google","client_id":"web-client","client_secret":"private-example"}]}`
}

func runtimeSecurityReference(path string) string {
	raw, _ := json.Marshal(struct {
		Path string `json:"provisioned_file"`
	}{filepath.Clean(path)})
	return string(raw)
}

func provisioningTestOptions(t *testing.T) SecurityProvisionOptions {
	t.Helper()
	clearSecurityEnvironment(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	var setup securitySetupJSON
	if err := json.Unmarshal([]byte(inlineSecurityExample()), &setup); err != nil {
		t.Fatal(err)
	}
	setup.PersistenceRoot = state
	raw, _ := json.Marshal(setup)
	t.Setenv("LANTERN_AUTH_MODE", "oidc")
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", string(raw))
	return SecurityProvisionOptions{OutputDirectory: filepath.Join(root, "provisioned"), OperatorDirectory: filepath.Join(root, "operator"), ValidFor: time.Hour}
}
