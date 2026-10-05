package security

import "errors"

// ManagementRejection marks a validation failure reached before persistence.
// It concerns this invocation only; it is not proof about any earlier use of ID.
// Committer errors must never be classified by their underlying sentinel.
type ManagementRejection struct {
	Reason ManagementRejectionReason
	Cause  error
}
type ManagementRejectionReason uint8

const (
	RejectInvalidChanges ManagementRejectionReason = iota + 1
	RejectUnknownRole
	RejectEnvironmentOwned
	RejectLastAdministrator
	RejectRevisionConflict
)

func (e *ManagementRejection) Error() string { return e.Cause.Error() }
func (e *ManagementRejection) Unwrap() error { return e.Cause }

// rejectManagementValidation is used only at known local validation stages.
// Authentication, authority, retained intent conflicts and persistence remain
// unclassified even if another layer happens to wrap a validation sentinel.
func rejectManagementValidation(err error) error {
	reason := RejectInvalidChanges
	switch {
	case errors.Is(err, ErrUnknownRole):
		reason = RejectUnknownRole
	case errors.Is(err, ErrBootstrapLocked):
		reason = RejectEnvironmentOwned
	case errors.Is(err, ErrLastAdministrator):
		reason = RejectLastAdministrator
	case errors.Is(err, ErrRevisionConflict):
		reason = RejectRevisionConflict
	case errors.Is(err, ErrInvalidImage), errors.Is(err, ErrInvalidPolicy):
	default:
		return err
	}
	return &ManagementRejection{Reason: reason, Cause: err}
}
