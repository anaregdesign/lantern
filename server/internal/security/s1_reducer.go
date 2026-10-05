package security

import (
	"errors"
	"math"
	"sort"
)

// S1Projection is an immutable full typed closure. Session revocation floors
// survive absent sessions; they are not inferred from cookie expiry/deletion.
type S1Projection struct {
	snapshot *Snapshot
	lineage  map[Identity]uint64
	cut      SemanticCut
}

// NewS1Projection prepares a pure initial image. Authentic checkpoint/fence
// verification is an S2 boundary; this does not certify the input for serving.
func NewS1Projection(image Image, limits PolicyLimits, domain, cohort, fences [32]byte, generation [16]byte) (*S1Projection, error) {
	snapshot, err := CompileImage(image, limits)
	if err != nil {
		return nil, err
	}
	p := &S1Projection{snapshot: snapshot, lineage: make(map[Identity]uint64)}
	for identity := range snapshot.principals {
		if identity.Kind == OIDCPrincipal {
			p.lineage[identity] = 1
		}
	}
	p.cut = SemanticCut{Version: S1Version, Domain: domain, Cohort: cohort, Generation: generation, Sequence: 1, Fences: fences}
	p.cut.Projection = p.projectionDigest()
	p.cut.Frontier = s1Digest("initial-frontier", p.cut.Projection)
	if !p.cut.valid() {
		return nil, ErrS1Contract
	}
	return p, nil
}

func (p *S1Projection) Cut() SemanticCut                        { return p.cut }
func (p *S1Projection) Image() Image                            { return p.snapshot.Image() }
func (p *S1Projection) SessionLineage(identity Identity) uint64 { return p.lineage[identity] }

func (p *S1Projection) projectionDigest() [32]byte {
	type entry struct {
		Identity Identity
		Epoch    uint64
	}
	entries := make([]entry, 0, len(p.lineage))
	for identity, epoch := range p.lineage {
		entries = append(entries, entry{identity, epoch})
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].Identity, entries[j].Identity
		if a.Issuer != b.Issuer {
			return a.Issuer < b.Issuer
		}
		return a.Subject < b.Subject
	})
	return s1Digest("projection", struct {
		Image   Image
		Lineage []entry
	}{p.Image(), entries})
}

// S1Assessment separates high-impact purpose from the restrictive reserve.
// A data-only expansion is ordinary, but cannot consume restrictive capacity.
type S1Assessment struct {
	Next               *S1Projection
	NeedsPurpose       bool
	ProvenNonexpanding bool
}

