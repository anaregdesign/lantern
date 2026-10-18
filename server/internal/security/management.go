package security

import (
	"errors"
	"time"
)

var (
	ErrPermissionDenied       = errors.New("security management permission denied")
	ErrControlReserve         = errors.New("security control capacity is reserved for revocation")
	ErrOperationAuthorization = errors.New("reviewed operation authentication required")
	ErrRevisionConflict       = errors.New("security reviewed cut conflict")
	ErrChangeConflict         = errors.New("security change ID reused with different intent")
	ErrBootstrapLocked        = errors.New("env-owned security assignment is locked")
)

const RecentAuthenticationLifetime = 5 * time.Minute
