package provider

import (
	"context"
	"errors"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

type managedBackupCertified struct {
	manager *ManagedOperationalRuntime
	owner   *backup.Backupper
	adapter *managedBackupRuntime
}

type managedBackupRuntime struct {
	owner            *backup.Backupper
	bootstrapSources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
}

func NewManagedBackupRuntime(ctx context.Context, manager *ManagedOperationalRuntime, owner *backup.Backupper) (managedBackupCertified, error) {
	if ctx.Err() != nil || manager == nil {
		return managedBackupCertified{}, errManagedOperational
	}
	sources, err := manager.BootstrapSources(operationalpolicy.GroupBackup)
	if err != nil {
		return managedBackupCertified{}, errManagedOperational
	}
	adapter := &managedBackupRuntime{owner: owner, bootstrapSources: sources}
	if err := adapter.AvailableOperationalPolicy(operationalpolicy.GroupBackup, operationalpolicy.Version); err != nil {
		return managedBackupCertified{}, err
	}
	if err := manager.Register(ctx, operationalpolicy.GroupBackup, adapter); err != nil {
		return managedBackupCertified{}, err
	}
	return managedBackupCertified{manager, owner, adapter}, nil
}

func (b *managedBackupRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if b == nil || b.owner == nil || group != operationalpolicy.GroupBackup || version != operationalpolicy.Version || b.owner.AvailableProduction() != nil {
		return errManagedOperational
	}
	return nil
}

func managedBackupConfig(values operationalpolicy.Values) (backup.ProductionConfig, error) {
	if values.Validate() != nil || values.Backup == nil || uint64(values.Backup.Retain) > uint64(^uint(0)>>1) {
		return backup.ProductionConfig{}, errManagedOperational
	}
	return backup.ProductionConfig{Enabled: values.Backup.Enabled, Interval: time.Duration(values.Backup.IntervalNanos), Retain: int(values.Backup.Retain)}, nil
}

func (b *managedBackupRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if b.AvailableOperationalPolicy(operationalpolicy.GroupBackup, operationalpolicy.Version) != nil || change.Validate() != nil {
		return errManagedOperational
	}
	config, err := managedBackupConfig(change.Values)
	if err != nil || b.owner.ValidateProduction(config) != nil {
		return errManagedOperational
	}
	return nil
}

func (b *managedBackupRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if b.AvailableOperationalPolicy(operationalpolicy.GroupBackup, operationalpolicy.Version) != nil || policy.Validate() != nil {
		return errManagedOperational
	}
	config, err := managedBackupConfig(policy.Values)
	if err != nil {
		return err
	}
	return b.owner.ApplyProduction(backup.ProductionSnapshot{Generation: policy.Generation, Config: config})
}

func (b *managedBackupRuntime) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if b == nil || b.owner == nil {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	available := b.owner.AvailableProduction()
	if available != nil && !errors.Is(available, backup.ErrProductionStopped) {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	actual := b.owner.Production()
	observation := OperationalOwnerObservation{EffectiveGeneration: actual.Generation, Mode: securitytransport.OperationalModeHot, Error: securitytransport.OperationalErrorNone}
	if actual.Generation == 0 {
		if len(b.bootstrapSources) != 3 {
			return observation, errManagedOperational
		}
		observation.Sources = cloneOperationalSources(b.bootstrapSources)
	} else {
		observation.Sources = map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource{
			pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_ENABLED:        securitytransport.OperationalSourceManaged,
			pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_INTERVAL_NANOS: securitytransport.OperationalSourceManaged,
			pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_RETAIN:         securitytransport.OperationalSourceManaged,
		}
	}
	values := operationalpolicy.Values{Backup: &operationalpolicy.Backup{Enabled: actual.Config.Enabled, IntervalNanos: int64(actual.Config.Interval), Retain: int64(actual.Config.Retain)}}
	if values.Validate() != nil {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	observation.Effective = &values
	if available != nil {
		observation.Error = securitytransport.OperationalErrorOwnerUnavailable
	} else if b.owner.ValidateProduction(actual.Config) != nil {
		observation.Error = securitytransport.OperationalErrorDependencyUnavailable
	}
	return observation, nil
}

// NewManagedBackupPreviewer exposes only a bounded connected-node sample.
// The catalog fingerprint is informational, separate from the native policy
// acknowledgement/CAS. No candidate path or filesystem error crosses this seam.
func NewManagedBackupPreviewer(certified managedBackupCertified) securitytransport.OperationalBackupPreviewer {
	return func(ctx context.Context, retain int64) (securitytransport.OperationalBackupRetentionPreview, error) {
		if certified.manager == nil || certified.owner == nil || certified.adapter == nil || certified.adapter.owner != certified.owner || certified.adapter.AvailableOperationalPolicy(operationalpolicy.GroupBackup, operationalpolicy.Version) != nil || retain < 0 || uint64(retain) > uint64(^uint(0)>>1) {
			return securitytransport.OperationalBackupRetentionPreview{}, errManagedOperational
		}
		preview, err := certified.owner.PreviewRetention(ctx, int(retain))
		if err != nil && !errors.Is(err, backup.ErrRetentionPreviewLimit) {
			return securitytransport.OperationalBackupRetentionPreview{}, errManagedOperational
		}
		if preview.ValidBackups < 0 || preview.ValidBytes < 0 || preview.PruneBackups < 0 || preview.PruneBytes < 0 || preview.PruneBackups > preview.ValidBackups || preview.PruneBytes > preview.ValidBytes {
			return securitytransport.OperationalBackupRetentionPreview{}, errManagedOperational
		}
		return securitytransport.OperationalBackupRetentionPreview{ValidBackups: uint64(preview.ValidBackups), ValidBytes: uint64(preview.ValidBytes), PruneBackups: uint64(preview.PruneBackups), PruneBytes: uint64(preview.PruneBytes), Complete: preview.Complete, Fingerprint: preview.Fingerprint}, nil
	}
}

// operationalBackupPreview is deferred until the real producer is registered.
// Authority can install this callback before the Backupper and management
// runtime exist without adding a construction cycle or guessing an inventory.
func (r *SecurityRuntime) operationalBackupPreview(ctx context.Context, retain int64) (securitytransport.OperationalBackupRetentionPreview, error) {
	if r == nil || r.managedOperational == nil {
		return securitytransport.OperationalBackupRetentionPreview{}, errManagedOperational
	}
	manager := r.managedOperational
	manager.mu.Lock()
	entry := manager.backup
	manager.mu.Unlock()
	if entry == nil {
		return securitytransport.OperationalBackupRetentionPreview{}, errManagedOperational
	}
	owner, ok := entry.owner.(*managedBackupRuntime)
	if !ok || owner == nil {
		return securitytransport.OperationalBackupRetentionPreview{}, errManagedOperational
	}
	return NewManagedBackupPreviewer(managedBackupCertified{manager, owner.owner, owner})(ctx, retain)
}
