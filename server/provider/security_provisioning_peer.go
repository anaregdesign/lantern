package provider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"slices"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// loadProvisionedPeerIdentity compares renewable delivery with immutable,
// completion-signed original data trust before any native state is created.
func loadProvisionedPeerIdentity(p provisionedSecurityPeer, profile security.CurrentProfile, count int) (PeerIdentityConfig, error) {
	if count < 1 || count > 31 || len(p.Members) != count || len(p.VerificationKey) != ed25519.PublicKeySize ||
		p.Domain.AuthMode != "oidc" || p.Domain.CurrentProfile != profile.Binding() || p.Domain.SecurityGeneration != profile.Generation {
		return PeerIdentityConfig{}, errSecurityProvisioning
	}
	key, err := loadOperatorPublicKey(p.OperatorKey)
	if err != nil || !bytes.Equal(key, p.VerificationKey) {
		return PeerIdentityConfig{}, errSecurityProvisioning
	}
	roots, err := readSecurityOperatorFile(p.Roots, true, 1<<20)
	if err != nil || sha256.Sum256(roots) != p.Domain.TrustDigest {
		return PeerIdentityConfig{}, errSecurityProvisioning
	}
	raw, err := readSecurityOperatorFile(p.Manifest, true, peerauth.MaxManifestBytes)
	if err != nil {
		return PeerIdentityConfig{}, errSecurityProvisioning
	}
	manifest, err := peerauth.VerifyManifest(raw, p.VerificationKey, p.Domain)
	if err != nil || !slices.Equal(manifest.Members, p.Members) {
		return PeerIdentityConfig{}, errSecurityProvisioning
	}
	var expectedKey [32]byte
	copy(expectedKey[:], p.VerificationKey)
	if expectedKey == [32]byte{} {
		return PeerIdentityConfig{}, errSecurityProvisioning
	}
	return PeerIdentityConfig{AuthMode: "oidc", Deployment: p.Domain.Deployment, StateMode: "resume", StateFile: p.State,
		CurrentProfile: profile.Binding(), SecurityGeneration: profile.Generation, ExpectedDomain: p.Domain, ExpectedOperatorKey: expectedKey, FixedMembers: slices.Clone(p.Members),
		ManifestFile: p.Manifest, OperatorKeyFile: p.OperatorKey, SelfIdentity: p.Identity, CertFile: p.Certificate, KeyFile: p.Key, CAFile: p.Roots}, nil
}