func AssessS1(p *S1Projection, operation OperationIdentity) (S1Assessment, error) {
	if p == nil || operation.reviewed != p.cut {
		return S1Assessment{}, ErrRevisionConflict
	}
	command, err := operation.command()
	if err != nil {
		return S1Assessment{}, err
	}
	if err = validateS1Command(command, p.snapshot.limits); err != nil {
		return S1Assessment{}, err
	}
	image := p.Image()
	next := &S1Projection{lineage: make(map[Identity]uint64, len(p.lineage)), cut: p.cut}
	for id, epoch := range p.lineage {
		next.lineage[id] = epoch
	}
	needsPurpose, trustChange := false, false
	bump := func(identity Identity) error {
		if identity.Kind != OIDCPrincipal {
			return nil
		}
		if next.lineage[identity] == math.MaxUint64 {
			return ErrS1Contract
		}
		next.lineage[identity]++
		return nil
	}
	switch command.Kind {
	case S1Management:
		if err = applyManagementChanges(&image, command.Changes); err != nil {
			return S1Assessment{}, err
		}
		if !sameEnvOwned(p.Image(), image) {
			return S1Assessment{}, ErrBootstrapLocked
		}
		for _, change := range command.Changes {
			switch change.Kind {
			case PutIssuer, DisableIssuer, DeleteIssuer:
				needsPurpose, trustChange = true, true
				url := change.IssuerURL
				if change.Issuer != nil {
					url = change.Issuer.URL
				}
				for id := range next.lineage {
					if id.Issuer == url {
						if err = bump(id); err != nil {
							return S1Assessment{}, err
						}
						revokePrincipalSessions(&image, id)
					}
				}
			case DeletePrincipal, RevokeSessions:
				if err = bump(*change.Identity); err != nil {
					return S1Assessment{}, err
				}
			case PutPrincipal:
				if change.Identity.Kind == OIDCPrincipal {
					if _, known := next.lineage[*change.Identity]; !known {
						next.lineage[*change.Identity] = 1
					}
					if change.State == Suspended {
						if err = bump(*change.Identity); err != nil {
							return S1Assessment{}, err
						}
					}
				}
			}
		}
	case S1IssueSession:
		session := *command.Session
		issuer, known := p.snapshot.Issuer(session.Identity.Issuer)
		if session.Identity != operation.actor || !known || !issuer.Enabled || issuer.Deleted || issuer.ConfigRevision != session.IssuerConfigRevision || command.SessionLineage != p.lineage[session.Identity] {
			return S1Assessment{}, ErrPermissionDenied
		}
		if _, active := p.snapshot.AccessFor(session.Identity); !active {
			return S1Assessment{}, ErrPermissionDenied
		}
		if _, exists := p.snapshot.Session(session.Digest); exists {
			return S1Assessment{}, ErrChangeConflict
		}
		found := command.ReplacesDigest == ""
		for i := range image.Sessions {
			old := &image.Sessions[i]
			if old.Digest == command.ReplacesDigest {
				if old.Identity != session.Identity || old.Revoked || old.IssuerConfigRevision != issuer.ConfigRevision || session.CreatedAt.Before(old.CreatedAt) || !session.CreatedAt.Before(old.ExpiresAt) {
					return S1Assessment{}, ErrPermissionDenied
				}
				old.Revoked, found = true, true
			}
		}
		if !found {
			return S1Assessment{}, ErrPermissionDenied
		}
		for i := range image.Principals {
			if image.Principals[i].Identity == session.Identity {
				image.Principals[i].HumanIssuerConfigRevision = issuer.ConfigRevision
			}
		}
		image.Sessions = append(image.Sessions, session)
	case S1RevokeSession:
		if command.SessionLineage != p.lineage[operation.actor] {
			return S1Assessment{}, ErrPermissionDenied
		}
		found := false
		for i := range image.Sessions {
			if image.Sessions[i].Digest == command.SessionDigest {
				if image.Sessions[i].Identity != operation.actor {
					return S1Assessment{}, ErrPermissionDenied
				}
				image.Sessions[i].Revoked, found = true, true
			}
		}
		if !found {
			return S1Assessment{}, ErrPermissionDenied
		}
	default:
		return S1Assessment{}, ErrS1Contract
	}
	next.snapshot, err = compileImage(image, p.snapshot.limits, p.snapshot)
	if err != nil {
		return S1Assessment{}, err
	}
	for identity := range next.snapshot.principals {
		before, wasActive := p.snapshot.AccessFor(identity)
		after, isActive := next.snapshot.AccessFor(identity)
		if isActive && after.AllowsGlobal(SecurityManage) && (!wasActive || !before.AllowsGlobal(SecurityManage)) {
			needsPurpose = true
		}
	}
	if p.cut.Sequence == math.MaxUint64 {
		return S1Assessment{}, ErrS1Contract
	}
	next.cut.Sequence++
	next.cut.Previous = s1Digest("cut", p.cut)
	next.cut.Projection = next.projectionDigest()
	next.cut.Frontier = s1Digest("semantic-event", struct {
		Previous  [32]byte
		Operation string
	}{p.cut.Frontier, operation.canonical})
	nonexpanding := !trustChange && command.Kind != S1IssueSession && s1PermissionsSubset(p.snapshot, next.snapshot)
	return S1Assessment{next, needsPurpose, nonexpanding}, nil
}

func s1PermissionsSubset(before, after *Snapshot) bool {
	for identity := range after.principals {
		a, active := after.AccessFor(identity)
		if !active {
			continue
		}
		b, wasActive := before.AccessFor(identity)
		for _, action := range []Action{SecurityManage, OperationsRead, SchemaRead} {
			if a.AllowsGlobal(action) && (!wasActive || !b.AllowsGlobal(action)) {
				return false
			}
		}
		for _, action := range []Action{VertexRead, VertexWrite, VertexDelete, Query, CDCIdentity, CDCValue, Export, ReceiptRead} {
			var scope *Scope
			if wasActive {
				scope = b.Scope(action)
			}
			if !a.Scope(action).IsSubsetOf(scope) {
				return false
			}
		}
	}
	return true
}

func s1Disposition(err error) S1Disposition {
	switch {
	case errors.Is(err, ErrRevisionConflict):
		return S1RejectedCAS
	case errors.Is(err, ErrLastAdministrator):
		return S1RejectedAdmin
	case errors.Is(err, ErrPermissionDenied):
		return S1RejectedAuthority
	case errors.Is(err, ErrOperationAuthorization):
		return S1RejectedPurpose
	case errors.Is(err, ErrControlReserve):
		return S1RejectedCapacity
	default:
		return S1RejectedInvariant
	}
}
