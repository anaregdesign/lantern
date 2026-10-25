package provider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"path/filepath"

	"github.com/anaregdesign/lantern/server/internal/oidc"
)

const securityCompletionDomain = "lantern.security.provisioning.complete.v1\x00"

type securityCompletion struct {
	Version        uint32   `json:"version"`
	NodeSHA256     [32]byte `json:"node_sha256"`
	OperatorSHA256 [32]byte `json:"operator_sha256"`
	Signature      []byte   `json:"signature"`
}

func securityCompletionBytes(nodeRaw []byte, operatorDigest [32]byte, keyPEM []byte) ([]byte, error) {
	block, rest := pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 || operatorDigest == [32]byte{} {
		return nil, errSecurityProvisioning
	}
	defer clear(block.Bytes)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	private, ok := key.(ed25519.PrivateKey)
	if err != nil || !ok {
		return nil, errSecurityProvisioning
	}
	defer clear(private)
	completion := securityCompletion{Version: 1, NodeSHA256: sha256.Sum256(nodeRaw), OperatorSHA256: operatorDigest}
	payload, _ := json.Marshal(completion)
	completion.Signature = ed25519.Sign(private, append([]byte(securityCompletionDomain), payload...))
	raw, err := json.Marshal(completion)
	if err != nil {
		return nil, errSecurityProvisioning
	}
	return raw, nil
}

// Completion is signed only after every serving file and operator custody file
// is durable. A node never accepts a merely half-written provisioning bundle.
func writeSecurityCompletion(dir string, nodeRaw []byte, operatorDigest [32]byte, keyPEM []byte) error {
	raw, err := securityCompletionBytes(nodeRaw, operatorDigest, keyPEM)
	if err != nil {
		return err
	}
	return writePrivateNewFile(filepath.Join(dir, "COMPLETE"), raw)
}

func verifySecurityCompletion(dir string, nodeRaw, authority []byte) error {
	raw, err := readSecurityOperatorFile(filepath.Join(dir, "COMPLETE"), true, 4096)
	if err != nil {
		return errSecurityProvisioning
	}
	var completion securityCompletion
	if decodeSecurityConfigJSON(string(raw), &completion, oidc.ValidateJSON) != nil || completion.Version != 1 || completion.NodeSHA256 != sha256.Sum256(nodeRaw) || completion.OperatorSHA256 == [32]byte{} {
		return errSecurityProvisioning
	}
	var keyDocument struct {
		Membership struct {
			PublicKey []byte `json:"public_key"`
		} `json:"membership"`
	}
	if json.Unmarshal(authority, &keyDocument) != nil || len(keyDocument.Membership.PublicKey) != ed25519.PublicKeySize {
		return errSecurityProvisioning
	}
	signature := completion.Signature
	completion.Signature = nil
	payload, _ := json.Marshal(completion)
	if !ed25519.Verify(keyDocument.Membership.PublicKey, append([]byte(securityCompletionDomain), payload...), signature) {
		return errSecurityProvisioning
	}
	return nil
}
