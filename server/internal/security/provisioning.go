package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

var ErrProvisioning = errors.New("invalid security provisioning intent or generated trust")

// ProvisioningOptions is the complete one-time construction input. Now dates
// certificates and initial manifests; it never supplies runtime trusted time.
// Target is explicitly linux or darwin, independently of the provisioning host.
type ProvisioningOptions struct {
	Target              string
	Now, End, CleanupBy time.Time
	Image               Image
	Nodes               []ProvisioningNode
}

// ProvisioningNode describes fixed endpoints and private runtime locations.
// Construction only checks lexical paths; the persistence owner must enforce
// create-only private files/directories before any native state is initialized.
type ProvisioningNode struct {
	Name, ControlOrigin, DataOrigin, ControlListen string
	StateDir, RenewableDir                         string
}

// ProvisioningBundle separates serving material from external operator keys.
// Callers must never persist Operator inside a serving node's mount.
type ProvisioningBundle struct {
	Nodes    []ProvisionedNode
	Operator ProvisionedOperator
}

type ProvisionedNode struct {
	Name                                string
	AuthorityJSON                       []byte
	RootsPEM, CertificatePEM, TLSKeyPEM []byte
	DataManifest, DataOperatorPublicKey []byte
	DataDomain                          peerauth.Domain
	DataSelf                            peerauth.Member
	Identity                            string
	CurrentProfile                      CurrentProfile
}

type ProvisionedOperator struct {
	CAKeyPEM                      []byte
	ControlKeyPEM, DataKeyPEM     []byte
	ControlManifest, DataManifest []byte
	RootsPEM                      []byte
}

type provisioningMember struct {
	input                 ProvisioningNode
	voting, origin        ed25519.PrivateKey
	incarnation, workload [16]byte
	identity              *provisioningIdentity
	control, data         peerauth.Member
}

