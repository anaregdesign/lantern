package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"reflect"
	"strconv"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

const currentJSONProvisioningVersion = 1

func currentJSONGenesis(g currentGenesisDocument) (*s2cTrust, *S1ApplyState, error) {
	if g.Version != CurrentPublicVersion || !g.Execution.valid() || g.Roots.Genesis == [32]byte{} || g.Roots.Retirement != [32]byte{} ||
		g.TimeProfile != sha256.Sum256([]byte(authorityTimeProfileDescription)) {
		return nil, nil, errS3AConfig
	}
	for _, origin := range g.Origins {
		if origin.Profile != authorityAdmissionProfile() {
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
	if trust == nil {
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
	u, err := url.Parse(c.Membership.Self.Origin)
	_, port, listenErr := net.SplitHostPort(listen)
	n, numberErr := strconv.Atoi(port)
	if !matched || err != nil || listenErr != nil || numberErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || port != u.Port() {
		return errS3AConfig
	}
	return nil
}

func currentJSONConfig(d currentJSONProvisioningDocument, manifestRaw []byte) (s3aConfig, CurrentProfile, error) {
	trust, state, err := currentJSONGenesis(d.Genesis)
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
		!c.Limits.valid(c.Participant) || p.BScope.validateGenesis(trust.genesis) != nil || s3aPaths(&c) != nil || currentJSONEnrollment(c, d.ListenAddress, len(d.OriginKey) != 0) != nil {
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
	if p == nil || p.source != "" && len(p.jsonSource) != 0 {
		return nil, errS3AConfig
	}
	if len(p.jsonSource) != 0 {
		return LoadCurrentProvisioningJSON(p.jsonSource)
	}
	return LoadCurrentProvisioning(p.source)
}

// ValidateInitialIssuers compares requested policy with original trust without
// changing its administrator locks, object ownership or configuration revisions.
func (p *CurrentProvisioning) ValidateInitialIssuers(issuers []Issuer) error {
	if p == nil || p.config.Participant.Trust == nil || len(issuers) > MaxIssuers {
		return errS3AConfig
	}
	original := p.config.Participant.Trust.genesis.state.projection.snapshot
	seen := make(map[string]bool, len(issuers))
	for _, configured := range issuers {
		trusted, found := original.Issuer(configured.URL)
		if !found || seen[configured.URL] {
			return errS3AConfig
		}
		seen[configured.URL] = true
		configured.ConfigRevision, configured.EnvOwned = trusted.ConfigRevision, trusted.EnvOwned
		a, e1 := json.Marshal(configured)
		b, e2 := json.Marshal(trusted)
		if e1 != nil || e2 != nil || !bytes.Equal(a, b) {
			return errS3AConfig
		}
	}
	return nil
}

// Whitespace and object order are harmless; decoded shape must exactly match
// typed JSON. This catches fixed-array truncation, aliases and byte encodings.
func decodeCurrentJSONProvisioning(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > MaxImageBytes+(2<<20) || s2JSONPreflight(raw, DefaultPolicyLimits()) != nil {
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
