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
	nodeRaw, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal("read original node identity", err)
	}
	var d securityMaterialDocument
	if err := json.Unmarshal(nodeRaw, &d); err != nil {
		t.Fatal("decode original node identity", err)
	}
	if err := verifySecurityCompletion(dir, nodeRaw, d.Authority); err != nil {
		t.Fatal("valid completion rejected", err)
	}
	markerPath := filepath.Join(dir, "COMPLETE")
	original, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal("read original completion marker", err)
	}
	for _, field := range []string{"node", "operator", "signature"} {
		t.Run(field, func(t *testing.T) {
			var marker securityCompletion
			if err := json.Unmarshal(original, &marker); err != nil {
				t.Fatal("decode original completion marker", err)
			}
			switch field {
			case "node":
				marker.NodeSHA256 = sha256.Sum256([]byte("different node"))
			case "operator":
				marker.OperatorSHA256 = sha256.Sum256([]byte("different custody"))
			case "signature":
				marker.Signature[0] ^= 1
			}
			changed, err := json.Marshal(marker)
			if err != nil {
				t.Fatal("encode forged completion marker", err)
			}
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