// GenerateProvisioning creates independent cryptographic identities once and
// validates one coherent immutable trust profile. It performs no filesystem,
// listener, network, native-custody or runtime-clock operation.
func GenerateProvisioning(options ProvisioningOptions) (*ProvisioningBundle, error) {
	description, valid := authorityTimeDescriptionForTarget(options.Target)
	if !valid || !provisioningTimesValid(options) || len(options.Nodes) < 1 || len(options.Nodes) > 31 {
		return nil, ErrProvisioning
	}
	if _, err := CompileImage(options.Image, DefaultPolicyLimits()); err != nil {
		return nil, ErrProvisioning
	}
	if err := validateProvisioningNodes(options.Nodes); err != nil {
		return nil, err
	}
	ca, err := newProvisioningCA(options.Now, options.CleanupBy)
	if err != nil {
		return nil, ErrProvisioning
	}
	defer clear(ca.key)
	controlPublic, controlKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, ErrProvisioning
	}
	defer clear(controlKey)
	dataPublic, dataKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, ErrProvisioning
	}
	defer clear(dataKey)
	g := currentGenesisDocument{Version: CurrentPublicVersion, Image: options.Image,
		Execution: provisioningExecution(), Bounds: provisioningBounds(), TimeProfile: sha256.Sum256([]byte(description))}
	var lineage peerauth.ControlLineage
	if !provisioningRandom(g.Domain[:], g.Cohort[:], g.Generation[:], g.Fences[:], g.Roots.Genesis[:], lineage.Deployment[:], lineage.Instance[:]) {
		return nil, ErrProvisioning
	}
	members := make([]provisioningMember, len(options.Nodes))
	defer func() {
		for _, member := range members {
			clear(member.voting)
			clear(member.origin)
		}
	}()
	for i, input := range options.Nodes {
		member := &members[i]
		member.input = input
		_, member.voting, err = ed25519.GenerateKey(rand.Reader)
		if err == nil {
			_, member.origin, err = ed25519.GenerateKey(rand.Reader)
		}
		if err != nil || !provisioningRandom(member.incarnation[:], member.workload[:]) {
			return nil, ErrProvisioning
		}
		identity := fmt.Sprintf("spiffe://lantern.internal/%s/node-%02d", hex.EncodeToString(lineage.Deployment[:]), i+1)
		member.identity, err = ca.issue([]string{input.ControlOrigin, input.DataOrigin}, identity, options.Now, options.CleanupBy)
		if err != nil {
			return nil, ErrProvisioning
		}
		member.control = peerauth.Member{ID: member.workload, Identity: identity, SPKI: member.identity.spki, Origin: input.ControlOrigin}
		member.data = member.control
		member.data.Origin = input.DataOrigin
		id := uint32(i + 1)
		g.Members = append(g.Members, s2cMember{ID: id, PublicKey: [32]byte(member.voting.Public().(ed25519.PublicKey)), Proposer: true})
		g.Origins = append(g.Origins, s2cOriginDescriptor{ID: id, Member: id, PublicKey: [32]byte(member.origin.Public().(ed25519.PublicKey)),
			Incarnation: member.incarnation, Profile: authorityAdmissionProfileForTime(description), Namespace: uint64(id)})
	}
	trust, state, err := currentJSONGenesisForTime(g, description)
	if err != nil {
		return nil, ErrProvisioning
	}
	profile := peerauth.ControlProfile{Version: 1, Lineage: lineage, NamespaceFormat: keyspace.Version,
		ProtocolScope: trust.scope, TrustDigest: ca.digest}
	for i, member := range members {
		profile.Voters = append(profile.Voters, peerauth.ControlVoter{Workload: member.control, Voter: uint32(i + 1), Key: g.Members[i].PublicKey, Proposer: true})
	}
	issued, expires := options.Now.Add(-5*time.Second), options.Now.Add(peerauth.MaxMembershipLifetime-5*time.Second)
	if expires.After(options.End) {
		expires = options.End
	}
	controlManifest, err := peerauth.SignControlManifest(peerauth.ControlManifest{Version: 1, Profile: profile, IssuedAt: issued, ExpiresAt: expires}, controlKey)
	if err != nil {
		return nil, ErrProvisioning
	}
	current := CurrentProfile{CurrentPublicVersion, g.Domain, g.Cohort, g.Generation, trust.scope, g.TimeProfile, profile.Digest(), state.configuration.Digest()}
	domain := peerauth.Domain{Deployment: lineage.Deployment, NamespaceFormat: keyspace.Version, AuthMode: "oidc", SecurityGeneration: g.Generation, TrustDigest: ca.digest, CurrentProfile: current.Binding()}
	dataManifest := peerauth.Manifest{Version: 1, Domain: domain, IssuedAt: issued, ExpiresAt: expires}
	for _, member := range members {
		dataManifest.Members = append(dataManifest.Members, member.data)
	}
	dataRaw, err := peerauth.SignManifest(dataManifest, dataKey)
	if err != nil {
		return nil, ErrProvisioning
	}
	controlPEM, err := provisioningKeyPEM(controlKey)
	if err != nil {
		return nil, err
	}
	dataPEM, err := provisioningKeyPEM(dataKey)
	if err != nil {
		return nil, err
	}
	dataPublicDER, err := x509.MarshalPKIXPublicKey(dataPublic)
	if err != nil {
		return nil, ErrProvisioning
	}
	dataPublicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: dataPublicDER})
	caPEM, err := provisioningKeyPEM(ca.key)
	if err != nil {
		return nil, err
	}
	bundle := &ProvisioningBundle{Operator: ProvisionedOperator{CAKeyPEM: caPEM, ControlKeyPEM: controlPEM, DataKeyPEM: dataPEM,
		ControlManifest: controlManifest, DataManifest: dataRaw, RootsPEM: append([]byte(nil), ca.rootsPEM...)}}
	for i, member := range members {
		participant, limits, err := provisioningParticipant(g, state, trust, member, uint32(i+1))
		if err != nil {
			return nil, err
		}
		document := currentJSONProvisioningDocument{Version: currentJSONProvisioningVersion, Genesis: g, Participant: participant,
			Membership: currentJSONMembershipDocument{Path: filepath.Join(member.input.StateDir, "control-membership"),
				ManifestFile: filepath.Join(member.input.RenewableDir, "control.json"), PublicKey: controlPublic, Profile: profile, Self: member.control},
			Identity: currentJSONIdentityDocument{Roots: string(ca.rootsPEM), Certificate: string(member.identity.certificatePEM),
				TLSKey: string(member.identity.tlsKeyPEM), VotingKey: member.voting}, OriginKey: member.origin,
			FloorsFile: filepath.Join(member.input.StateDir, "control-floors.json"), ListenAddress: member.input.ControlListen, Limits: limits}
		_, validated, err := currentJSONConfigForTime(document, controlManifest, description, false)
		if err != nil || validated != current {
			return nil, ErrProvisioning
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return nil, ErrProvisioning
		}
		bundle.Nodes = append(bundle.Nodes, ProvisionedNode{Name: member.input.Name, AuthorityJSON: raw,
			RootsPEM: append([]byte(nil), ca.rootsPEM...), CertificatePEM: append([]byte(nil), member.identity.certificatePEM...),
			TLSKeyPEM: append([]byte(nil), member.identity.tlsKeyPEM...), DataManifest: append([]byte(nil), dataRaw...),
			DataOperatorPublicKey: append([]byte(nil), dataPublicPEM...), DataDomain: domain, DataSelf: member.data, Identity: member.control.Identity, CurrentProfile: current})
	}
	return bundle, nil
}

