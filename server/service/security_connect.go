package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SecurityServiceOptions comes from certified Server composition, never clients.
// A nil Store means explicitly OFF; enabled-but-unavailable never becomes OFF.
type SecurityServiceOptions struct {
	Store               *security.Store
	ValidateIssuer      func(context.Context, security.Issuer) error
	Enforced            func(security.ChangeResult) bool
	Now                 func() time.Time
	Ready               func(context.Context, *security.Revision) bool
	BeginAuthorization  func(context.Context, *security.Admission, security.ManagementBinding) (security.AuthorizationStart, string, error)
	ReadAuthorization   func(context.Context, *security.Admission, [32]byte) (security.AuthorizationStatus, error)
	VerifyAuthorization func([]byte, security.ManagementBinding, time.Time) error
}

type SecurityConnectHandler struct {
	store               *security.Store
	validateIssuer      func(context.Context, security.Issuer) error
	enforced            func(security.ChangeResult) bool
	cursors             cipher.AEAD
	now                 func() time.Time
	ready               func(context.Context, *security.Revision) bool
	beginAuthorization  func(context.Context, *security.Admission, security.ManagementBinding) (security.AuthorizationStart, string, error)
	readAuthorization   func(context.Context, *security.Admission, [32]byte) (security.AuthorizationStatus, error)
	verifyAuthorization func([]byte, security.ManagementBinding, time.Time) error
}

var _ graphv1connect.LanternSecurityServiceHandler = (*SecurityConnectHandler)(nil)

