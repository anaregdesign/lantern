package service

import (
	"connectrpc.com/connect"
	"errors"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// precommitRejectionError is called only from known pre-persistence stages.
// A storage failure or retained-ID intent conflict always stays indeterminate.
func (h *SecurityConnectHandler) precommitRejectionError(err error, reason pb.SecurityChangeRejectionReason, id []byte, expected uint64) error {
	code := securityErrorCode(err)
	if reason == pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_ISSUER_VALIDATION {
		code = connect.CodeFailedPrecondition
	}
	result := connect.NewError(code, err)
	if len(id) != 16 || expected == 0 || reason == pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_UNSPECIFIED || errors.Is(err, security.ErrStoreUnavailable) || errors.Is(err, security.ErrChangeConflict) {
		return result
	}
	var changeID [16]byte
	copy(changeID[:], id)
	if changeID == [16]byte{} {
		return result
	}
	// A known retained ID must be reconciled, including malformed changed intent.
	// Unknown history is not noncommit proof; the detail only covers this call.
	if _, statusErr := h.store.ChangeStatus(changeID); !errors.Is(statusErr, security.ErrUnknownChange) {
		return result
	}
	detail, detailErr := connect.NewErrorDetail(&pb.SecurityChangePrecommitRejected{ChangeId: append([]byte(nil), id...), ExpectedRevision: expected, Reason: reason})
	if detailErr == nil {
		result.AddDetail(detail)
	}
	return result
}
func (h *SecurityConnectHandler) managementApplyError(err error, binding security.ManagementBinding, id []byte, expected uint64) error {
	var rejected *security.ManagementRejection
	if errors.As(err, &rejected) && !errors.Is(err, security.ErrStoreUnavailable) {
		reason := pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_UNSPECIFIED
		switch rejected.Reason {
		case security.RejectInvalidChanges:
			reason = pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_INVALID_CHANGES
		case security.RejectUnknownRole:
			reason = pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_UNKNOWN_ROLE
		case security.RejectEnvironmentOwned:
			reason = pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_ENVIRONMENT_OWNED
		case security.RejectLastAdministrator:
			reason = pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_LAST_ADMINISTRATOR
		case security.RejectRevisionConflict:
			reason = pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_REVISION_CONFLICT
		}
		return h.precommitRejectionError(err, reason, id, expected)
	}
	return operationAuthorizationError(err, binding)
}
