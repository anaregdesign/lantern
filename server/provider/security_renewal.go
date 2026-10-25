package provider

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// SecurityRenewOptions describes offline preparation, not publication. The
// existing operator directory supplies the current publisher.json and CA signer.
// OutputDirectory must be new; no serving or publisher state is changed.
type SecurityRenewOptions struct {
	OperatorDirectory, OutputDirectory string
	PublisherConfig                    string
	ValidFor                           time.Duration
}

// securityRenewalPublication is a reviewable installation plan. Publisher
// configuration is deliberately separate: install the leaves while stopped,
// advance the retained old config to NextConfig, then explicitly resume.
type securityRenewalPublication struct {
	Version                                    uint32
	PreviousConfig, NextConfig, PublisherState string
	PreviousConfigSHA256, NextConfigSHA256     string
	Files                                      []securityRenewalFile
}

type securityRenewalFile struct {
	Source, Target, SHA256 string
}

type securityRenewalNode struct {
	name, sourceDirectory, operatorCertificate string
	document, certificate                      []byte
}

// RenewSecurity prepares same-identity certificates and a later finite operator
// window. It never overwrites existing files, publishes memberships, advances a
// journal, opens native state, or supplies runtime trusted time.
func RenewSecurity(ctx context.Context, options SecurityRenewOptions) error {
	return renewSecurityAt(ctx, options, time.Now().UTC().Truncate(time.Second))
}

