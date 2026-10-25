package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

const currentJSONProvisioningVersion = 1

// ValidateProvisioningJSON checks bounded operator-document structure before a
// caller's strict typed decode. It rejects duplicate keys without accepting
// any document as trusted provisioning or opening its referenced paths.
func ValidateProvisioningJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxImageBytes+(2<<20) || s2JSONPreflight(raw, DefaultPolicyLimits()) != nil {
		return errS3AConfig
	}
	return nil
}

// ValidateProvisioningAuthorityJSON validates generated trust against its
// explicit target and complete original seed without reading paths or opening
// native state. The signed manifest's interval is only certificate validation
// context here; runtime serving must still acquire its own trusted time.
func ValidateProvisioningAuthorityJSON(raw, manifest []byte, target string, initial Image) error {
	description, valid := authorityTimeDescriptionForTarget(target)
	var document currentJSONProvisioningDocument
	if !valid || decodeCurrentJSONProvisioning(raw, &document) != nil || document.Version != currentJSONProvisioningVersion {
		return errS3AConfig
	}
	c, _, err := currentJSONConfigForTime(document, manifest, description, false)
	if err != nil {
		return errS3AConfig
	}
	for _, path := range []string{document.Participant.PPath, document.Participant.BPath, document.Membership.Path, document.Membership.ManifestFile, document.FloorsFile} {
		if !provisioningPath(path) {
			return errS3AConfig
		}
	}
	if (&CurrentProvisioning{config: c}).ValidateInitialImage(initial) != nil {
		return errS3AConfig
	}
	signed, err := peerauth.VerifyControlManifest(manifest, c.Membership.Key, c.Membership.Profile.Lineage)
	if err != nil {
		return errS3AConfig
	}
	c.Membership.TimeBounds = func() (time.Time, time.Time, error) { return signed.IssuedAt, signed.ExpiresAt, nil }
	identity, err := s3aLoadIdentity(c)
	if err != nil {
		return errS3AConfig
	}
	defer identity.clear()
	if !validProvisioningOriginKey(c, document.OriginKey) {
		return errS3AConfig
	}
	return nil
}

func validProvisioningOriginKey(c s3aConfig, raw []byte) bool {
	if c.Participant.Trust == nil {
		return false
	}
	if c.Participant.OwnedOrigin == 0 {
		// An enrolled fixed voter may observe, endorse and serve current reads
		// without owning a consume origin. It must carry no replacement origin key.
		_, found := c.Participant.Trust.origin(0)
		return !found && len(raw) == 0
	}
	origin := ed25519.PrivateKey(raw)
	descriptor, found := c.Participant.Trust.origin(c.Participant.OwnedOrigin)
	if !found || len(origin) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(origin[:ed25519.SeedSize]), origin) ||
		!bytes.Equal(origin.Public().(ed25519.PublicKey), descriptor.PublicKey[:]) {
		return false
	}
	spki := sha256.Sum256(s3aVotingSPKI(descriptor.PublicKey))
	for _, voter := range c.Membership.Profile.Voters {
		if voter.Key == descriptor.PublicKey || voter.Workload.SPKI == spki {
			return false
		}
	}
	return true
}

// ValidateProvisioningAuthorityPaths binds generated state families to the
// independently selected setup locations before initialization can create them.
func ValidateProvisioningAuthorityPaths(raw []byte, stateDir, renewableDir string) error {
	var d currentJSONProvisioningDocument
	if !provisioningPath(stateDir) || !provisioningPath(renewableDir) || decodeCurrentJSONProvisioning(raw, &d) != nil ||
		d.Participant.PPath != filepath.Join(stateDir, "control-protocol.wal") ||
		d.Participant.BPath != filepath.Join(stateDir, "control-materialized.wal") ||
		d.Membership.Path != filepath.Join(stateDir, "control-membership") ||
		d.FloorsFile != filepath.Join(stateDir, "control-floors.json") ||
		d.Membership.ManifestFile != filepath.Join(renewableDir, "control.json") {
		return errS3AConfig
	}
	return nil
}

