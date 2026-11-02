// Package service adapts the data executor to Connect and owns the HTTP
// listener lifecycle. State, publication and recovery belong to dataruntime.
package service

import (
	"context"
	"net/http"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

// These construction names retain the in-process composition surface for
// consumers outside server's internal-package boundary. They add no state or
// ownership; provider and backup consume dataruntime directly.
type (
	LanternService                  = dataruntime.Executor
	LanternReplicationService       = dataruntime.Replication
	Backend                         = dataruntime.Backend
	ServingRuntime                  = dataruntime.ServingRuntime
	DurableReceiptWALRuntimeConfig  = dataruntime.DurableReceiptWALRuntimeConfig
	ReceiptStartupRestore           = dataruntime.ReceiptStartupRestore
	ReceiptWholeStateCapture        = dataruntime.ReceiptWholeStateCapture
	ReceiptWholeStateBackupCapture  = dataruntime.ReceiptWholeStateBackupCapture
	ReceiptWholeStateSource         = dataruntime.ReceiptWholeStateSource
	ReceiptBaselineFormat           = dataruntime.ReceiptBaselineFormat
	ReceiptBaselineArchiveCodec     = dataruntime.ReceiptBaselineArchiveCodec
	ReceiptBaselineCandidate        = dataruntime.ReceiptBaselineCandidate
	OriginState                     = dataruntime.OriginState
	OriginStatesProvider            = dataruntime.OriginStatesProvider
	SearchConfigFingerprintProvider = dataruntime.SearchConfigFingerprintProvider
	HotPathMetrics                  = dataruntime.HotPathMetrics
	ScanLimits                      = dataruntime.ScanLimits
	SearchLimits                    = dataruntime.SearchLimits
	SearchObservation               = dataruntime.SearchObservation
	CapacityLimits                  = dataruntime.CapacityLimits
	TraversalLimits                 = dataruntime.TraversalLimits
	StatusInfo                      = dataruntime.StatusInfo
	ReplicationSnapshotter          = dataruntime.ReplicationSnapshotter
	ReplicationStatusInfo           = dataruntime.ReplicationStatusInfo
	SubscribeMetrics                = dataruntime.SubscribeMetrics
	ChangeCursorKey                 = dataruntime.ChangeCursorKey
	ChangeServiceOptions            = dataruntime.ChangeServiceOptions
	Sender[T any]                   = dataruntime.Sender[T]
)

const (
	ServiceName                   = dataruntime.ServiceName
	ReplicationServiceName        = dataruntime.ReplicationServiceName
	SnapshotTailHeader            = dataruntime.SnapshotTailHeader
	MaxReceiptStatusBatchSize     = dataruntime.MaxReceiptStatusBatchSize
	ReceiptBaselineFormatCombined = dataruntime.ReceiptBaselineFormatCombined
)

var ErrDurableReceiptWALBackupFallbackEligible = dataruntime.ErrDurableReceiptWALBackupFallbackEligible

func NewLanternService(cache Backend) *LanternService { return dataruntime.NewExecutor(cache) }

func NewLanternReplicationService(log *mutationlog.Log, backend Backend, clock *hlc.Clock) *LanternReplicationService {
	return dataruntime.NewReplication(log, backend, clock)
}

func NewGraphOnlyServingRuntime(graph *graphcache.GraphCache[string, *pb.Vertex], log *mutationlog.Log, clock *hlc.Clock, namespaceFormat ...string) (*ServingRuntime, error) {
	return dataruntime.NewGraphOnlyServingRuntime(graph, log, clock, namespaceFormat...)
}

func CreateDurableReceiptWALServingRuntime(config DurableReceiptWALRuntimeConfig) (*ServingRuntime, error) {
	return dataruntime.CreateDurableReceiptWALServingRuntime(config)
}

func OpenDurableReceiptWALServingRuntime(config DurableReceiptWALRuntimeConfig) (*ServingRuntime, error) {
	return dataruntime.OpenDurableReceiptWALServingRuntime(config)
}

func OpenDurableReceiptWALServingRuntimeFromBackup(config DurableReceiptWALRuntimeConfig) (*ServingRuntime, error) {
	return dataruntime.OpenDurableReceiptWALServingRuntimeFromBackup(config)
}

func NewReceiptWholeStateSource(executor *LanternService, store *mutationreceipt.Store) (*ReceiptWholeStateSource, error) {
	return dataruntime.NewReceiptWholeStateSource(executor, store)
}

func PrepareReceiptSnapshotFrames(capture ReceiptWholeStateCapture, policy mutationreceipt.Config) ([]*pb.SnapshotResponse, error) {
	return dataruntime.PrepareReceiptSnapshotFrames(capture, policy)
}

func ValidateReceiptSnapshotGraphCapture(frames []*pb.SnapshotResponse, origins []OriginState) error {
	return dataruntime.ValidateReceiptSnapshotGraphCapture(frames, origins)
}

func ValidateReceiptSnapshotFrame(frame *pb.SnapshotResponse) error {
	return dataruntime.ValidateReceiptSnapshotFrame(frame)
}

func DecodeReceiptSnapshotFrames(frames []*pb.SnapshotResponse, policy mutationreceipt.Config, retired mutationreceipt.RetiredCatalogConfig) (ReceiptWholeStateCapture, error) {
	return dataruntime.DecodeReceiptSnapshotFrames(frames, policy, retired)
}

func ValidateDataSnapshotNamespace(frames []*pb.SnapshotResponse, expected string) error {
	return dataruntime.ValidateDataSnapshotNamespace(frames, expected)
}

func WithReplicationTailMetadata(ctx context.Context, values []string, header http.Header) context.Context {
	return dataruntime.WithReplicationTailMetadata(ctx, values, header)
}
