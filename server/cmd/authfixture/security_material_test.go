package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureAuthorityPathResolutionPreservesHistoricalTrust(t *testing.T) {
	original := []byte(`{"genesis":{"original":"historical arbitrary policy"},"identity":{"tls_key_pem":"private"},"participant":{"PPath":"/old/p","BPath":"/old/b","Member":3,"OwnedOrigin":0},"membership":{"path":"/old/m","manifest_file":"/old/signed","public_key":"original"},"floors_file":"/old/floors","limits":{"old":1}}`)
	resolved, err := fixtureAuthorityPaths(original, "/state/node-3", "/material/nodes/node-3")
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if json.Unmarshal(original, &before) != nil || json.Unmarshal(resolved, &after) != nil {
		t.Fatal("invalid result")
	}
	for _, key := range []string{"genesis", "identity", "limits"} {
		var a, b any
		_ = json.Unmarshal(before[key], &a)
		_ = json.Unmarshal(after[key], &b)
		ar, _ := json.Marshal(a)
		br, _ := json.Marshal(b)
		if !bytes.Equal(ar, br) {
			t.Fatal("historical original changed", key)
		}
	}
	var participant struct {
		PPath, BPath        string
		Member, OwnedOrigin uint32
	}
	_ = json.Unmarshal(after["participant"], &participant)
	if participant.PPath != "/state/node-3/control-protocol.wal" || participant.BPath != "/state/node-3/control-materialized.wal" || participant.Member != 3 || participant.OwnedOrigin != 0 {
		t.Fatal("state path resolution changed origin-less observer")
	}
}

func TestFixtureEnvironmentRequiresAndReadsCurrentStartupEveryProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.json")
	node := fixtureNode{StartupFile: path, Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}
	if _, err := fixtureEnvironment(node, nil); err == nil {
		t.Fatal("missing current startup ignored")
	}
	for _, raw := range []string{`{"issuers":[]}`, `{"issuers":[{"issuer":"https://new.example"}]}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		env, err := fixtureEnvironment(node, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range env {
			if entry == "LANTERN_SECURITY_CONFIG_JSON="+raw {
				found = true
			}
		}
		if !found {
			t.Fatal("current supplied JSON not consumed")
		}
	}
	encoded, err := json.Marshal(node)
	if err != nil || bytes.Contains(encoded, []byte("https://new.example")) {
		t.Fatal("private startup leaked through metadata", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureEnvironment(node, nil); err == nil {
		t.Fatal("empty startup ignored")
	}
}
