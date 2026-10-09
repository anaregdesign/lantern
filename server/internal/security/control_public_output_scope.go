package security

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
)

// PublicOutputResource describes the already selected logical service view.
// Exact keys, Head-owned edges, and filtered collection ranges are distinct;
// collection authority never turns into a whole-domain/global grant.
type PublicOutputResource struct {
	Kind                    PublicOutputResourceKind
	Actions                 []Action
	Key, Tail, Head, Prefix string
	Provenance              [32]byte
}
type PublicOutputResourceKind uint8

const (
	OutputKey PublicOutputResourceKind = iota + 1
	OutputEdge
	OutputVertexCollection
	OutputEdgeCollection
	OutputEmpty
	OutputReceipt
)

type currentOutputGrant struct {
	kind           string
	admission      *currentPublicAdmission
	projection     *Snapshot
	resource       [32]byte
	cut            SemanticCut
	issuer         string
	issuerRevision uint64
}

func bindCurrentGrant(ctx context.Context, o *CurrentAuthority, g currentOutputGrant) error {
	r, err := currentRequest(ctx, o)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return ErrAuthorityUnavailable
	}
	r.grant = g
	return nil
}

func (a *Admission) currentOutput(ctx context.Context, kind string, resource [32]byte) error {
	if a == nil || a.current == nil {
		return nil
	}
	if resource == [32]byte{} || a.Check(ctx, time.Time{}) != nil {
		return ErrAuthorityUnavailable
	}
	return bindCurrentGrant(ctx, a.current.owner, currentOutputGrant{kind: kind, admission: a.current, projection: a.current.snapshot, resource: resource})
}

func (a *Admission) BindCurrentSelfOutput(ctx context.Context) error {
	if a == nil || a.current == nil {
		return nil
	}
	return a.currentOutput(ctx, "self", s2cHash("public-self-output", a.Identity()))
}

func (a *Admission) BindCurrentGlobalOutput(ctx context.Context, action Action) error {
	if a == nil || a.current == nil {
		return nil
	}
	if kind, known := actionResource(action); !known || kind != GlobalResource || !a.Access().AllowsGlobal(action) {
		return ErrPermissionDenied
	}
	return a.currentOutput(ctx, "global", s2cHash("public-global-output", action))
}

// Source is the canonical request/projection digest compiled by the trusted
// service adapter. The opaque result owns a complete immutable snapshot and
// typed selected resources; no caller Boolean or reusable token is admitted.
func (a *Admission) BindCurrentResourceOutput(ctx context.Context, source [32]byte, resources []PublicOutputResource) error {
	if a == nil || a.current == nil {
		return nil
	}
	r, err := currentRequest(ctx, a.current.owner)
	if err != nil {
		return err
	}
	if source == [32]byte{} || len(resources) == 0 || len(resources) > 1<<18 {
		return ErrPermissionDenied
	}
	bytes := 0
	frozen := make([]PublicOutputResource, len(resources))
	for i, resource := range resources {
		bytes += len(resource.Key) + len(resource.Tail) + len(resource.Head) + len(resource.Prefix) + 32*len(resource.Actions)
		if bytes > r.owner.limits.ReadBytes*4 || len(resource.Actions) == 0 && resource.Kind != OutputEmpty || len(resource.Actions) > 32 {
			return ErrControlReserve
		}
		for _, action := range resource.Actions {
			if _, known := actionResource(action); !known {
				return ErrPermissionDenied
			}
		}
		switch resource.Kind {
		case OutputEmpty:
			if resource.Key != "" || resource.Tail != "" || resource.Head != "" || resource.Prefix != "" || len(resource.Actions) != 0 || resource.Provenance != [32]byte{} {
				return ErrPermissionDenied
			}
		case OutputReceipt:
			if resource.Provenance == [32]byte{} || resource.Tail != "" || resource.Prefix != "" || resource.Key == "" && resource.Head != "" {
				return ErrPermissionDenied
			}
			for _, action := range resource.Actions {
				allowed := a.Access().AllowsAll(action)
				if resource.Key != "" && resource.Head == "" {
					allowed = a.Access().Allows(action, resource.Key)
				} else if resource.Head != "" {
					allowed = a.Access().AllowsEdgeAction(action, resource.Key, resource.Head)
				}
				if !allowed {
					return ErrPermissionDenied
				}
			}
		case OutputKey:
			if resource.Key == "" || resource.Tail != "" || resource.Head != "" || resource.Prefix != "" {
				return ErrPermissionDenied
			}
			for _, action := range resource.Actions {
				if !a.Access().Allows(action, resource.Key) {
					return ErrPermissionDenied
				}
			}
		case OutputEdge:
			if resource.Tail == "" || resource.Head == "" || resource.Key != "" || resource.Prefix != "" {
				return ErrPermissionDenied
			}
			for _, action := range resource.Actions {
				if !a.Access().AllowsEdgeAction(action, resource.Tail, resource.Head) {
					return ErrPermissionDenied
				}
			}
		case OutputVertexCollection, OutputEdgeCollection:
			if resource.Key != "" || resource.Tail != "" || resource.Head != "" {
				return ErrPermissionDenied
			}
			scope := a.Access().Scope(resource.Actions...)
			if resource.Kind == OutputEdgeCollection {
				scope = a.Access().EdgeCandidateScope(resource.Actions...)
			}
			if scope.Within(resource.Prefix).Empty() {
				return ErrPermissionDenied
			}
		default:
			return ErrPermissionDenied
		}
		frozen[i] = resource
		frozen[i].Actions = slices.Clone(resource.Actions)
	}
	return a.currentOutput(ctx, "resources", s2cHash("public-selected-resources", struct {
		Source    [32]byte
		Resources []PublicOutputResource
	}{source, frozen}))
}

