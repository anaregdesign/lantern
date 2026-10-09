package security

import (
	"encoding/json"
	"errors"
)

const (
	s2cVersion            uint16 = 1
	s2cMaxHistoricalBytes uint64 = 8 << 20
	s2cMaxHeaderBytes     uint64 = 64 << 10
	s2cMaxProofBytes      uint64 = 64 << 10
	s2cMaxPayloadBytes    uint64 = (32 << 20) - 36
)

var errS2CTrust = errors.New("invalid S2-C independent bootstrap")

// Common representability limits, never recipient disk or path policy.
type s2cBounds struct {
	HistoricalBytes uint64
	HeaderBytes     uint64
	ProofBytes      uint64
	PayloadBytes    uint64
	Capsule         s2CapsuleLimits
}

func (b s2cBounds) valid() bool {
	return b.HistoricalBytes > 0 && b.HistoricalBytes <= s2cMaxHistoricalBytes && b.HeaderBytes > 0 && b.HeaderBytes <= s2cMaxHeaderBytes && b.ProofBytes > 0 && b.ProofBytes <= s2cMaxProofBytes && b.PayloadBytes > 0 && b.PayloadBytes <= s2cMaxPayloadBytes && b.Capsule.valid()
}

type s2cMember struct {
	ID        uint32
	PublicKey [32]byte
	Proposer  bool
}

// These independently configured contracts identify what the trusted origin
// asserts. They do not certify a production credential verifier or clock.
type s2cAdmissionProfile struct {
	Version            uint16
	BrowserCode        bool
	HumanBearer        bool
	CredentialContract [32]byte
	ConsumeContract    [32]byte
	PurposeContract    [32]byte
}

func (p s2cAdmissionProfile) valid() bool {
	if p.Version == authorityAdmissionVersion {
		return p == authorityAdmissionProfile()
	}
	return p.Version == s2cVersion && (p.BrowserCode || p.HumanBearer) && p.CredentialContract != [32]byte{} && p.ConsumeContract != [32]byte{} && p.PurposeContract != [32]byte{}
}

func (p s2cAdmissionProfile) digest() [32]byte { return s2cHash("admission-profile", p) }

type s2cOriginDescriptor struct {
	ID          uint32
	Member      uint32
	PublicKey   [32]byte
	Incarnation [16]byte
	Profile     s2cAdmissionProfile
	// Absent in the historical v1 encoding. Current profiles independently pin
	// one retry namespace; local requests cannot supply a replacement namespace.
	Namespace uint64 `json:",omitempty"`
}

type s2cBootstrap struct {
	Genesis *s2TrustedGenesis
	Members []s2cMember
	Origins []s2cOriginDescriptor
	Bounds  s2cBounds
}

// Owns detached immutable bootstrap inputs. Decoded data cannot construct it.
// All accessors return value copies; the private genesis never escapes the
// composite participant's package boundary.
type s2cTrust struct {
	scope, memberSet [32]byte
	genesis          *s2TrustedGenesis
	members          []s2cMember
	origins          []s2cOriginDescriptor
	bounds           s2cBounds
	originDigests    map[uint32][32]byte
}

type s2cBasicScope struct {
	Domain, Cohort [32]byte
	Generation     [16]byte
	Fences         [32]byte
}

func s2cHash(label string, value any) [32]byte {
	b, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}
	}
	return s2LocalHash("lantern/security/s2c/"+label+"\x00\x01", b)
}

func s2cMemberSetDigest(members []s2cMember) ([32]byte, error) {
	if len(members) < 3 || len(members) > 31 || len(members)%2 == 0 {
		return [32]byte{}, errS2CTrust
	}
	keys := make(map[[32]byte]bool, len(members))
	proposers := 0
	for i, m := range members {
		if m.ID == 0 || !s2cValidPublicKey(m.PublicKey) || i > 0 && members[i-1].ID >= m.ID || keys[m.PublicKey] {
			return [32]byte{}, errS2CTrust
		}
		keys[m.PublicKey] = true
		if m.Proposer {
			proposers++
		}
	}
	if proposers == 0 {
		return [32]byte{}, errS2CTrust
	}
	return s2cHash("member-set", members), nil
}