func NewSecurityConnectHandler(options SecurityServiceOptions) (*SecurityConnectHandler, error) {
	handler := &SecurityConnectHandler{store: options.Store, validateIssuer: options.ValidateIssuer, enforced: options.Enforced, ready: options.Ready, now: time.Now}
	handler.beginAuthorization, handler.readAuthorization, handler.verifyAuthorization = options.BeginAuthorization, options.ReadAuthorization, options.VerifyAuthorization
	if options.Now != nil {
		handler.now = options.Now
	}
	if options.Store == nil {
		return handler, nil
	}
	if options.ValidateIssuer == nil {
		return nil, security.ErrAuthorityUnavailable
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	handler.cursors, err = cipher.NewGCM(block)
	return handler, err
}
func (h *SecurityConnectHandler) admission(ctx context.Context, manage bool) (*security.Admission, error) {
	if h.store == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("security management is disabled"))
	}
	admission, ok := security.AdmissionFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if err := admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	if manage && !admission.Access().AllowsGlobal(security.SecurityManage) {
		return nil, connect.NewError(connect.CodePermissionDenied, security.ErrPermissionDenied)
	}
	current, ok := h.store.Current()
	if !ok || current.Digest() != admission.Revision().Digest() {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	return admission, nil
}
func securityVersion(revision *security.Revision) *pb.SecurityVersion {
	digest, generation := revision.Digest(), revision.Generation()
	return &pb.SecurityVersion{Revision: revision.Sequence(), Digest: append([]byte(nil), digest[:]...), Generation: append([]byte(nil), generation[:]...)}
}
func securityRequestError(message proto.Message) error {
	if err := strictSecurityMessage(message); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return nil
}
func (h *SecurityConnectHandler) GetAuthCapabilities(ctx context.Context, req *connect.Request[pb.GetAuthCapabilitiesRequest]) (*connect.Response[pb.GetAuthCapabilitiesResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	response := &pb.GetAuthCapabilitiesResponse{Mode: pb.AuthMode_AUTH_MODE_OFF, ProtocolVersion: 1, Ready: true}
	if h.store == nil && h.ready != nil {
		response.Ready = h.ready(ctx, nil)
	}
	if h.store != nil {
		response.Mode = pb.AuthMode_AUTH_MODE_OIDC
		response.Ready = false
		response.LoginPath = "/auth/login"
		if current, ok := h.store.Current(); ok {
			response.Ready = h.ready != nil && h.ready(ctx, current)
			for _, issuer := range current.Snapshot().Image().Issuers {
				if issuer.Enabled && !issuer.Deleted {
					response.LoginIssuers = append(response.LoginIssuers, &pb.LoginIssuer{Issuer: issuer.URL, Label: issuer.URL})
				}
			}
		}
	}
	result := connect.NewResponse(response)
	result.Header().Set("Cache-Control", "no-store")
	return result, nil
}
func (h *SecurityConnectHandler) GetCurrentPrincipal(ctx context.Context, req *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
	admission, err := h.admission(ctx, false)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	response := &pb.GetCurrentPrincipalResponse{Identity: encodeSecurityIdentity(admission.Identity()), Version: securityVersion(admission.Revision()), ExpiresAt: timestamppb.New(admission.ExpiresAt()), RecentAuthentication: !admission.AuthTime().IsZero() && !admission.AuthTime().After(h.now()) && h.now().Sub(admission.AuthTime()) <= security.RecentAuthenticationLifetime}
	image := admission.Revision().Snapshot().Image()
	roles := make(map[string]bool)
	for _, user := range image.Principals {
		if user.Identity == admission.Identity() {
			for _, assignment := range user.Assignments {
				roles[assignment.RoleID] = true
			}
		}
	}
	for _, role := range image.Roles {
		if roles[role.ID] {
			response.Roles = append(response.Roles, encodeSecurityManagedRole(role, image))
		}
	}
	if admission.Browser() {
		response.CsrfToken = admission.CSRFToken()
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	result := connect.NewResponse(response)
	result.Header().Set("Cache-Control", "no-store")
	return result, nil
}

type securityPageRequest interface {
	proto.Message
	GetLimit() uint32
	GetCursor() string
	GetExact() string
}

func (h *SecurityConnectHandler) page(req securityPageRequest, method string, admission *security.Admission, count int) (int, int, error) {
	if err := securityRequestError(req); err != nil {
		return 0, 0, err
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = 100
	}
	if limit > 200 || len(req.GetExact()) > 2048 || len(req.GetCursor()) > 1024 {
		return 0, 0, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	offset := 0
	if req.GetCursor() != "" {
		bytes, err := base64.RawURLEncoding.Strict().DecodeString(req.GetCursor())
		if err != nil || len(bytes) < h.cursors.NonceSize() {
			return 0, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid security cursor"))
		}
		clear, err := h.cursors.Open(nil, bytes[:h.cursors.NonceSize()], bytes[h.cursors.NonceSize():], securityCursorAAD(method, req.GetExact(), admission))
		if err != nil || len(clear) != 4 {
			return 0, 0, connect.NewError(connect.CodeAborted, errors.New("security cursor scope changed"))
		}
		offset = int(binary.BigEndian.Uint32(clear))
	}
	if offset > count {
		return 0, 0, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	end := min(offset+limit, count)
	return offset, end, nil
}
func securityCursorAAD(method, exact string, admission *security.Admission) []byte {
	binding := admission.ScopeBinding()
	return append(append(binding[:], []byte(method+"\x00")...), exact...)
}
func (h *SecurityConnectHandler) nextCursor(end, count int, method, exact string, admission *security.Admission) string {
	if end >= count {
		return ""
	}
	nonce := make([]byte, h.cursors.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	clear := binary.BigEndian.AppendUint32(nil, uint32(end))
	return base64.RawURLEncoding.EncodeToString(h.cursors.Seal(nonce, nonce, clear, securityCursorAAD(method, exact, admission)))
}
func (h *SecurityConnectHandler) ListIssuers(ctx context.Context, req *connect.Request[pb.ListIssuersRequest]) (*connect.Response[pb.ListIssuersResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	var items []*pb.SecurityIssuer
	for _, issuer := range admission.Revision().Snapshot().Image().Issuers {
		if req.Msg.Exact == "" || req.Msg.Exact == issuer.URL {
			items = append(items, encodeSecurityIssuer(issuer))
		}
	}
	start, end, err := h.page(req.Msg, "issuers", admission, len(items))
	if err != nil {
		return nil, err
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.ListIssuersResponse{Issuers: items[start:end], Version: securityVersion(admission.Revision()), NextCursor: h.nextCursor(end, len(items), "issuers", req.Msg.Exact, admission)})
}
func (h *SecurityConnectHandler) ListRoles(ctx context.Context, req *connect.Request[pb.ListRolesRequest]) (*connect.Response[pb.ListRolesResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	var items []*pb.SecurityRole
	for _, role := range admission.Revision().Snapshot().Image().Roles {
		if req.Msg.Exact == "" || req.Msg.Exact == role.ID {
			items = append(items, encodeSecurityManagedRole(role, admission.Revision().Snapshot().Image()))
		}
	}
	start, end, err := h.page(req.Msg, "roles", admission, len(items))
	if err != nil {
		return nil, err
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.ListRolesResponse{Roles: items[start:end], Version: securityVersion(admission.Revision()), NextCursor: h.nextCursor(end, len(items), "roles", req.Msg.Exact, admission)})
}
func (h *SecurityConnectHandler) ListUsers(ctx context.Context, req *connect.Request[pb.ListUsersRequest]) (*connect.Response[pb.ListUsersResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	var items []*pb.SecurityUser
	for _, user := range admission.Revision().Snapshot().Image().Principals {
		if req.Msg.Exact == "" || req.Msg.Exact == user.Identity.Subject || req.Msg.Exact == user.Identity.MachineName {
			items = append(items, encodeSecurityUser(user))
		}
	}
	start, end, err := h.page(req.Msg, "users", admission, len(items))
	if err != nil {
		return nil, err
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.ListUsersResponse{Users: items[start:end], Version: securityVersion(admission.Revision()), NextCursor: h.nextCursor(end, len(items), "users", req.Msg.Exact, admission)})
}
func (h *SecurityConnectHandler) ListRoleAssignments(ctx context.Context, req *connect.Request[pb.ListRoleAssignmentsRequest]) (*connect.Response[pb.ListRoleAssignmentsResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	identity, err := decodeSecurityIdentity(req.Msg.Identity)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	for _, user := range admission.Revision().Snapshot().Image().Principals {
		if user.Identity == identity {
			return securityReadResponse(ctx, admission, h.now(), &pb.ListRoleAssignmentsResponse{Assignments: encodeSecurityUser(user).Assignments, Version: securityVersion(admission.Revision())})
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("user not registered"))
}
func (h *SecurityConnectHandler) ListSecurityAudit(ctx context.Context, req *connect.Request[pb.ListSecurityAuditRequest]) (*connect.Response[pb.ListSecurityAuditResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	var items []*pb.SecurityAuditRecord
	for _, record := range admission.Revision().Snapshot().Image().Audit {
		if req.Msg.Exact == "" || req.Msg.Exact == record.ChangeID || req.Msg.Exact == strconv.FormatUint(record.Revision, 10) {
			items = append(items, &pb.SecurityAuditRecord{Revision: record.Revision, ChangeId: record.ChangeID, IntentDigest: record.IntentDigest, ActorDigest: record.ActorDigest, OccurredAt: timestamppb.New(record.OccurredAt), Operation: record.Operation, TargetDigests: append([]string(nil), record.TargetDigests...), AdditionalTargets: uint32(record.AdditionalTargets), Outcome: record.Outcome})
		}
	}
	start, end, err := h.page(req.Msg, "audit", admission, len(items))
	if err != nil {
		return nil, err
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.ListSecurityAuditResponse{Records: items[start:end], Version: securityVersion(admission.Revision()), NextCursor: h.nextCursor(end, len(items), "audit", req.Msg.Exact, admission)})
}
func (h *SecurityConnectHandler) GetRoleTemplates(ctx context.Context, req *connect.Request[pb.GetRoleTemplatesRequest]) (*connect.Response[pb.GetRoleTemplatesResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	roles, err := security.RoleTemplates(req.Msg.Prefix)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	response := &pb.GetRoleTemplatesResponse{Version: securityVersion(admission.Revision())}
	for _, role := range roles {
		response.Roles = append(response.Roles, encodeSecurityRole(role))
	}
	return securityReadResponse(ctx, admission, h.now(), response)
}
func (h *SecurityConnectHandler) ExplainAccess(ctx context.Context, req *connect.Request[pb.ExplainAccessRequest]) (*connect.Response[pb.ExplainAccessResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	identity, err := decodeSecurityIdentity(req.Msg.Identity)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	action, known := securityActions[req.Msg.Action]
	if !known {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidPolicy)
	}
	var allowed bool
	var matches []security.RuleMatch
	if req.Msg.Edge != nil {
		if req.Msg.LogicalKey != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidPolicy)
		}
		allowed, matches, err = admission.Revision().Snapshot().ExplainEdge(identity, action, req.Msg.Edge.Tail, req.Msg.Edge.Head)
	} else {
		allowed, matches, err = admission.Revision().Snapshot().Explain(identity, action, req.Msg.LogicalKey)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	response := &pb.ExplainAccessResponse{Allowed: allowed, Version: securityVersion(admission.Revision())}
	for _, match := range matches {
		response.Matches = append(response.Matches, &pb.SecurityRuleMatch{RoleId: match.RoleID, RuleId: match.RuleID, Effect: encodeSecurityEffect(match.Effect), Action: encodeSecurityAction(match.Action), Endpoint: match.Endpoint})
	}
	return securityReadResponse(ctx, admission, h.now(), response)
}
func (h *SecurityConnectHandler) ValidateIssuer(ctx context.Context, req *connect.Request[pb.ValidateIssuerRequest]) (*connect.Response[pb.ValidateIssuerResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	if err = admission.CheckIssuerProbe(ctx, admission.Revision(), h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	issuer, err := decodeSecurityIssuer(req.Msg.Issuer)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if req.Msg.Issuer.SecretRef == nil {
		if existing, known := admission.Revision().Snapshot().Issuer(issuer.URL); known {
			issuer.SecretRef = existing.SecretRef
		}
	}
	if err = h.validateIssuer(ctx, issuer); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("issuer validation failed"))
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	return connect.NewResponse(&pb.ValidateIssuerResponse{Valid: true}), nil
}
func (h *SecurityConnectHandler) ApplySecurityChanges(ctx context.Context, req *connect.Request[pb.ApplySecurityChangesRequest]) (*connect.Response[pb.ApplySecurityChangesResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	if len(req.Msg.ChangeId) != 16 || req.Msg.ExpectedRevision == 0 || len(req.Msg.Changes) == 0 || len(req.Msg.Changes) > security.MaxTransactionChanges {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	if len(req.Msg.AuthorizationProof) != 0 && len(req.Msg.AuthorizationProof) != 32 {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	var changeID [16]byte
	copy(changeID[:], req.Msg.ChangeId)
	changes := make([]security.Change, len(req.Msg.Changes))
	for i, change := range req.Msg.Changes {
		changes[i], err = decodeSecurityChange(change)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	management := h.managementRequest(admission, req.Msg.ExpectedRevision, changeID, changes)
	prepared, err := h.store.PrepareManagement(ctx, management)
	if err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	if !prepared.Retained && prepared.AuthorizationRequired {
		if h.verifyAuthorization == nil {
			return nil, operationAuthorizationError(security.ErrOperationAuthorization, prepared.Binding)
		}
		if err := h.verifyAuthorization(req.Msg.AuthorizationProof, prepared.Binding, h.now()); err != nil {
			return nil, operationAuthorizationError(err, prepared.Binding)
		}
		proof := append([]byte(nil), req.Msg.AuthorizationProof...)
		management.Authorize = func(binding security.ManagementBinding, now time.Time) error {
			return h.verifyAuthorization(proof, binding, now)
		}
	}
	// No network fetch for a stale CAS, invalid or unauthenticated command.
	// An older expected revision may be an exact retained retry. Manage checks
	// its original intent before CAS; it cannot commit a new stale command.
	stale := req.Msg.ExpectedRevision != admission.Revision().Sequence()
	if !stale {
		for _, change := range changes {
			if change.Issuer != nil && change.Issuer.Enabled {
				issuer := *change.Issuer
				if change.PreserveSecret {
					if existing, known := admission.Revision().Snapshot().Issuer(issuer.URL); known {
						issuer.SecretRef = existing.SecretRef
					}
				}
				if err = h.validateIssuer(ctx, issuer); err != nil {
					return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("issuer validation failed"))
				}
			}
		}
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	result, err := h.store.Manage(ctx, management)
	if err != nil {
		return nil, operationAuthorizationError(err, prepared.Binding)
	}
	digest, generation := result.Digest, admission.Revision().Generation()
	response := &pb.ApplySecurityChangesResponse{Version: &pb.SecurityVersion{Revision: result.Revision, Digest: append([]byte(nil), digest[:]...), Generation: append([]byte(nil), generation[:]...)}, Applied: make([]bool, len(changes)), Replayed: result.Replayed, Enforcement: pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING}
	for i := range response.Applied {
		response.Applied[i] = true
	}
	if h.enforced != nil && h.enforced(result) {
		response.Enforcement = pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED
	}
	return connect.NewResponse(response), nil
}
func (h *SecurityConnectHandler) ApplySecurityChange(ctx context.Context, req *connect.Request[pb.ApplySecurityChangeRequest]) (*connect.Response[pb.ApplySecurityChangeResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	batch := connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: req.Msg.ExpectedRevision, ChangeId: req.Msg.ChangeId, Changes: []*pb.SecurityChange{req.Msg.Change}, AuthorizationProof: req.Msg.AuthorizationProof})
	result, err := h.ApplySecurityChanges(ctx, batch)
	if err != nil {
		return nil, err
	}
	if len(result.Msg.Applied) != 1 {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("unaligned security outcome"))
	}
	return connect.NewResponse(&pb.ApplySecurityChangeResponse{Version: result.Msg.Version, Applied: result.Msg.Applied[0], Replayed: result.Msg.Replayed, Enforcement: result.Msg.Enforcement}), nil
}

func securityReadResponse[T any](ctx context.Context, admission *security.Admission, now time.Time, message *T) (*connect.Response[T], error) {
	if err := admission.Check(ctx, now); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	response := connect.NewResponse(message)
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}
func (h *SecurityConnectHandler) GetIssuer(ctx context.Context, req *connect.Request[pb.GetIssuerRequest]) (*connect.Response[pb.GetIssuerResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	if req.Msg.Issuer == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	issuer, known := admission.Revision().Snapshot().Issuer(req.Msg.Issuer)
	if !known {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("issuer not registered"))
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.GetIssuerResponse{Issuer: encodeSecurityIssuer(issuer), Version: securityVersion(admission.Revision())})
}
func (h *SecurityConnectHandler) GetRole(ctx context.Context, req *connect.Request[pb.GetRoleRequest]) (*connect.Response[pb.GetRoleResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	for _, role := range admission.Revision().Snapshot().Image().Roles {
		if role.ID == req.Msg.Id {
			return securityReadResponse(ctx, admission, h.now(), &pb.GetRoleResponse{Role: encodeSecurityManagedRole(role, admission.Revision().Snapshot().Image()), Version: securityVersion(admission.Revision())})
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("role not registered"))
}
func (h *SecurityConnectHandler) GetUser(ctx context.Context, req *connect.Request[pb.GetUserRequest]) (*connect.Response[pb.GetUserResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	identity, err := decodeSecurityIdentity(req.Msg.Identity)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	for _, user := range admission.Revision().Snapshot().Image().Principals {
		if user.Identity == identity {
			return securityReadResponse(ctx, admission, h.now(), &pb.GetUserResponse{User: encodeSecurityUser(user), Version: securityVersion(admission.Revision())})
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("user not registered"))
}
func (h *SecurityConnectHandler) GetSecurityChangeStatus(ctx context.Context, req *connect.Request[pb.GetSecurityChangeStatusRequest]) (*connect.Response[pb.GetSecurityChangeStatusResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err = securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	if len(req.Msg.ChangeId) != 16 {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	var id [16]byte
	copy(id[:], req.Msg.ChangeId)
	result, err := h.store.ChangeStatus(id)
	if err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	digest, generation := result.Digest, admission.Revision().Generation()
	// Retained history proves the original commit, not request-aligned item
	// outcomes or the current policy. Keep those acknowledgements on Apply.
	response := &pb.GetSecurityChangeStatusResponse{Version: &pb.SecurityVersion{Revision: result.Revision, Digest: append([]byte(nil), digest[:]...), Generation: append([]byte(nil), generation[:]...)}, ChangeId: append([]byte(nil), id[:]...), Enforcement: pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING}
	if h.enforced != nil && h.enforced(result) {
		response.Enforcement = pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED
	}
	return securityReadResponse(ctx, admission, h.now(), response)
}
