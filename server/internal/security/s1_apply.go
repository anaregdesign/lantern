package security

import (
	"math"
	"time"
)

type S1Disposition string

const (
	S1Applied           S1Disposition = "applied"
	S1RejectedCAS       S1Disposition = "rejected_cas"
	S1RejectedAdmin     S1Disposition = "rejected_last_admin"
	S1RejectedAuthority S1Disposition = "rejected_authority"
	S1RejectedPurpose   S1Disposition = "rejected_purpose"
	S1RejectedCapacity  S1Disposition = "rejected_capacity"
	S1RejectedInvariant S1Disposition = "rejected_invariant"
)

// CommitRef is the first Apply position/value, not a ballot, a later duplicate
// certificate, earliest choice time, or a current enforcement guarantee.
type CommitRef struct {
	Version                    uint16
	Domain, Cohort, Membership [32]byte
	Slot                       uint64
	Value                      [32]byte
}

type S1ItemOutcome struct {
	Index       int
	Kind        string
	Disposition S1Disposition
}

// OriginalOutcome never changes after first ID ownership, even when current
// authority is revoked. Public disclosure requires separate current admission.
type OriginalOutcome struct {
	id                  FullChangeID
	operation           OperationIdentity
	handoff             [32]byte
	commit              CommitRef
	disposition         S1Disposition
	items               []S1ItemOutcome
	observed, resulting SemanticCut
}

func (o *OriginalOutcome) ID() FullChangeID             { return o.id }
func (o *OriginalOutcome) Operation() OperationIdentity { return o.operation }
func (o *OriginalOutcome) HandoffDigest() [32]byte      { return o.handoff }
func (o *OriginalOutcome) Commit() CommitRef            { return o.commit }
func (o *OriginalOutcome) Disposition() S1Disposition   { return o.disposition }
func (o *OriginalOutcome) Items() []S1ItemOutcome       { return append([]S1ItemOutcome(nil), o.items...) }
func (o *OriginalOutcome) Observed() SemanticCut        { return o.observed }
func (o *OriginalOutcome) Resulting() SemanticCut       { return o.resulting }

// s1VerifiedAuthorization and s1PrefixCertificate are intentionally private.
// There is NO production constructor/verifier in S1. Only future trusted S2
// verification may create them after authentic historical authority, exact
// purpose/final consume and contiguous prefix/checkpoint verification. Tests
// supply explicit fixtures. A caller Boolean is never a certification API.
type s1VerifiedAuthorization struct {
	authentication                                      Authentication
	lineage                                             uint64
	credentialEvidence, purposeEvidence, purposeBinding [32]byte
	consume, credentialDeadline, purposeDeadline        time.Time
}

type S1Handoff struct {
	id            FullChangeID
	operation     OperationIdentity
	origin        [32]byte
	serial        uint64
	authorization *s1VerifiedAuthorization
}

func (h *S1Handoff) digest() [32]byte {
	if h == nil || h.authorization == nil {
		return [32]byte{}
	}
	a := h.authorization
	return s1Digest("handoff", struct {
		ID                                           FullChangeID
		Operation                                    string
		Origin                                       [32]byte
		Serial                                       uint64
		Authentication                               Authentication
		Lineage                                      uint64
		Credential, Purpose, Binding                 [32]byte
		Consume, CredentialDeadline, PurposeDeadline time.Time
	}{h.id, h.operation.canonical, h.origin, h.serial, a.authentication, a.lineage, a.credentialEvidence, a.purposeEvidence, a.purposeBinding, a.consume.UTC(), a.credentialDeadline.UTC(), a.purposeDeadline.UTC()})
}

func (h *S1Handoff) valid(scope SemanticCut) bool {
	if h == nil || h.authorization == nil || !h.id.valid() || h.id.Domain != scope.Domain || h.id.Cohort != scope.Cohort || h.origin == [32]byte{} || h.serial == 0 || !h.operation.reviewed.valid() || h.operation.reviewed.Domain != scope.Domain || h.operation.reviewed.Cohort != scope.Cohort {
		return false
	}
	if _, err := h.operation.command(); err != nil {
		return false
	}
	a := h.authorization
	if a.credentialEvidence == [32]byte{} || a.consume.IsZero() || !a.consume.Before(a.credentialDeadline) || a.authentication.Class != EndUser || h.operation.actor.Kind != OIDCPrincipal || a.lineage == 0 {
		return false
	}
	if a.purposeEvidence != [32]byte{} && (a.purposeBinding != s1PurposeBinding(h.id, h.operation) || !a.consume.Before(a.purposeDeadline)) {
		return false
	}
	return h.digest() != [32]byte{}
}

func s1PurposeBinding(id FullChangeID, o OperationIdentity) [32]byte {
	return s1Digest("purpose", struct {
		ID        FullChangeID
		Operation string
	}{id, o.canonical})
}

