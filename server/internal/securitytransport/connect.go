package securitytransport

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// SecurityServiceOptions comes from certified Server composition, never clients.
// A nil authority means explicitly OFF; unavailable OIDC never becomes OFF.
type SecurityServiceOptions struct {
	Current                   *security.CurrentAuthority
	CurrentBeginAuthorization func(context.Context, *security.Admission, security.CurrentReview) (security.AuthorizationStart, string, string, error)
	CurrentReadAuthorization  func(context.Context, *security.Admission, [32]byte, string) (security.AuthorizationStatus, error)
	ValidateIssuer            func(context.Context, security.Issuer) error
	Now                       func() time.Time
	Ready                     func(context.Context) bool
}

type SecurityConnectHandler struct {
	current                   *security.CurrentAuthority
	currentBeginAuthorization func(context.Context, *security.Admission, security.CurrentReview) (security.AuthorizationStart, string, string, error)
	currentReadAuthorization  func(context.Context, *security.Admission, [32]byte, string) (security.AuthorizationStatus, error)
	validateIssuer            func(context.Context, security.Issuer) error
	cursors                   cipher.AEAD
	now                       func() time.Time
	ready                     func(context.Context) bool
}

var _ graphv1connect.LanternSecurityServiceHandler = (*SecurityConnectHandler)(nil)

