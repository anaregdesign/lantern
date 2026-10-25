package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// RenewProvisioningIdentity is explicit offline certificate maintenance. It
// retains the workload key, URI, SANs, genesis, fixed voters, paths and budgets;
// only the leaf certificate's serial and validity change. The CA key belongs
// exclusively to operator custody. This function never reads files, opens a
// runtime or supplies trusted runtime time.
func RenewProvisioningIdentity(roots, caKey, authorityJSON []byte, now, cleanupBy time.Time, target string) (newAuthority, newCertificate []byte, err error) {
	description, valid := authorityTimeDescriptionForTarget(target)
	var document currentJSONProvisioningDocument
	start, end, periodErr := provisioningCertificatePeriod(now, cleanupBy)
	if !valid || periodErr != nil || decodeCurrentJSONProvisioning(authorityJSON, &document) != nil ||
		document.Version != currentJSONProvisioningVersion || !bytes.Equal(roots, []byte(document.Identity.Roots)) ||
		sha256.Sum256(roots) != document.Membership.Profile.TrustDigest || len(document.Membership.PublicKey) != ed25519.PublicKeySize {
		return nil, nil, ErrProvisioning
	}
	defer clear(document.Identity.VotingKey)
	defer clear(document.OriginKey)
	trust, _, err := currentJSONGenesisForTime(document.Genesis, description)
	if err != nil {
		return nil, nil, ErrProvisioning
	}
	root, key, err := provisioningMaintenanceCA(roots, caKey)
	if err != nil {
		return nil, nil, err
	}
	defer clear(key)
	if start.Before(root.NotBefore) || end.After(root.NotAfter) {
		return nil, nil, ErrProvisioning
	}
	pair, err := tls.X509KeyPair([]byte(document.Identity.Certificate), []byte(document.Identity.TLSKey))
	if err != nil || len(pair.Certificate) != 1 {
		return nil, nil, ErrProvisioning
	}
	tlsKey, ok := pair.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, ErrProvisioning
	}
	defer clear(tlsKey)
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || !leaf.NotAfter.After(leaf.NotBefore) || !end.After(leaf.NotAfter) {
		return nil, nil, ErrProvisioning
	}
	// An expired original leaf remains usable as offline identity evidence. Its
	// old midpoint proves its exact chain and bindings; it grants no live lease.
	oldTime := leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) / 2)
	p := document.Participant
	c := s3aConfig{
		Participant: s2cParticipantConfig{Trust: trust, Member: p.Member, Incarnation: p.Incarnation, PPath: p.PPath, BPath: p.BPath,
			PIdentity: p.PIdentity, PEpoch: p.PEpoch, PPolicy: p.PPolicy, BScope: p.BScope, OwnedOrigin: p.OwnedOrigin,
			PendingBytes: p.PendingBytes, PendingCount: p.PendingCount, OutboxBytes: p.OutboxBytes},
		Limits: document.Limits,
		Membership: peerauth.ControlStoreOptions{Profile: document.Membership.Profile, Self: document.Membership.Self,
			TimeBounds: func() (time.Time, time.Time, error) { return oldTime, oldTime, nil }},
		Identity: s3aIdentityFiles{material: &s3aIdentityMaterial{roots: bytes.Clone(roots),
			certificate: []byte(document.Identity.Certificate), tlsKey: []byte(document.Identity.TLSKey), votingKey: bytes.Clone(document.Identity.VotingKey)}},
	}
	defer clear(c.Identity.material.tlsKey)
	defer clear(c.Identity.material.votingKey)
	if !c.Limits.valid(c.Participant) || p.BScope.validateGenesis(trust.genesis) != nil ||
		currentJSONEnrollment(c, document.ListenAddress, len(document.OriginKey) != 0) != nil || !validProvisioningOriginKey(c, document.OriginKey) {
		return nil, nil, ErrProvisioning
	}
	oldIdentity, err := s3aLoadIdentity(c)
	if err != nil {
		return nil, nil, ErrProvisioning
	}
	oldIdentity.clear()
	serial, err := provisioningSerial()
	if err != nil {
		return nil, nil, ErrProvisioning
	}
	template := *leaf
	template.SerialNumber, template.NotBefore, template.NotAfter = serial, start, end
	der, err := x509.CreateCertificate(rand.Reader, &template, root, tlsKey.Public(), key)
	if err != nil {
		return nil, nil, ErrProvisioning
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	document.Identity.Certificate = string(certificate)
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, nil, ErrProvisioning
	}
	return raw, certificate, nil
}

func provisioningMaintenanceCA(roots, encoded []byte) (*x509.Certificate, ed25519.PrivateKey, error) {
	rootBlock, trailing := pem.Decode(roots)
	if rootBlock == nil || rootBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, nil, ErrProvisioning
	}
	root, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil || !root.IsCA || root.CheckSignatureFrom(root) != nil {
		return nil, nil, ErrProvisioning
	}
	keyBlock, trailing := pem.Decode(encoded)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, nil, ErrProvisioning
	}
	defer clear(keyBlock.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, ErrProvisioning
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, ErrProvisioning
	}
	public, ok := root.PublicKey.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(key[:ed25519.SeedSize]), key) ||
		!bytes.Equal(key.Public().(ed25519.PublicKey), public) {
		clear(key)
		return nil, nil, ErrProvisioning
	}
	return root, key, nil
}