func renewSecurityAt(ctx context.Context, options SecurityRenewOptions, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.ValidFor == 0 {
		options.ValidFor = 30 * 24 * time.Hour
	}
	operator, err := filepath.EvalSymlinks(options.OperatorDirectory)
	if err != nil || operator != options.OperatorDirectory || !cleanAbsolutePath(operator) || !privateNewDirectory(options.OutputDirectory) ||
		pathsOverlap(operator, options.OutputDirectory) || options.ValidFor < time.Hour || options.ValidFor > 365*24*time.Hour ||
		now.IsZero() || now.Location() != time.UTC {
		return errSecurityProvisioning
	}
	configPath := options.PublisherConfig
	if configPath == "" {
		configPath = filepath.Join(operator, "publisher.json")
	}
	resolvedConfig, err := filepath.EvalSymlinks(configPath)
	if err != nil || resolvedConfig != configPath || !cleanAbsolutePath(configPath) {
		return errSecurityProvisioning
	}
	oldRaw, err := readSecurityOperatorFile(configPath, true, 1<<20)
	if err != nil {
		return errSecurityProvisioning
	}
	var previous membershippublisher.Config
	if decodeSecurityConfigJSON(string(oldRaw), &previous, oidc.ValidateOperatorJSON) != nil || !validRenewalPublisher(previous, operator) {
		return errSecurityProvisioning
	}
	next := previous
	next.End = now.Add(options.ValidFor)
	allowance := previous.CleanupBy.Sub(previous.End)
	next.CleanupBy = next.End.Add(allowance)
	if !next.End.After(previous.End) || !next.CleanupBy.After(previous.CleanupBy) || next.CleanupBy.Year() >= 2080 {
		return errSecurityProvisioning
	}
	roots, err := readSecurityOperatorFile(filepath.Join(operator, "roots.pem"), true, 1<<20)
	if err != nil {
		return errSecurityProvisioning
	}
	caKey, err := readSecurityOperatorFile(filepath.Join(operator, "ca-signing-key.pem"), true, 16<<10)
	if err != nil {
		return errSecurityProvisioning
	}
	defer clear(caKey)
	controlKey, err := readSecurityOperatorFile(filepath.Join(operator, "control-signing-key.pem"), true, 16<<10)
	if err != nil {
		return errSecurityProvisioning
	}
	defer clear(controlKey)
	dataKey, err := readSecurityOperatorFile(filepath.Join(operator, "data-signing-key.pem"), true, 16<<10)
	if err != nil {
		return errSecurityProvisioning
	}
	defer clear(dataKey)
	controlPublic, err := renewalPublicKey(controlKey)
	if err != nil {
		return errSecurityProvisioning
	}
	dataPublic, err := renewalPublicKey(dataKey)
	if err != nil {
		return errSecurityProvisioning
	}
	control, data := previous.Streams[0], previous.Streams[1]
	if control.Kind != "control" {
		control, data = data, control
	}
	var nodes []securityRenewalNode
	defer func() {
		for _, node := range nodes {
			clear(node.document)
		}
	}()
	seen := map[string]bool{}
	for i, target := range control.Targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Dir(filepath.Dir(target))
		name := filepath.Base(dir)
		if !validSecurityNodeName(name) || seen[name] || target != filepath.Join(dir, "membership", "control.json") ||
			data.Targets[i] != filepath.Join(dir, "membership", "data.json") ||
			control.Certificates[i] != filepath.Join(operator, "certificates", name+".pem") ||
			pathsOverlap(options.OutputDirectory, dir) {
			return errSecurityProvisioning
		}
		seen[name] = true
		originalPath := filepath.Join(dir, "identity.json")
		document, raw, err := readSecurityMaterial(originalPath)
		if err != nil || document.Node != name ||
			pathsOverlap(options.OutputDirectory, document.StatePaths.Directory) || pathsOverlap(options.OutputDirectory, document.StatePaths.CustodyDirectory) ||
			pathsOverlap(operator, document.StatePaths.Directory) || pathsOverlap(operator, document.StatePaths.CustodyDirectory) {
			return errSecurityProvisioning
		}
		defer clear(raw)
		trustedDataPublic, err := loadOperatorPublicKey(document.Peer.OperatorKey)
		if err != nil || !bytes.Equal(trustedDataPublic, dataPublic) || !bytes.Equal(document.Peer.VerificationKey, dataPublic) ||
			!renewalOriginalSigners(document.Authority, control.Initial, data.Initial, controlPublic, dataPublic, roots, document.Peer.Domain, document.Peer.Members) {
			return errSecurityProvisioning
		}
		workloadRoots, err := readSecurityOperatorFile(document.Peer.Roots, true, 1<<20)
		if err != nil || !bytes.Equal(workloadRoots, roots) {
			return errSecurityProvisioning
		}
		newAuthority, certificate, err := security.RenewProvisioningIdentity(roots, caKey, document.Authority, now, next.CleanupBy, document.TargetPlatform)
		if err != nil {
			return errSecurityProvisioning
		}
		document.Authority = newAuthority
		renewed, err := json.Marshal(document)
		clear(newAuthority)
		if err != nil {
			return errSecurityProvisioning
		}
		nodes = append(nodes, securityRenewalNode{name: name, sourceDirectory: dir, operatorCertificate: control.Certificates[i], document: renewed, certificate: certificate})
	}
	publisherRaw, err := json.Marshal(next)
	if err != nil {
		return errSecurityProvisioning
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := createPrivateDirectory(options.OutputDirectory); err != nil {
		return err
	}
	oldConfigDigest, nextConfigDigest := sha256.Sum256(oldRaw), sha256.Sum256(publisherRaw)
	publication := securityRenewalPublication{Version: 1, PreviousConfig: configPath, NextConfig: filepath.Join(options.OutputDirectory, "publisher.json"), PublisherState: filepath.Join(operator, "state.json"),
		PreviousConfigSHA256: hex.EncodeToString(oldConfigDigest[:]), NextConfigSHA256: hex.EncodeToString(nextConfigDigest[:])}
	for _, dir := range []string{filepath.Join(options.OutputDirectory, "nodes"), filepath.Join(options.OutputDirectory, "certificates")} {
		if err := createPrivateDirectory(dir); err != nil {
			return err
		}
	}
	operatorDigest := sha256.Sum256(publisherRaw)
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(options.OutputDirectory, "nodes", node.name)
		if err := createPrivateDirectory(dir); err != nil {
			return err
		}
		prepared := []struct {
			name, target string
			value        []byte
		}{
			{"identity.json", filepath.Join(node.sourceDirectory, "identity.json"), node.document},
			{"workload.pem", filepath.Join(node.sourceDirectory, "workload.pem"), node.certificate},
		}
		for _, file := range prepared {
			source := filepath.Join(dir, file.name)
			if err := writePrivateNewFile(source, file.value); err != nil {
				return err
			}
			publication.Files = append(publication.Files, renewalPublicationFile(source, file.target, file.value))
		}
		certificatePath := filepath.Join(options.OutputDirectory, "certificates", node.name+".pem")
		if err := writePrivateNewFile(certificatePath, node.certificate); err != nil {
			return err
		}
		publication.Files = append(publication.Files, renewalPublicationFile(certificatePath, node.operatorCertificate, node.certificate))
	}
	if err := writePrivateNewFile(publication.NextConfig, publisherRaw); err != nil {
		return err
	}
	// Sign completion only after all candidate leaves and the complete next
	// operator configuration are durable. A partial candidate grants no setup.
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(options.OutputDirectory, "nodes", node.name)
		if err := writeSecurityCompletion(dir, node.document, operatorDigest, controlKey); err != nil {
			return err
		}
		markerPath := filepath.Join(dir, "COMPLETE")
		marker, err := readSecurityOperatorFile(markerPath, true, 16<<10)
		if err != nil {
			return errSecurityProvisioning
		}
		// Install every completion after all replacement documents and leaves.
		publication.Files = append(publication.Files, renewalPublicationFile(markerPath, filepath.Join(node.sourceDirectory, "COMPLETE"), marker))
	}
	publicationRaw, err := json.Marshal(publication)
	if err != nil {
		return errSecurityProvisioning
	}
	if err := writePrivateNewFile(filepath.Join(options.OutputDirectory, "publication.json"), publicationRaw); err != nil {
		return err
	}
	// This marker commits preparation only. Installation still requires the
	// reviewed plan, stopped owners, and the publisher's explicit transition.
	digest := sha256.Sum256(publicationRaw)
	return writePrivateNewFile(filepath.Join(options.OutputDirectory, "COMPLETE"), []byte(hex.EncodeToString(digest[:])+"\n"))
}