// ValidateProvisioningAuthorityLocations checks the locations already bound
// into genuine external immutable authority material. It never rewrites paths
// or reads an old configuration selector. Native custody additionally checks
// actual descriptor identities and private files when opening existing paths.
func ValidateProvisioningAuthorityLocations(raw []byte, stateDir, custodyDir, renewableDir string) error {
	var d currentJSONProvisioningDocument
	if !provisioningPath(stateDir) || !provisioningPath(custodyDir) || !provisioningPath(renewableDir) || decodeCurrentJSONProvisioning(raw, &d) != nil || d.Version != currentJSONProvisioningVersion {
		return errS3AConfig
	}
	for _, path := range []string{d.Participant.PPath, d.Participant.BPath, d.Membership.Path} {
		if !provisioningPath(path) || filepath.Dir(path) != stateDir {
			return errS3AConfig
		}
	}
	if !provisioningPath(d.FloorsFile) || filepath.Dir(d.FloorsFile) != custodyDir || d.Membership.ManifestFile != filepath.Join(renewableDir, "control.json") {
		return errS3AConfig
	}
	seen := map[string]bool{}
	paths := []string{d.FloorsFile, d.FloorsFile + ".state", d.FloorsFile + ".state.lease", d.Membership.ManifestFile}
	for _, family := range []string{d.Participant.PPath, d.Participant.BPath, d.Membership.Path} {
		paths = append(paths, family, family+".tip", family+".lease")
	}
	for _, path := range paths {
		if seen[path] {
			return errS3AConfig
		}
		seen[path] = true
	}
	return nil
}

// ProvisioningAuthorityProfile returns the exact independently signed profile
// without opening state paths on the provisioning host. It asserts no readiness.
func ProvisioningAuthorityProfile(raw, manifest []byte, target string) (CurrentProfile, error) {
	description, valid := authorityTimeDescriptionForTarget(target)
	var document currentJSONProvisioningDocument
	if !valid || decodeCurrentJSONProvisioning(raw, &document) != nil || document.Version != currentJSONProvisioningVersion {
		return CurrentProfile{}, errS3AConfig
	}
	_, profile, err := currentJSONConfigForTime(document, manifest, description, false)
	return profile, err
}

func currentJSONGenesis(g currentGenesisDocument) (*s2cTrust, *S1ApplyState, error) {
	return currentJSONGenesisForTime(g, authorityTimeProfileDescription)
}

func currentJSONGenesisForTime(g currentGenesisDocument, description string) (*s2cTrust, *S1ApplyState, error) {
	if g.Version != CurrentPublicVersion || !g.Execution.valid() || g.Roots.Genesis == [32]byte{} || g.Roots.Retirement != [32]byte{} ||
		g.TimeProfile != sha256.Sum256([]byte(description)) {
		return nil, nil, errS3AConfig
	}
	for _, origin := range g.Origins {
		if origin.Profile != authorityAdmissionProfileForTime(description) {
			return nil, nil, errS3AConfig
		}
	}
	projection, err := NewS1Projection(g.Image, g.Execution.Policy, g.Domain, g.Cohort, g.Fences, g.Generation)
	if err != nil {
		return nil, nil, errS3AConfig
	}
	membership, err := s2cMemberSetDigest(g.Members)
	if err != nil {
		return nil, nil, errS3AConfig
	}
	state, err := NewS1ApplyState(projection, membership, g.Execution.Capacity, S1Retention{})
	if err != nil {
		return nil, nil, errS3AConfig
	}
	trust, err := newS2CTrust(s2cBootstrap{&s2TrustedGenesis{state, g.Roots}, g.Members, g.Origins, g.Bounds})
	if err != nil {
		return nil, nil, errS3AConfig
	}
	return trust, state, nil
}

func currentJSONEnrollment(c s3aConfig, listen string, hasOriginKey bool) error {
	p, trust := c.Participant, c.Participant.Trust
	if trust == nil || p.PIdentity == [32]byte{} || p.PEpoch == [16]byte{} || !p.PPolicy.valid() ||
		p.PendingBytes == 0 || p.PendingBytes > p.PPolicy.Bytes || p.PendingCount == 0 || p.PendingCount > p.PPolicy.Records ||
		p.OutboxBytes == 0 || p.OutboxBytes > uint64(MaxSystemJournalBytes) {
		return errS3AConfig
	}
	self, found := trust.member(p.Member)
	origin, owned := trust.origin(p.OwnedOrigin)
	if !found || p.Incarnation == [16]byte{} {
		return errS3AConfig
	}
	if p.OwnedOrigin == 0 {
		if owned || hasOriginKey {
			return errS3AConfig
		}
	} else if !owned || !self.Proposer || origin.Member != self.ID || origin.Incarnation != p.Incarnation || !hasOriginKey {
		return errS3AConfig
	}
	matched := false
	for _, voter := range c.Membership.Profile.Voters {
		member, exists := trust.member(voter.Voter)
		if !exists || member.PublicKey != voter.Key || member.Proposer != voter.Proposer {
			return errS3AConfig
		}
		matched = matched || voter.Voter == self.ID && voter.Workload == c.Membership.Self
	}
	if !matched || !validCurrentLocalListen(listen, c.Membership.Self.Origin) {
		return errS3AConfig
	}
	return nil
}