func (o *CurrentAuthority) BindOriginalOutput(ctx context.Context, a *Admission, id FullChangeID, intent [32]byte) error {
	if err := o.CheckOriginalDisclosure(ctx, a, id, intent); err != nil {
		return err
	}
	return a.currentOutput(ctx, "original", s2cHash("public-original-output", struct {
		ID     FullChangeID
		Intent [32]byte
	}{id, intent}))
}

func (o *CurrentAuthority) BindIssuedSessionOutput(ctx context.Context, a *Admission, s Session) error {
	if err := o.ConfirmIssuedSession(ctx, a, s); err != nil {
		return err
	}
	return a.currentOutput(ctx, "issued-session", s2cHash("public-issued-session-output", s))
}

func (o *CurrentAuthority) BindLoginOutput(ctx context.Context, cut SemanticCut, issuer string) error {
	i, current, err := o.LoginIssuer(ctx, issuer)
	if err != nil || current != cut {
		return ErrAuthorityUnavailable
	}
	return bindCurrentGrant(ctx, o, currentOutputGrant{kind: "login", cut: cut, issuer: issuer, issuerRevision: i.ConfigRevision, resource: s2cHash("public-login-output", struct {
		Cut      SemanticCut
		Issuer   string
		Revision uint64
	}{cut, issuer, i.ConfigRevision})})
}

type CurrentPublicMetadata uint8

const (
	CurrentCapabilitiesMetadata CurrentPublicMetadata = iota + 1
	CurrentHealthMetadata
	CurrentCORSMetadata
	CurrentLocalCookieClear
	CurrentGenericFailure
	CurrentPurposeReturn
)

// These fixed schemas contain no principal, issuer list, cookie issuance or
// original result. Callers must construct the named schema, not arbitrary data.
func (o *CurrentAuthority) BindPublicMetadata(ctx context.Context, kind CurrentPublicMetadata) error {
	r, err := currentRequest(ctx, o)
	if err != nil {
		return err
	}
	path := strings.TrimPrefix(r.path, "/browser")
	valid := false
	switch kind {
	case CurrentCapabilitiesMetadata:
		valid = path == "/graph.v1.LanternSecurityService/GetAuthCapabilities"
	case CurrentHealthMetadata:
		valid = path == "/grpc.health.v1.Health/Check"
	case CurrentCORSMetadata:
		valid = r.method == http.MethodOptions
	case CurrentLocalCookieClear:
		valid = path == "/auth/logout"
	case CurrentGenericFailure:
		valid = true
	case CurrentPurposeReturn:
		valid = strings.HasPrefix(path, "/auth/callback/")
	}
	if !valid {
		return ErrPermissionDenied
	}
	return bindCurrentGrant(ctx, o, currentOutputGrant{kind: "public", resource: s2cHash("public-fixed-schema", kind)})
}

func (r *currentOutputRequest) authorize(ctx context.Context, g currentOutputGrant) (authorityCurrentTime, error) {
	if ctx.Err() != nil || g.kind == "" || g.resource == [32]byte{} {
		return authorityCurrentTime{}, ErrAuthorityUnavailable
	}
	if g.kind == "public" || g.kind == "public-failure" {
		return authorityCurrentTime{}, nil
	}
	o := r.owner.owner.origin
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.chosen != nil {
		return authorityCurrentTime{}, ErrAuthorityUnavailable
	}
	if g.kind == "login" || g.kind == "login-catalog" {
		if k.replayState.projection.cut != g.cut {
			return authorityCurrentTime{}, ErrPermissionDenied
		}
		if g.kind == "login" {
			i, known := k.replayState.projection.snapshot.Issuer(g.issuer)
			if !known || !i.Enabled || i.Deleted || i.ConfigRevision != g.issuerRevision {
				return authorityCurrentTime{}, ErrPermissionDenied
			}
		}
		start, expiry, err := o.eligibilityLocked(ctx)
		if err != nil {
			return authorityCurrentTime{}, err
		}
		now, _, err := o.network.receiver.currentLocked()
		if err != nil || time.Unix(0, int64(now.utc.low)).Before(start) || !time.Unix(0, int64(now.utc.high)).Before(expiry) {
			return authorityCurrentTime{}, ErrAuthorityUnavailable
		}
		return now, nil
	}
	if g.admission == nil || g.admission.owner != r.owner.owner || g.projection != k.replayState.projection.snapshot {
		return authorityCurrentTime{}, ErrPermissionDenied
	}
	// The typed resource compilation used this identical immutable snapshot.
	// Full-cut/lineage/credential/renewal are checked again at the native event.
	return o.authorizeCurrentCredentialLocked(ctx, g.admission.credential)
}