func newS2CTrust(b s2cBootstrap) (*s2cTrust, error) {
	membership, err := s2cMemberSetDigest(b.Members)
	if err != nil || !b.Bounds.valid() || b.Genesis == nil || b.Genesis.state == nil || b.Genesis.state.projection == nil || b.Genesis.state.projection.snapshot == nil || len(b.Origins) == 0 || len(b.Origins) > 31 {
		return nil, errS2CTrust
	}
	g := b.Genesis.state
	if g.slot != 0 || len(g.ledger) != 0 || g.membership != membership || g.projection.cut.Sequence != 1 || g.projection.cut.Previous != [32]byte{} {
		return nil, errS2CTrust
	}
	// Recompute the initial prefix from the independently supplied exact state.
	retention := S1Retention{g.retiredThrough, g.projection.cut.Domain, g.projection.cut.Cohort, b.Genesis.roots.Retirement}
	initial, err := NewS1ApplyState(g.projection, membership, g.configuration.Capacity, retention)
	if err != nil || initial.prefix != g.prefix || initial.configuration != g.configuration {
		return nil, errS2CTrust
	}
	encoded, _, err := s2EncodeCapsule(g, b.Genesis.roots, b.Bounds.Capsule)
	if err != nil {
		return nil, errS2CTrust
	}
	detached, err := s2DetachState(g)
	if err != nil {
		return nil, errS2CTrust
	}
	t := &s2cTrust{memberSet: membership, genesis: &s2TrustedGenesis{detached, b.Genesis.roots}, members: append([]s2cMember(nil), b.Members...), origins: append([]s2cOriginDescriptor(nil), b.Origins...), bounds: b.Bounds, originDigests: make(map[uint32][32]byte)}
	c := g.projection.cut
	basic := s2cBasicScope{c.Domain, c.Cohort, c.Generation, c.Fences}
	keys := make(map[[32]byte]bool, len(t.origins))
	namespaces := make(map[uint64]bool, len(t.origins))
	for i, o := range t.origins {
		_, member := t.member(o.Member)
		if o.ID == 0 || !member || !s2cValidPublicKey(o.PublicKey) || o.Incarnation == [16]byte{} || !o.Profile.valid() || keys[o.PublicKey] || i > 0 && t.origins[i-1].ID >= o.ID {
			return nil, errS2CTrust
		}
		if o.Profile.Version != t.origins[0].Profile.Version {
			return nil, errS2CTrust
		}
		if o.Profile.Version == authorityAdmissionVersion {
			if o.Namespace == 0 || namespaces[o.Namespace] || o.Namespace <= g.retiredThrough {
				return nil, errS2CTrust
			}
			for _, m := range t.members {
				if m.PublicKey == o.PublicKey {
					return nil, errS2CTrust
				}
			}
			namespaces[o.Namespace] = true
		} else if o.Namespace != 0 {
			return nil, errS2CTrust
		}
		keys[o.PublicKey] = true
		// OriginDigest excludes the complete scope to avoid a hash cycle.
		t.originDigests[o.ID] = s2cHash("origin", struct {
			Basic   s2cBasicScope
			Members [32]byte
			Origin  s2cOriginDescriptor
		}{basic, membership, o})
	}
	t.scope = s2cHash("protocol-scope", struct {
		Version       uint16
		Basic         s2cBasicScope
		Genesis       [32]byte
		Configuration S1ExecutionConfig
		Members       [32]byte
		Origins       []s2cOriginDescriptor
		Bounds        s2cBounds
	}{s2cVersion, basic, s2LocalCapsuleDigest(encoded), g.configuration, membership, t.origins, b.Bounds})
	return t, nil
}

func (t *s2cTrust) member(id uint32) (s2cMember, bool) {
	if t != nil {
		for _, m := range t.members {
			if m.ID == id {
				return m, true
			}
		}
	}
	return s2cMember{}, false
}

func (t *s2cTrust) origin(id uint32) (s2cOriginDescriptor, bool) {
	if t != nil {
		for _, o := range t.origins {
			if o.ID == id {
				return o, true
			}
		}
	}
	return s2cOriginDescriptor{}, false
}

func (t *s2cTrust) majority() int { return len(t.members)/2 + 1 }

func (t *s2cTrust) noopValue() [32]byte {
	c := t.genesis.state.projection.cut
	return s1Digest("noop", struct{ Domain, Cohort [32]byte }{c.Domain, c.Cohort})
}
