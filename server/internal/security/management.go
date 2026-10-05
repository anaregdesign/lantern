package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var ErrPermissionDenied = errors.New("security management permission denied")
var ErrRecentAuthentication = errors.New("recent authentication required")
var ErrControlReserve = errors.New("security control capacity is reserved for revocation")
var ErrOperationAuthorization = errors.New("reviewed operation authentication required")

const RecentAuthenticationLifetime = 5 * time.Minute
const ordinaryImageMaxBytes = 3 << 20

// ManagementRequest is trusted Server composition input. Actor, times and
// serving-authority checks come from verified authentication, never client JSON.
// Expected revision and change ID identify the exact authorized client command.
type ManagementRequest struct {
	ExpectedRevision uint64
	ChangeID         [16]byte
	Actor            Identity
	AuthTime         time.Time
	Now              time.Time
	Changes          []Change
	Authentication   Authentication
	Admission        *Admission
	Clock            func() time.Time
	Authorize        func(ManagementBinding, time.Time) error
}

// ManagementBinding includes the exact fixed-writer cut and issuer trust.
// It is proof context, never part of the retained v1 business intent encoding.
type ManagementBinding struct {
	Actor                Identity
	ChangeID             [16]byte
	IntentDigest         [32]byte
	Generation           [16]byte
	ExpectedRevision     uint64
	ExpectedDigest       [32]byte
	Writer               [32]byte
	IssuerConfigRevision uint64
}
type PreparedManagement struct {
	Binding               ManagementBinding
	AuthorizationRequired bool
	Retained              bool
	Result                ChangeResult
}

// managementIntent preserves the original v1 bytes across authentication and
// proof changes. Do not marshal ManagementRequest: times/proofs are not intent.
func (s *Store) managementIntent(request ManagementRequest) ([32]byte, bool, error) {
	if request.ChangeID == [16]byte{} || request.ExpectedRevision == 0 || len(request.Changes) == 0 || len(request.Changes) > MaxTransactionChanges {
		return [32]byte{}, false, ErrInvalidImage
	}
	roles := make([]Role, 0, len(request.Changes))
	for _, change := range request.Changes {
		if change.Role != nil {
			roles = append(roles, *change.Role)
		}
	}
	if err := validateRoleBounds(roles, s.limits); err != nil {
		return [32]byte{}, false, err
	}
	seen, urgent := make(map[string]bool, len(request.Changes)), true
	for _, change := range request.Changes {
		if err := change.validate(); err != nil {
			return [32]byte{}, false, err
		}
		target := change.target()
		if seen[target] {
			return [32]byte{}, false, ErrInvalidImage
		}
		seen[target] = true
		// This capacity reserve is independent of effective authority expansion.
		switch change.Kind {
		case DisableIssuer, DeleteIssuer, DeleteRole, DeletePrincipal, DeleteAssignment, RevokeSessions:
		case PutPrincipal:
			urgent = urgent && change.State == Suspended
		default:
			urgent = false
		}
	}
	intent, err := json.Marshal(struct {
		Version  int
		Expected uint64
		Actor    Identity
		Changes  []Change
	}{1, request.ExpectedRevision, request.Actor, request.Changes})
	if err != nil || len(intent) > MaxImageBytes {
		return [32]byte{}, false, ErrInvalidImage
	}
	return sha256.Sum256(intent), urgent, nil
}

func (s *Store) prepareManagementLocked(ctx context.Context, request ManagementRequest, digest [32]byte) (PreparedManagement, Image, error) {
	if s.faulted.Load() {
		return PreparedManagement{}, Image{}, ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return PreparedManagement{}, Image{}, err
	}
	current := s.current.Load()
	if current == nil {
		return PreparedManagement{}, Image{}, ErrStoreUnavailable
	}
	if request.Now.IsZero() || request.AuthTime.After(request.Now) {
		return PreparedManagement{}, Image{}, ErrInvalidImage
	}
	if request.Admission != nil {
		if request.Admission.Identity() != request.Actor {
			return PreparedManagement{}, Image{}, ErrPermissionDenied
		}
		if err := request.Admission.CheckManagement(ctx, current, request.Now); err != nil {
			return PreparedManagement{}, Image{}, err
		}
	} else if err := current.snapshot.checkHumanAuthentication(request.Actor, request.Authentication, request.Now); err != nil {
		return PreparedManagement{}, Image{}, err
	}
	access, active := current.snapshot.AccessFor(request.Actor)
	if !active || !access.AllowsGlobal(SecurityManage) {
		return PreparedManagement{}, Image{}, ErrPermissionDenied
	}
	issuer, _ := current.snapshot.Issuer(request.Actor.Issuer)
	prepared := PreparedManagement{Binding: ManagementBinding{Actor: request.Actor, ChangeID: request.ChangeID, IntentDigest: digest, Generation: current.generation, ExpectedRevision: request.ExpectedRevision, ExpectedDigest: current.digest, Writer: current.writer, IssuerConfigRevision: issuer.ConfigRevision}}
	// Current credential/permission checks precede retained retry; new approval
	// does not. Original results never depend on a new proof's bytes or time.
	if record, known := s.changes[request.ChangeID]; known {
		if record.expected != request.ExpectedRevision || record.intentDigest != digest {
			return PreparedManagement{}, Image{}, ErrChangeConflict
		}
		prepared.Retained, prepared.Result = true, record.result
		prepared.Result.Replayed = true
		return prepared, Image{}, nil
	}
	if request.ExpectedRevision != current.sequence {
		return PreparedManagement{}, Image{}, ErrRevisionConflict
	}
	image := current.snapshot.Image()
	if err := applyManagementChanges(&image, request.Changes); err != nil {
		return PreparedManagement{}, Image{}, err
	}
	if !sameEnvOwned(current.snapshot.Image(), image) {
		return PreparedManagement{}, Image{}, ErrBootstrapLocked
	}
	next, err := compileImage(image, s.limits, current.snapshot)
	if err != nil {
		return PreparedManagement{}, Image{}, err
	}
	for _, change := range request.Changes {
		prepared.AuthorizationRequired = prepared.AuthorizationRequired || change.Kind == PutIssuer || change.Kind == DisableIssuer || change.Kind == DeleteIssuer
	}
	for identity := range next.principals {
		before, wasActive := current.snapshot.AccessFor(identity)
		after, isActive := next.AccessFor(identity)
		if isActive && after.AllowsGlobal(SecurityManage) && (!wasActive || !before.AllowsGlobal(SecurityManage)) {
			prepared.AuthorizationRequired = true
		}
	}
	return prepared, image, nil
}