func validCurrentLocalListen(address, origin string) bool {
	if len(address) == 0 || len(address) > 2048 || strings.ContainsAny(address, "\x00\r\n") {
		return false
	}
	u, err := url.Parse(origin)
	_, port, listenErr := net.SplitHostPort(address)
	number, numberErr := strconv.Atoi(port)
	return err == nil && listenErr == nil && numberErr == nil && number >= 1 && number <= 65535 && strconv.Itoa(number) == port && port == u.Port()
}

// ValidateProvisioningLocalListen checks a supplied local control bind before
// state creation. Advertised membership origins and the native Start port proof
// remain fixed; this cannot rebase signed material to another control port.
func ValidateProvisioningLocalListen(raw []byte, address string) error {
	var document currentJSONProvisioningDocument
	if decodeCurrentJSONProvisioning(raw, &document) != nil || document.Version != currentJSONProvisioningVersion || !validCurrentLocalListen(address, document.Membership.Self.Origin) {
		return errS3AConfig
	}
	return nil
}

func currentJSONConfig(d currentJSONProvisioningDocument, manifestRaw []byte) (s3aConfig, CurrentProfile, error) {
	return currentJSONConfigForTime(d, manifestRaw, authorityTimeProfileDescription, true)
}

// The generator shares structural/cryptographic validation without opening
// paths on the provisioning host. Only runtime loading checks local paths.
func currentJSONConfigForTime(d currentJSONProvisioningDocument, manifestRaw []byte, description string, localPaths bool) (s3aConfig, CurrentProfile, error) {
	trust, state, err := currentJSONGenesisForTime(d.Genesis, description)
	if err != nil || len(d.Membership.PublicKey) != ed25519.PublicKeySize {
		return s3aConfig{}, CurrentProfile{}, errS3AConfig
	}
	p, m := d.Participant, d.Membership
	c := s3aConfig{Participant: s2cParticipantConfig{
		Trust: trust, Member: p.Member, Incarnation: p.Incarnation, PPath: p.PPath, BPath: p.BPath,
		PIdentity: p.PIdentity, PEpoch: p.PEpoch, PPolicy: p.PPolicy, BScope: p.BScope,
		OwnedOrigin: p.OwnedOrigin, PendingBytes: p.PendingBytes, PendingCount: p.PendingCount, OutboxBytes: p.OutboxBytes,
	}, Limits: d.Limits, Manifest: bytes.Clone(manifestRaw),
		Membership: peerauth.ControlStoreOptions{Path: m.Path, Key: ed25519.PublicKey(bytes.Clone(m.PublicKey)), Profile: m.Profile, Self: m.Self},
		Identity: s3aIdentityFiles{material: &s3aIdentityMaterial{roots: []byte(d.Identity.Roots), certificate: []byte(d.Identity.Certificate),
			tlsKey: []byte(d.Identity.TLSKey), votingKey: bytes.Clone(d.Identity.VotingKey), originKey: bytes.Clone(d.OriginKey)}}}
	manifest, err := peerauth.VerifyControlManifest(c.Manifest, c.Membership.Key, c.Membership.Profile.Lineage)
	if err != nil || !c.Membership.Profile.Valid() || manifest.Profile.Digest() != c.Membership.Profile.Digest() || c.Membership.Profile.ProtocolScope != trust.scope ||
		!c.Limits.valid(c.Participant) || p.BScope.validateGenesis(trust.genesis) != nil || currentJSONEnrollment(c, d.ListenAddress, len(d.OriginKey) != 0) != nil {
		return s3aConfig{}, CurrentProfile{}, errS3AConfig
	}
	if localPaths && s3aPaths(&c) != nil {
		return s3aConfig{}, CurrentProfile{}, errS3AConfig
	}
	g := d.Genesis
	profile := CurrentProfile{CurrentPublicVersion, g.Domain, g.Cohort, g.Generation, trust.scope, g.TimeProfile, c.Membership.Profile.Digest(), state.configuration.Digest()}
	return c, profile, nil
}

