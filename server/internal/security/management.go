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
}

func (s *Store) Manage(ctx context.Context, request ManagementRequest) (ChangeResult, error) {
	if s == nil || s.faulted.Load() {
		return ChangeResult{}, ErrStoreUnavailable
	}
	if len(s.privateKey) == 0 {
		return ChangeResult{}, ErrReadOnlyWriter
	}
	if request.ChangeID == [16]byte{} || request.ExpectedRevision == 0 || len(request.Changes) == 0 || len(request.Changes) > MaxTransactionChanges {
		return ChangeResult{}, ErrInvalidImage
	}
	roles := make([]Role, 0, len(request.Changes))
	for _, change := range request.Changes {
		if change.Role != nil {
			roles = append(roles, *change.Role)
		}
	}
	if err := validateRoleBounds(roles, s.limits); err != nil {
		return ChangeResult{}, err
	}
	seen := make(map[string]bool, len(request.Changes))
	urgent := true
	for _, change := range request.Changes {
		if err := change.validate(); err != nil {
			return ChangeResult{}, err
		}
		target := change.target()
		if seen[target] {
			return ChangeResult{}, ErrInvalidImage
		}
		seen[target] = true
		// Grants/registration cannot consume the independent revocation reserve.
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
		return ChangeResult{}, ErrInvalidImage
	}
	digest := sha256.Sum256(intent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faulted.Load() {
		return ChangeResult{}, ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ChangeResult{}, err
	}
	current := s.current.Load()
	if current == nil {
		return ChangeResult{}, ErrStoreUnavailable
	}
	access, active := current.snapshot.AccessFor(request.Actor)
	if !active || !access.AllowsGlobal(SecurityManage) {
		return ChangeResult{}, ErrPermissionDenied
	}
	if request.Now.IsZero() || request.AuthTime.IsZero() || request.AuthTime.After(request.Now) || request.Now.Sub(request.AuthTime) > RecentAuthenticationLifetime {
		return ChangeResult{}, ErrRecentAuthentication
	}
	if record, known := s.changes[request.ChangeID]; known {
		if record.expected != request.ExpectedRevision || record.intentDigest != digest {
			return ChangeResult{}, ErrChangeConflict
		}
		result := record.result
		result.Replayed = true
		return result, nil
	}
	if request.ExpectedRevision != current.sequence {
		return ChangeResult{}, ErrRevisionConflict
	}
	image := current.snapshot.Image()
	if err := applyManagementChanges(&image, request.Changes); err != nil {
		return ChangeResult{}, err
	}
	if !sameEnvOwned(current.snapshot.Image(), image) {
		return ChangeResult{}, ErrBootstrapLocked
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
	if err := s.persistAndPublish(ctx, revision); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Revision: revision.sequence, Digest: revision.digest}, nil
}
