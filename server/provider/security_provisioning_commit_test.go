package provider

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityCompletionCannotBeForgedForDifferentNodeOrOperator(t *testing.T) {
	options := provisioningTestOptions(t)
	if err := ProvisionSecurity(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(provisioningTestMaterialRoot(t), "nodes", "node-1")
	nodeRaw, _ := os.ReadFile(filepath.Join(dir, "identity.json"))
	var d securityMaterialDocument
	json.Unmarshal(nodeRaw, &d)
	if err := verifySecurityCompletion(dir, nodeRaw, d.Authority); err != nil {
		t.Fatal("valid completion rejected", err)
	}
	markerPath := filepath.Join(dir, "COMPLETE")
	original, _ := os.ReadFile(markerPath)
	for _, field := range []string{"node", "operator", "signature"} {
		t.Run(field, func(t *testing.T) {
			var marker securityCompletion
			json.Unmarshal(original, &marker)
			switch field {
			case "node":
				marker.NodeSHA256 = sha256.Sum256([]byte("different node"))
			case "operator":
				marker.OperatorSHA256 = sha256.Sum256([]byte("different custody"))
			case "signature":
				marker.Signature[0] ^= 1
			}
			changed, _ := json.Marshal(marker)
			if err := os.WriteFile(markerPath, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if err := verifySecurityCompletion(dir, nodeRaw, d.Authority); err == nil {
				t.Fatal("forged completion accepted")
			}
		})
	}
	if err := os.WriteFile(markerPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySecurityCompletion(dir, append(nodeRaw, ' '), d.Authority); err == nil {
		t.Fatal("completion ignored node bytes")
	}
}