type currentJSONProvisioningDocument struct {
	Version       uint32                        `json:"version"`
	Genesis       currentGenesisDocument        `json:"genesis"`
	Participant   currentParticipantDocument    `json:"participant"`
	Membership    currentJSONMembershipDocument `json:"membership"`
	Identity      currentJSONIdentityDocument   `json:"identity"`
	OriginKey     []byte                        `json:"origin_key,omitempty"`
	FloorsFile    string                        `json:"floors_file"`
	ListenAddress string                        `json:"listen_address"`
	Limits        s3aLimits                     `json:"limits"`
}

type currentJSONMembershipDocument struct {
	Path         string                  `json:"path"`
	ManifestFile string                  `json:"manifest_file"`
	PublicKey    []byte                  `json:"public_key"`
	Profile      peerauth.ControlProfile `json:"profile"`
	Self         peerauth.Member         `json:"self"`
}

type currentJSONIdentityDocument struct {
	Roots       string `json:"roots_pem"`
	Certificate string `json:"certificate_pem"`
	TLSKey      string `json:"tls_key_pem"`
	VotingKey   []byte `json:"voting_key"`
}

// LoadCurrentProvisioningJSON owns independent original operator material. It
// opens no journal, signer, injected clock or assertion of serving readiness.
// Only the separately signed renewable manifest remains a runtime input file.
func LoadCurrentProvisioningJSON(raw []byte) (*CurrentProvisioning, error) {
	var document currentJSONProvisioningDocument
	if err := decodeCurrentJSONProvisioning(raw, &document); err != nil || document.Version != currentJSONProvisioningVersion {
		return nil, errS3AConfig
	}
	floors, err := currentCustodyPath(document.FloorsFile)
	if err != nil {
		return nil, err
	}
	manifestPath, err := currentCustodyPath(document.Membership.ManifestFile)
	if err != nil {
		return nil, err
	}
	manifestRaw, err := s3aReadProvisioned(manifestPath, peerauth.MaxManifestBytes)
	if err != nil {
		return nil, errS3AConfig
	}
	c, profile, err := currentJSONConfig(document, manifestRaw)
	if err != nil {
		return nil, err
	}
	paths := []string{manifestPath}
	for _, family := range []string{c.Participant.PPath, c.Participant.BPath, c.Membership.Path} {
		paths = append(paths, family, family+".tip", family+".lease")
	}
	for i := range paths {
		paths[i], err = currentCustodyPath(paths[i])
		if err != nil {
			return nil, err
		}
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, errS3AConfig
	}
	participant := document.Participant
	participant.PPath, participant.BPath = c.Participant.PPath, c.Participant.BPath
	// Genesis and membership already pin voting/origin public keys, workload
	// SPKI and CA digest. Private material and configuration input paths never
	// define custody; durable state locations retain their existing identity.
	binding := s2cHash("current-custody-json-v1", struct {
		Genesis     currentGenesisDocument
		Participant currentParticipantDocument
		Membership  struct {
			Path    string
			Key     []byte
			Profile peerauth.ControlProfile
			Self    peerauth.Member
		}
		Floors string
		Listen string
		Limits s3aLimits
	}{Genesis: document.Genesis, Participant: participant, Membership: struct {
		Path    string
		Key     []byte
		Profile peerauth.ControlProfile
		Self    peerauth.Member
	}{c.Membership.Path, document.Membership.PublicKey, document.Membership.Profile, document.Membership.Self},
		Floors: floors, Listen: document.ListenAddress, Limits: document.Limits})
	return &CurrentProvisioning{config: c, floors: floors, listen: document.ListenAddress, profile: profile,
		jsonSource: canonical, binding: binding, paths: paths, manifest: manifestPath}, nil
}

func (p *CurrentProvisioning) reload() (*CurrentProvisioning, error) {
	if p == nil || len(p.jsonSource) == 0 {
		return nil, errS3AConfig
	}
	verified, err := LoadCurrentProvisioningJSON(p.jsonSource)
	if err != nil {
		return nil, err
	}
	if p.localListen != "" {
		verified, err = verified.WithLocalListen(p.localListen)
		if err != nil {
			return nil, err
		}
	}
	if p.config.Participant.configured != nil {
		return verified.WithConfiguredTrust(p.config.Participant.configured)
	}
	return verified, nil
}