func provisioningKeyPEM(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, ErrProvisioning
	}
	defer clear(der)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func provisioningRandom(values ...[]byte) bool {
	for _, value := range values {
		if _, err := rand.Read(value); err != nil {
			return false
		}
	}
	return true
}

func provisioningTimesValid(options ProvisioningOptions) bool {
	return !options.Now.IsZero() && !options.End.IsZero() && !options.CleanupBy.IsZero() &&
		options.Now.Location() == time.UTC && options.End.Location() == time.UTC && options.CleanupBy.Location() == time.UTC &&
		options.Now.Year() >= 2020 && options.CleanupBy.Year() < 2080 && options.End.After(options.Now.Add(2*peerauth.ClockMargin)) &&
		options.CleanupBy.After(options.End.Add(peerauth.ClockMargin))
}

func validateProvisioningNodes(nodes []ProvisioningNode) error {
	names, origins, states, renewables := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, node := range nodes {
		if !provisioningName(node.Name) || names[node.Name] || !provisioningPath(node.StateDir) || !provisioningPath(node.RenewableDir) ||
			node.StateDir == node.RenewableDir || states[node.StateDir] || renewables[node.RenewableDir] {
			return ErrProvisioning
		}
		names[node.Name], states[node.StateDir], renewables[node.RenewableDir] = true, true, true
		for _, origin := range []string{node.ControlOrigin, node.DataOrigin} {
			if _, err := provisioningOriginHost(origin); err != nil || origins[origin] {
				return ErrProvisioning
			}
			origins[origin] = true
		}
		u, _ := url.Parse(node.ControlOrigin)
		_, port, err := net.SplitHostPort(node.ControlListen)
		if err != nil || port != u.Port() {
			return ErrProvisioning
		}
	}
	for state := range states {
		for renewable := range renewables {
			if state == renewable || strings.HasPrefix(state, renewable+string(filepath.Separator)) || strings.HasPrefix(renewable, state+string(filepath.Separator)) {
				return ErrProvisioning
			}
		}
	}
	return nil
}

func provisioningPath(path string) bool {
	return filepath.IsAbs(path) && path != string(filepath.Separator) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func provisioningName(name string) bool {
	if len(name) == 0 || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
