package securitytransport

import (
	"context"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// OperationalSettingsObserver reads actual installed owners. A successful Apply
// callback alone never establishes an applied generation, particularly when a
// group requires a coordinated process restart.
type OperationalSettingsObserver func(security.OperationalPolicyProjection) (map[operationalpolicy.Group]OperationalSettingsObservation, error)

// OperationalSetting is a closed provenance identity. Settings values always
// use operationalpolicy.Values; no arbitrary key/value configuration is accepted.
type OperationalSetting = pb.OperationalSetting
type OperationalSettingsSource = pb.OperationalSettingsSource
type OperationalSettingsChangeMode = pb.OperationalSettingsChangeMode
type OperationalSettingsError = pb.OperationalSettingsError

const (
	OperationalSourceDefault              = pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_DEFAULT
	OperationalSourceEnvironment          = pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_ENVIRONMENT
	OperationalSourceDerived              = pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_DERIVED
	OperationalSourceManaged              = pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_MANAGED
	OperationalModeUnavailable            = pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_UNAVAILABLE
	OperationalModeHot                    = pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT
	OperationalModeWorkerRestart          = pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_WORKER_RESTART
	OperationalModeProcessRestart         = pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_PROCESS_RESTART
	OperationalModeCoordinatedRestart     = pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_COORDINATED_RESTART
	OperationalErrorNone                  = pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_NONE
	OperationalErrorInvalidPolicy         = pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_INVALID_POLICY
	OperationalErrorOwnerUnavailable      = pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_OWNER_UNAVAILABLE
	OperationalErrorApplyFailed           = pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_APPLY_FAILED
	OperationalErrorDependencyUnavailable = pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_DEPENDENCY_UNAVAILABLE
	OperationalErrorCompatibilityMismatch = pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_COMPATIBILITY_MISMATCH
)

type OperationalSettingsObservation struct {
	EffectiveCompatibilityFingerprint string
	// Nil for unavailable ownership or explicitly invalid bootstrap values. Never fill this from defaults
	// when the verified authority or actual runtime observation is unavailable.
	Effective         *operationalpolicy.Values
	Sources           map[OperationalSetting]OperationalSettingsSource
	AppliedGeneration uint64
	AppliedCut        security.SemanticCut
	Mode              OperationalSettingsChangeMode
	Error             OperationalSettingsError
	Pending           bool
	RestartRequired   bool
}

// OperationalBackupPreviewer reports a bounded sample of this node only. The
// fingerprint is informational and does not seal a future deletion catalog.
type OperationalBackupPreviewer func(context.Context, int64) (OperationalBackupRetentionPreview, error)
type OperationalBackupRetentionPreview struct {
	ValidBackups uint64
	ValidBytes   uint64
	PruneBackups uint64
	PruneBytes   uint64
	Complete     bool
	Fingerprint  string
}

// OperationalCapacityPreviewer samples the registered owner on this node.
// Counters are sampled separately; the generation labels the effective owner,
// not an atomic graph measurement or a commitment to future admission.
type OperationalCapacityPreviewer func(context.Context, operationalpolicy.Capacity) (OperationalCapacityPreview, error)
type OperationalCapacityPreview struct {
	Member                uint32
	EffectiveGeneration   uint64
	VertexFootprint       uint64
	EdgeFootprint         uint64
	VertexCausalEntries   uint64
	EdgeCausalEntries     uint64
	VerticesOverLimit     bool
	EdgesOverLimit        bool
	VertexCausalOverLimit bool
	EdgeCausalOverLimit   bool
}