type s1PrefixCertificate struct {
	commit            CommitRef
	previous, witness [32]byte
}

// S1CertifiedNext is opaque to other packages. A slot certificate alone is
// insufficient: its predecessor must match this complete installed prefix.
// A zero value never authorizes Apply. A nil handoff is a certified NOOP.
type S1CertifiedNext struct {
	certificate *s1PrefixCertificate
	handoff     *S1Handoff
}

// S1Capacity reserves metadata for every terminal result, including CAS failures.
// Limits are deterministic input contracts; native reservation/accounting is S2.
type S1Capacity struct {
	LedgerEntries         uint32
	RestrictiveEntries    uint32
	ImageBytes            uint32
	RestrictiveImageBytes uint32
}

func (c S1Capacity) valid() bool {
	return c.LedgerEntries > 0 && c.LedgerEntries <= 100000 && c.RestrictiveEntries < c.LedgerEntries && c.ImageBytes > 0 && c.ImageBytes <= MaxImageBytes && c.RestrictiveImageBytes < c.ImageBytes
}

type S1LookupState string

const (
	S1Known      S1LookupState = "known"
	S1Unresolved S1LookupState = "unresolved"
	S1Retired    S1LookupState = "certified_retired"
)

// S1ApplyState owns projection and ledger together. No Store, WAL, clock or
// publication hook appears here. Absence is unresolved, never a cancellation.
type S1ApplyState struct {
	projection     *S1Projection
	ledger         map[FullChangeID]*OriginalOutcome
	membership     [32]byte
	slot           uint64
	prefix         [32]byte
	capacity       S1Capacity
	retiredThrough uint64
}

// S1Retention is an opaque trusted checkpoint input, not a retirement API.
// S2 must verify the barrier and retained-prefix ancestry before constructing a
// nonzero value. S1 never deletes outcomes, advances this floor or uses TTLs.
type S1Retention struct {
	retiredThrough             uint64
	domain, cohort, checkpoint [32]byte
}

func NewS1ApplyState(p *S1Projection, membership [32]byte, capacity S1Capacity, retention S1Retention) (*S1ApplyState, error) {
	if p == nil || !p.cut.valid() || membership == [32]byte{} || !capacity.valid() || len(p.snapshot.image) > int(capacity.ImageBytes) {
		return nil, ErrS1Contract
	}
	if retention.retiredThrough != 0 && (retention.domain != p.cut.Domain || retention.cohort != p.cut.Cohort || retention.checkpoint == [32]byte{}) {
		return nil, ErrS1Contract
	}
	return &S1ApplyState{projection: p, ledger: make(map[FullChangeID]*OriginalOutcome), membership: membership, prefix: s1Digest("initial-prefix", struct {
		Cut            SemanticCut
		Membership     [32]byte
		RetiredThrough uint64
	}{p.cut, membership, retention.retiredThrough}), capacity: capacity, retiredThrough: retention.retiredThrough}, nil
}

func (s *S1ApplyState) Projection() *S1Projection    { return s.projection }
func (s *S1ApplyState) Position() (uint64, [32]byte) { return s.slot, s.prefix }
func (s *S1ApplyState) Lookup(id FullChangeID) (S1LookupState, *OriginalOutcome) {
	if !id.valid() || id.Domain != s.projection.cut.Domain || id.Cohort != s.projection.cut.Cohort {
		return S1Unresolved, nil
	}
	if o, known := s.ledger[id]; known {
		return S1Known, o
	}
	if id.Namespace <= s.retiredThrough {
		return S1Retired, nil
	}
	return S1Unresolved, nil
}

type S1ApplyStatus string

const (
	S1Original     S1ApplyStatus = "original"
	S1Replay       S1ApplyStatus = "replay"
	S1IDConflict   S1ApplyStatus = "id_conflict"
	S1Noop         S1ApplyStatus = "noop"
	S1RetiredInput S1ApplyStatus = "retired"
)

type S1ApplyResult struct {
	State   *S1ApplyState
	Outcome *OriginalOutcome
	Status  S1ApplyStatus
}