func NewSecurityConnectHandler(options SecurityServiceOptions) (*SecurityConnectHandler, error) {
	if options.Current != nil && options.Current.Profile().Binding() == "" {
		return nil, security.ErrS1Contract
	}
	handler := &SecurityConnectHandler{validateIssuer: options.ValidateIssuer, ready: options.Ready, now: time.Now}
	handler.current = options.Current
	handler.currentBeginAuthorization, handler.currentReadAuthorization = options.CurrentBeginAuthorization, options.CurrentReadAuthorization
	if options.Now != nil {
		handler.now = options.Now
	}
	if options.Current == nil {
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
	if h.current == nil {
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
	profile, current := admission.CurrentProfile()
	if !current || profile != h.current.Profile() {
		return nil, connect.NewError(connect.CodeUnauthenticated, security.ErrPermissionDenied)
	}
	var bindErr error
	if manage {
		bindErr = admission.BindCurrentGlobalOutput(ctx, security.SecurityManage)
	} else {
		bindErr = admission.BindCurrentSelfOutput(ctx)
	}
	if bindErr != nil {
		return nil, connect.NewError(securityErrorCode(bindErr), bindErr)
	}
	return admission, nil
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
	if h.current != nil {
		if err := h.current.BindPublicMetadata(ctx, security.CurrentCapabilitiesMetadata); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		response.Mode, response.ProtocolVersion = pb.AuthMode_AUTH_MODE_OIDC, security.CurrentPublicVersion
		response.Ready, response.CurrentProfile = h.current.Ready(ctx), EncodeCurrentProfile(h.current.Profile())
		response.LoginPath = "/auth/login"
		response.CurrentOriginEnabled, response.CurrentMember = h.current.OriginEnabled(), h.current.MemberID()
		if response.Ready {
			issuers, err := h.current.PublicLoginIssuers(ctx)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnavailable, err)
			}
			for _, issuer := range issuers {
				response.LoginIssuers = append(response.LoginIssuers, &pb.LoginIssuer{Issuer: issuer, Label: issuer})
			}
		}
		result := connect.NewResponse(response)
		result.Header().Set("Cache-Control", "no-store")
		return result, nil
	}
	if h.ready != nil {
		response.Ready = h.ready(ctx)
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
	view, err := authn.InspectSession(ctx, admission, h.current, h.now)
	if err != nil {
		return nil, connect.NewError(sessionErrorCode(err), err)
	}
	result := connect.NewResponse(encodeSessionView(view))
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
	for _, issuer := range admission.Snapshot().Image().Issuers {
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
	return securityReadResponse(ctx, admission, h.now(), &pb.ListIssuersResponse{Issuers: items[start:end], Version: SecurityVersionForAdmission(admission), NextCursor: h.nextCursor(end, len(items), "issuers", req.Msg.Exact, admission)})
}
func (h *SecurityConnectHandler) ListRoles(ctx context.Context, req *connect.Request[pb.ListRolesRequest]) (*connect.Response[pb.ListRolesResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	var items []*pb.SecurityRole
	for _, role := range admission.Snapshot().Image().Roles {
		if req.Msg.Exact == "" || req.Msg.Exact == role.ID {
			items = append(items, encodeSecurityManagedRole(role, admission.Snapshot().Image()))
		}
	}
	start, end, err := h.page(req.Msg, "roles", admission, len(items))
	if err != nil {
		return nil, err
	}
	if err = admission.Check(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.ListRolesResponse{Roles: items[start:end], Version: SecurityVersionForAdmission(admission), NextCursor: h.nextCursor(end, len(items), "roles", req.Msg.Exact, admission)})
}
func (h *SecurityConnectHandler) ListUsers(ctx context.Context, req *connect.Request[pb.ListUsersRequest]) (*connect.Response[pb.ListUsersResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	var items []*pb.SecurityUser
	for _, user := range admission.Snapshot().Image().Principals {
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
	return securityReadResponse(ctx, admission, h.now(), &pb.ListUsersResponse{Users: items[start:end], Version: SecurityVersionForAdmission(admission), NextCursor: h.nextCursor(end, len(items), "users", req.Msg.Exact, admission)})
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
	for _, user := range admission.Snapshot().Image().Principals {
		if user.Identity == identity {
			return securityReadResponse(ctx, admission, h.now(), &pb.ListRoleAssignmentsResponse{Assignments: encodeSecurityUser(user).Assignments, Version: SecurityVersionForAdmission(admission)})
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("user not registered"))
}
func (h *SecurityConnectHandler) ListSecurityAudit(ctx context.Context, req *connect.Request[pb.ListSecurityAuditRequest]) (*connect.Response[pb.ListSecurityAuditResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	view, err := h.current.Audit(ctx, admission, req.Msg.Exact)
	if err != nil {
		return nil, currentControlError(err)
	}
	start, end, err := h.page(req.Msg, "current-audit", admission, view.Len())
	if err != nil {
		return nil, err
	}
	records, err := view.Page(start, end)
	if err != nil {
		return nil, currentControlError(err)
	}
	response := &pb.ListSecurityAuditResponse{Version: SecurityVersionForAdmission(admission), NextCursor: h.nextCursor(end, view.Len(), "current-audit", req.Msg.Exact, admission)}
	for _, record := range records {
		response.CurrentRecords = append(response.CurrentRecords, &pb.CurrentSecurityAuditRecord{Result: EncodeCurrentResult(record.Result), ActorDigest: record.Actor[:], Operation: record.Operation})
	}
	return securityReadResponse(ctx, admission, h.now(), response)
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
	response := &pb.GetRoleTemplatesResponse{Version: SecurityVersionForAdmission(admission)}
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
		allowed, matches, err = admission.Snapshot().ExplainEdge(identity, action, req.Msg.Edge.Tail, req.Msg.Edge.Head)
	} else {
		allowed, matches, err = admission.Snapshot().Explain(identity, action, req.Msg.LogicalKey)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	response := &pb.ExplainAccessResponse{Allowed: allowed, Version: SecurityVersionForAdmission(admission)}
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
	if err = admission.CheckIssuerProbe(ctx, h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	issuer, err := decodeSecurityIssuer(req.Msg.Issuer)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if existing, known := admission.Snapshot().Issuer(issuer.URL); known {
		issuer = issuerPreservation(req.Msg.Issuer, issuer).ResolveIssuerSecrets(existing)
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
	return h.applyCurrentChanges(ctx, req)
}
func (h *SecurityConnectHandler) ApplySecurityChange(ctx context.Context, req *connect.Request[pb.ApplySecurityChangeRequest]) (*connect.Response[pb.ApplySecurityChangeResponse], error) {
	if h.current == nil {
		_, err := h.admission(ctx, true)
		return nil, err
	}
	if securityRequestError(req.Msg) != nil || req.Msg.CurrentReview == nil || len(req.Msg.CurrentReview.Changes) != 1 || req.Msg.Change != nil || req.Msg.ExpectedRevision != 0 || len(req.Msg.ChangeId) != 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrS1Contract)
	}
	result, err := h.ApplySecurityChanges(ctx, connect.NewRequest(&pb.ApplySecurityChangesRequest{CurrentReview: req.Msg.CurrentReview, AuthorizationProof: req.Msg.AuthorizationProof}))
	if err != nil {
		return nil, err
	}
	response := connect.NewResponse(&pb.ApplySecurityChangeResponse{CurrentResult: result.Msg.CurrentResult})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
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
	issuer, known := admission.Snapshot().Issuer(req.Msg.Issuer)
	if !known {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("issuer not registered"))
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.GetIssuerResponse{Issuer: encodeSecurityIssuer(issuer), Version: SecurityVersionForAdmission(admission)})
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
	for _, role := range admission.Snapshot().Image().Roles {
		if role.ID == req.Msg.Id {
			return securityReadResponse(ctx, admission, h.now(), &pb.GetRoleResponse{Role: encodeSecurityManagedRole(role, admission.Snapshot().Image()), Version: SecurityVersionForAdmission(admission)})
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
	for _, user := range admission.Snapshot().Image().Principals {
		if user.Identity == identity {
			return securityReadResponse(ctx, admission, h.now(), &pb.GetUserResponse{User: encodeSecurityUser(user), Version: SecurityVersionForAdmission(admission)})
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("user not registered"))
}
func (h *SecurityConnectHandler) GetSecurityChangeStatus(ctx context.Context, req *connect.Request[pb.GetSecurityChangeStatusRequest]) (*connect.Response[pb.GetSecurityChangeStatusResponse], error) {
	return h.currentChangeStatus(ctx, req)
}