// PrepareManagement is nonmutating review preflight, never a reusable grant.
// Manage recomputes the complete requirement and invariants under the lock.
func (s *Store) PrepareManagement(ctx context.Context, request ManagementRequest) (PreparedManagement, error) {
	if s == nil || s.faulted.Load() {
		return PreparedManagement{}, ErrStoreUnavailable
	}
	if len(s.privateKey) == 0 {
		return PreparedManagement{}, ErrReadOnlyWriter
	}
	digest, _, err := s.managementIntent(request)
	if err != nil {
		return PreparedManagement{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Clock != nil {
		request.Now = request.Clock()
	}
	prepared, _, err := s.prepareManagementLocked(ctx, request, digest)
	return prepared, err
}

func (s *Store) Manage(ctx context.Context, request ManagementRequest) (ChangeResult, error) {
	if s == nil || s.faulted.Load() {
		return ChangeResult{}, ErrStoreUnavailable
	}
	if len(s.privateKey) == 0 {
		return ChangeResult{}, ErrReadOnlyWriter
	}
	digest, urgent, err := s.managementIntent(request)
	if err != nil {
		return ChangeResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Clock != nil {
		request.Now = request.Clock()
	}
	prepared, image, err := s.prepareManagementLocked(ctx, request, digest)
	if err != nil {
		return ChangeResult{}, err
	}
	if prepared.Retained {
		return prepared.Result, nil
	}
	current := s.current.Load()
	if prepared.AuthorizationRequired {
		if request.Authorize == nil {
			return ChangeResult{}, ErrOperationAuthorization
		}
		if err := request.Authorize(prepared.Binding, request.Now); err != nil {
			return ChangeResult{}, err
		}
	}
	audit := AuditRecord{Revision: current.sequence + 1, ChangeID: hex.EncodeToString(request.ChangeID[:]), IntentDigest: hex.EncodeToString(digest[:]), ActorDigest: redactedDigest(request.Actor), OccurredAt: request.Now.UTC(), Operation: "security.update", Outcome: "committed"}
	for _, change := range request.Changes {
		if len(audit.TargetDigests) < 16 {
			audit.TargetDigests = append(audit.TargetDigests, redactedDigest(change.target()))
		} else {
			audit.AdditionalTargets++
		}
	}
	image.Audit = append(image.Audit, audit)
	if len(image.Audit) > retainedChanges {
		image.Audit = image.Audit[len(image.Audit)-retainedChanges:]
	}
	snapshot, err := compileImage(image, s.limits, current.snapshot)
	if err != nil {
		return ChangeResult{}, err
	}
	if !urgent && len(snapshot.image) > ordinaryImageMaxBytes {
		return ChangeResult{}, ErrControlReserve
	}
	revision, err := signRevisionWithHistory(s.generation, current.sequence+1, current.digest, request.ChangeID, snapshot, s.privateKey, s.checkpointHistory(), digest)
	if err != nil {
		return ChangeResult{}, err
	}
	// Compilation and signing may consume time. Recheck the original current
	// credential/fence and exact proof immediately before durable persistence.
	// These callbacks perform no external I/O or reentrant Store operations.
	commitNow := request.Now
	if request.Clock != nil {
		commitNow = request.Clock()
	}
	if commitNow.Before(request.Now) {
		return ChangeResult{}, ErrAuthorityUnavailable
	}
	if request.Admission != nil {
		if err := request.Admission.CheckManagement(ctx, current, commitNow); err != nil {
			return ChangeResult{}, err
		}
	} else if err := current.snapshot.checkHumanAuthentication(request.Actor, request.Authentication, commitNow); err != nil {
		return ChangeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ChangeResult{}, err
	}
	if prepared.AuthorizationRequired {
		if err := request.Authorize(prepared.Binding, commitNow); err != nil {
			return ChangeResult{}, err
		}
	}
	if err := s.persistAndPublish(ctx, revision); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Revision: revision.sequence, Digest: revision.digest}, nil
}