func renewalPublicationFile(source, target string, value []byte) securityRenewalFile {
	digest := sha256.Sum256(value)
	return securityRenewalFile{Source: source, Target: target, SHA256: hex.EncodeToString(digest[:])}
}

func validRenewalPublisher(config membershippublisher.Config, operator string) bool {
	allowance := config.CleanupBy.Sub(config.End)
	if config.Version != 1 || len(config.Streams) != 2 || config.End.IsZero() || allowance <= 0 || !config.End.Add(allowance).Equal(config.CleanupBy) ||
		config.Interval < time.Second || config.Lifetime <= 2*peerauth.ClockMargin || config.Lifetime > peerauth.MaxMembershipLifetime ||
		config.Interval >= (config.Lifetime-2*peerauth.ClockMargin)/2 {
		return false
	}
	kinds := map[string]bool{}
	for _, stream := range config.Streams {
		if stream.Kind != "control" && stream.Kind != "data" || kinds[stream.Kind] || len(stream.Targets) < 1 || len(stream.Targets) > 31 ||
			len(stream.Certificates) != len(stream.Targets) || stream.KeyFile != filepath.Join(operator, stream.Kind+"-signing-key.pem") ||
			stream.RootsFile != filepath.Join(operator, "roots.pem") || len(stream.Initial) == 0 || len(stream.Initial) > peerauth.MaxManifestBytes {
			return false
		}
		kinds[stream.Kind] = true
	}
	return len(config.Streams[0].Targets) == len(config.Streams[1].Targets) && reflect.DeepEqual(config.Streams[0].Certificates, config.Streams[1].Certificates)
}

func renewalPublicKey(raw []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errSecurityProvisioning
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errSecurityProvisioning
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize || !bytes.Equal(key, ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])) {
		return nil, errSecurityProvisioning
	}
	defer clear(key)
	return bytes.Clone(key.Public().(ed25519.PublicKey)), nil
}

func renewalOriginalSigners(authority, controlRaw, dataRaw []byte, controlPublic, dataPublic ed25519.PublicKey, roots []byte, expected peerauth.Domain, members []peerauth.Member) bool {
	var document struct {
		Membership struct {
			PublicKey []byte                  `json:"public_key"`
			Profile   peerauth.ControlProfile `json:"profile"`
		} `json:"membership"`
	}
	if json.Unmarshal(authority, &document) != nil || !bytes.Equal(document.Membership.PublicKey, controlPublic) ||
		document.Membership.Profile.TrustDigest != sha256.Sum256(roots) {
		return false
	}
	control, err := peerauth.VerifyControlManifest(controlRaw, controlPublic, document.Membership.Profile.Lineage)
	if err != nil || control.Profile.Digest() != document.Membership.Profile.Digest() {
		return false
	}
	if expected.TrustDigest != sha256.Sum256(roots) || expected.AuthMode != "oidc" || expected.CurrentProfile == "" || expected.SecurityGeneration == [16]byte{} {
		return false
	}
	data, err := peerauth.VerifyManifest(dataRaw, dataPublic, expected)
	if err != nil || len(data.Members) != len(control.Profile.Voters) || !slices.Equal(data.Members, members) {
		return false
	}
	for _, member := range data.Members {
		found := false
		for _, voter := range control.Profile.Voters {
			found = found || member.ID == voter.Workload.ID && member.Identity == voter.Workload.Identity && member.SPKI == voter.Workload.SPKI
		}
		if !found {
			return false
		}
	}
	return true
}