// ProvisioningInitialImage returns detached historical seed data. It validates
// canonical provisioning structure and image invariants without opening state;
// possession of this image grants no runtime or configuration authority.
func ProvisioningInitialImage(raw []byte) (Image, error) {
	var document currentJSONProvisioningDocument
	if decodeCurrentJSONProvisioning(raw, &document) != nil || document.Version != currentJSONProvisioningVersion {
		return Image{}, errS3AConfig
	}
	snapshot, err := CompileImage(document.Genesis.Image, DefaultPolicyLimits())
	if err != nil {
		return Image{}, errS3AConfig
	}
	return snapshot.Image(), nil
}

// ValidateInitialImage compares the complete configured seed with immutable
// genesis only. It neither reads nor reapplies later runtime security changes.
func (p *CurrentProvisioning) ValidateInitialImage(image Image) error {
	if p == nil || p.config.Participant.Trust == nil {
		return errS3AConfig
	}
	original := p.config.Participant.Trust.genesis.state.projection.snapshot
	requested, err := CompileImage(image, original.limits)
	if err != nil || !bytes.Equal(requested.image, original.image) {
		return errS3AConfig
	}
	return nil
}

// Whitespace and object order are harmless; decoded shape must exactly match
// typed JSON. This catches fixed-array truncation, aliases and byte encodings.
func decodeCurrentJSONProvisioning(raw []byte, target any) error {
	if ValidateProvisioningJSON(raw) != nil {
		return errS3AConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errS3AConfig
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return errS3AConfig
	}
	decode := func(raw []byte) (any, error) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var value any
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, errS3AConfig
		}
		return value, nil
	}
	original, err := decode(raw)
	if err != nil {
		return errS3AConfig
	}
	normalized, err := decode(canonical)
	if err != nil || !reflect.DeepEqual(original, normalized) {
		return errS3AConfig
	}
	return nil
}

// ValidateProvisioningConfiguredTrust checks startup capability enrollment and
// supplied public issuer structure before the persistence owner creates paths.
// The explicit target is construction validation, never runtime clock evidence.
func ValidateProvisioningConfiguredTrust(raw, manifest []byte, target string, desired *ConfiguredTrust) error {
	description, valid := authorityTimeDescriptionForTarget(target)
	var document currentJSONProvisioningDocument
	if !valid || !desired.valid() || decodeCurrentJSONProvisioning(raw, &document) != nil || document.Version != currentJSONProvisioningVersion {
		return errS3AConfig
	}
	c, _, err := currentJSONConfigForTime(document, manifest, description, false)
	if err != nil {
		return errS3AConfig
	}
	member, known := c.Participant.Trust.member(c.Participant.Member)
	if !known || member.ConfiguredTrust != configuredTrustVersion {
		return errS3AConfig
	}
	if c.Participant.OwnedOrigin != 0 {
		d, known := c.Participant.Trust.origin(c.Participant.OwnedOrigin)
		if !known || d.Member != c.Participant.Member || d.ConfiguredTrust != configuredTrustVersion || d.Profile != authorityAdmissionProfileForTime(description) {
			return errS3AConfig
		}
	}
	return nil
}

// ValidateProvisioningInitialConfiguredTrust validates fresh initialization's
// historical seed plus today's desired trust before any state path is created.
// It preserves the exact original policy and principals; initialization never
// rebuilds default Roles or restores an administrator from current input.
func ValidateProvisioningInitialConfiguredTrust(raw, manifest []byte, target string, desired *ConfiguredTrust) error {
	if err := ValidateProvisioningConfiguredTrust(raw, manifest, target, desired); err != nil {
		return err
	}
	description, _ := authorityTimeDescriptionForTarget(target)
	var document currentJSONProvisioningDocument
	if decodeCurrentJSONProvisioning(raw, &document) != nil {
		return errS3AConfig
	}
	c, _, err := currentJSONConfigForTime(document, manifest, description, false)
	if err != nil {
		return errS3AConfig
	}
	_, err = assessConfiguredTrust(c.Participant.Trust.genesis.state.projection, desired)
	return err
}
