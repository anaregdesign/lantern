package securitytransport

import "github.com/anaregdesign/lantern/server/internal/security"

// LogSettingsObserver receives certified observations from the actual local logger
// owner. They never come from request fields or imply another node's application.
type LogSettingsObserver func(security.LogPolicyProjection) (LogSettingsObservation, error)

type LogSettingsSource uint8

const (
	LogSettingsSourceDefault LogSettingsSource = iota + 1
	LogSettingsSourceEnvironment
	LogSettingsSourceManaged
)

type LogSettingsError uint8

const (
	LogSettingsErrorNone LogSettingsError = iota + 1
	LogSettingsErrorInvalidPolicy
	LogSettingsErrorOwnerUnavailable
	LogSettingsErrorApplyFailed
)

type LogSettingsObservation struct {
	EffectiveLevel    security.LogLevel
	Source            LogSettingsSource
	AppliedGeneration uint64
	AppliedCut        security.SemanticCut
	Error             LogSettingsError
	Pending           bool
	RestartRequired   bool
}