func ApplyS1(s *S1ApplyState, next S1CertifiedNext) (S1ApplyResult, error) {
	if s == nil || next.certificate == nil || s.slot == math.MaxUint64 {
		return S1ApplyResult{}, ErrS1Contract
	}
	c := next.certificate
	cut := s.projection.cut
	value := s1Digest("noop", struct{ Domain, Cohort [32]byte }{cut.Domain, cut.Cohort})
	if next.handoff != nil {
		// Historical authenticity/scope precedes ID ownership and lookup.
		if !next.handoff.valid(cut) {
			return S1ApplyResult{}, ErrS1Contract
		}
		value = next.handoff.digest()
	}
	if c.commit.Version != S1Version || c.commit.Domain != cut.Domain || c.commit.Cohort != cut.Cohort || c.commit.Membership != s.membership || c.commit.Slot != s.slot+1 || c.commit.Value != value || c.previous != s.prefix || c.witness == [32]byte{} {
		return S1ApplyResult{}, ErrS1Contract
	}
	advanced := *s
	advanced.slot = c.commit.Slot
	advanced.prefix = s1Digest("prefix", struct {
		Previous [32]byte
		Commit   CommitRef
	}{s.prefix, c.commit})
	if next.handoff == nil {
		return S1ApplyResult{&advanced, nil, S1Noop}, nil
	}
	h := next.handoff
	if original, known := s.ledger[h.id]; known {
		if original.operation != h.operation {
			return S1ApplyResult{&advanced, nil, S1IDConflict}, nil
		}
		return S1ApplyResult{&advanced, original, S1Replay}, nil
	}
	if h.id.Namespace <= s.retiredThrough {
		return S1ApplyResult{&advanced, nil, S1RetiredInput}, nil
	}
	// Without a terminal-record reservation even refusal cannot be promised.
	// Leave prefix/state untouched; S2 must reserve before certifying acceptance.
	if len(s.ledger) >= int(s.capacity.LedgerEntries) {
		return S1ApplyResult{}, ErrControlReserve
	}
	command, _ := h.operation.command()
	var assessment S1Assessment
	var err error
	if h.operation.reviewed != cut {
		err = ErrRevisionConflict
	} else if !s1CurrentAuthority(s.projection, h, command) {
		err = ErrPermissionDenied
	} else {
		assessment, err = AssessS1(s.projection, h.operation)
	}
	if err == nil && assessment.NeedsPurpose && h.authorization.purposeEvidence == [32]byte{} {
		err = ErrOperationAuthorization
	}
	// Rejected/unknown-effect work also needs ordinary metadata reservation;
	// repeated terminal refusals cannot consume the restrictive reserve.
	if len(s.ledger) >= int(s.capacity.LedgerEntries-s.capacity.RestrictiveEntries) && (err != nil || !assessment.ProvenNonexpanding) {
		return S1ApplyResult{}, ErrControlReserve
	}
	if err == nil {
		limit := s.capacity.ImageBytes
		if !assessment.ProvenNonexpanding {
			limit -= s.capacity.RestrictiveImageBytes
		}
		if len(assessment.Next.snapshot.image) > int(limit) || !assessment.ProvenNonexpanding && len(s.ledger) >= int(s.capacity.LedgerEntries-s.capacity.RestrictiveEntries) {
			err = ErrControlReserve
		}
	}
	disposition := S1Applied
	if err != nil {
		disposition = s1Disposition(err)
	} else {
		advanced.projection = assessment.Next
	}
	items := []S1ItemOutcome{{0, command.Kind, disposition}}
	if command.Kind == S1Management {
		items = make([]S1ItemOutcome, len(command.Changes))
		for i, change := range command.Changes {
			items[i] = S1ItemOutcome{i, change.Kind, disposition}
		}
	}
	outcome := &OriginalOutcome{id: h.id, operation: h.operation, handoff: h.digest(), commit: c.commit, disposition: disposition, items: items, observed: h.operation.reviewed, resulting: advanced.projection.cut}
	advanced.ledger = make(map[FullChangeID]*OriginalOutcome, len(s.ledger)+1)
	for id, o := range s.ledger {
		advanced.ledger[id] = o
	}
	advanced.ledger[h.id] = outcome
	return S1ApplyResult{&advanced, outcome, S1Original}, nil
}

func s1CurrentAuthority(p *S1Projection, h *S1Handoff, c S1Command) bool {
	actor, a := h.operation.actor, h.authorization
	issuer, known := p.snapshot.Issuer(actor.Issuer)
	if !known || !issuer.Enabled || issuer.Deleted || issuer.ConfigRevision != a.authentication.IssuerConfigRevision || a.lineage != p.lineage[actor] {
		return false
	}
	access, active := p.snapshot.AccessFor(actor)
	if !active {
		return false
	}
	switch a.authentication.Provenance {
	case RFC9068Bearer:
		if p.snapshot.BearerActor(actor) != EndUser || a.authentication.SessionDigest != "" || c.Kind != S1Management {
			return false
		}
	case BrowserCode:
		if digest := a.authentication.SessionDigest; digest != "" {
			session, exists := p.snapshot.Session(digest)
			if !exists || session.Revoked || session.Identity != actor || session.IssuerConfigRevision != issuer.ConfigRevision {
				return false
			}
		}
	default:
		return false
	}
	return c.Kind != S1Management || access.AllowsGlobal(SecurityManage)
}
