package security

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func provisioningTestOptions(t *testing.T, count int, target string) ProvisioningOptions {
	t.Helper()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	options := ProvisioningOptions{Target: target, Now: now, End: now.Add(time.Hour), CleanupBy: now.Add(2 * time.Hour), Image: s1Image()}
	dir := t.TempDir()
	for i := range count {
		name := fmt.Sprintf("node-%02d", i+1)
		options.Nodes = append(options.Nodes, ProvisioningNode{Name: name, ControlOrigin: fmt.Sprintf("https://%s:16380", name),
			DataOrigin: fmt.Sprintf("https://%s:16381", name), ControlListen: "0.0.0.0:16380",
			StateDir: filepath.Join(dir, name, "state"), RenewableDir: filepath.Join(dir, name, "membership")})
	}
	return options
}

func provisioningTestDocument(t *testing.T, raw []byte) currentJSONProvisioningDocument {
	t.Helper()
	var document currentJSONProvisioningDocument
	if err := decodeCurrentJSONProvisioning(raw, &document); err != nil {
		t.Fatal("generated immutable document is invalid", err)
	}
	return document
}
