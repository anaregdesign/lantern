package backup

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func TestProductionValidationAndGenerations(t *testing.T) {
	for _, owner := range []*Backupper{nil, {}} {
		if !errors.Is(owner.AvailableProduction(), ErrProductionUnavailable) {
			t.Fatal("absent owner advertised production support")
		}
	}
	base := ProductionConfig{Enabled: true, Interval: time.Second + 7, Retain: 3}
	for _, config := range []ProductionConfig{
		{Interval: 0}, {Interval: -1}, {Interval: time.Second, Retain: -1},
	} {
		if config.Validate() == nil {
			t.Errorf("invalid configuration accepted: %+v", config)
		}
	}
	if err := (ProductionConfig{Interval: time.Second, Retain: int(^uint(0) >> 1)}).Validate(); err != nil {
		t.Fatalf("large safe retention rejected: %v", err)
	}
	b := New(&fakeService{}, testConfig(t.TempDir()), nil, nil)
	if err := b.AvailableProduction(); err != nil {
		t.Fatal(err)
	}
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 1, Config: base}); err != nil {
		t.Fatal(err)
	}
	if got := b.Production(); got.Generation != 1 || got.Config != base {
		t.Fatalf("effective snapshot = %+v", got)
	}
	if err := b.ApplyProduction(b.Production()); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	for _, next := range []ProductionSnapshot{
		{Config: base}, {Generation: 1, Config: ProductionConfig{Interval: time.Second}},
	} {
		if err := b.ApplyProduction(next); !errors.Is(err, ErrProductionConflict) {
			t.Fatalf("conflict result = %v", err)
		}
	}
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 4, Config: base}); err != nil {
		t.Fatalf("latest verified generation: %v", err)
	}
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 3, Config: base}); !errors.Is(err, ErrProductionConflict) {
		t.Fatalf("regression result = %v", err)
	}
	b.stopProduction()
	if !errors.Is(b.AvailableProduction(), ErrProductionStopped) {
		t.Fatal("stopped production owner advertised support")
	}
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 5, Config: base}); !errors.Is(err, ErrProductionStopped) {
		t.Fatalf("stopped result = %v", err)
	}
	missing := New(&fakeService{}, Config{}, nil, nil)
	if err := missing.ApplyProduction(ProductionSnapshot{Generation: 1, Config: base}); err == nil || missing.Production().Generation != 0 {
		t.Fatalf("missing destination apply = %v, %+v", err, missing.Production())
	}
	base.Enabled = false
	if err := missing.ApplyProduction(ProductionSnapshot{Generation: 1, Config: base}); err != nil {
		t.Fatalf("disabled policy needs no destination: %v", err)
	}
}

func TestRetentionAcknowledgementRequired(t *testing.T) {
	for _, test := range []struct {
		generation uint64
		before     int
		after      int
		want       bool
	}{
		{0, 3, 3, true}, {0, 3, 0, false}, {1, 0, 3, true},
		{1, 3, 2, true}, {1, 3, 3, false}, {1, 3, 4, false}, {1, 3, 0, false},
	} {
		current := ProductionSnapshot{Generation: test.generation, Config: ProductionConfig{Retain: test.before}}
		if got := RetentionAcknowledgementRequired(current, ProductionConfig{Retain: test.after}); got != test.want {
			t.Errorf("generation %d retain %d→%d = %v, want %v", test.generation, test.before, test.after, got, test.want)
		}
	}
}

type productionTestTimer struct {
	resets []time.Duration
	stops  int
}

func (timer *productionTestTimer) Reset(interval time.Duration) {
	timer.resets = append(timer.resets, interval)
}
func (timer *productionTestTimer) Stop() { timer.stops++ }

func TestProductionOnlySchedulingChangesResetCadence(t *testing.T) {
	b := New(&fakeService{}, testConfig(t.TempDir()), nil, nil)
	timer := &productionTestTimer{}
	b.productionTicker = timer
	config := b.Production().Config
	config.Retain = 1
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 1, Config: config}); err != nil {
		t.Fatal(err)
	}
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 2, Config: config}); err != nil {
		t.Fatal(err)
	}
	if len(timer.resets) != 0 || timer.stops != 0 {
		t.Fatal("retention or unrelated authority generation reset backup cadence")
	}
	config.Interval += 7
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 3, Config: config}); err != nil {
		t.Fatal(err)
	}
	config.Enabled = false
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 4, Config: config}); err != nil {
		t.Fatal(err)
	}
	config.Enabled = true
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 5, Config: config}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(timer.resets, []time.Duration{config.Interval, config.Interval}) || timer.stops != 1 {
		t.Fatalf("scheduling changes = resets %v, stops %d", timer.resets, timer.stops)
	}
}

type productionTestService struct {
	fakeService
	calls chan struct{}
	hook  func()
}

func (s *productionTestService) BackupSnapshot(ctx context.Context, request *pb.BackupSnapshotRequest, sender dataruntime.Sender[pb.BackupSnapshotResponse]) error {
	if s.hook != nil {
		s.hook()
	}
	select {
	case s.calls <- struct{}{}:
	default:
	}
	return s.fakeService.BackupSnapshot(ctx, request, sender)
}

func TestProductionResidentEnableDisableAndRestoreIndependence(t *testing.T) {
	service := &productionTestService{calls: make(chan struct{}, 8)}
	cfg := testConfig(t.TempDir())
	cfg.Enabled = false
	b := New(service, cfg, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("production owner did not stop")
		}
	})
	config := ProductionConfig{Enabled: true, Interval: 10 * time.Millisecond, Retain: 0}
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 1, Config: config}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled resident owner could not be enabled")
	}
	config.Enabled = false
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 2, Config: config}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.backupOnceWithSource(t.Context(), "periodic"); err != nil {
		t.Fatal(err)
	}
	if !b.cfg.RestoreOnStart || !reflect.DeepEqual(b.cfg, cfg) {
		t.Fatalf("production changed bootstrap/restore configuration: %+v", b.cfg)
	}
	// Manual production remains available with periodic production disabled.
	if _, err := b.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RestoreOnStartup(t.Context()); err != nil {
		t.Fatalf("managed disable suppressed startup restoration: %v", err)
	}
	if service.beginRecovery != 1 {
		t.Fatal("bootstrap-selected restore was not performed")
	}
}

func TestProductionAttemptPinsRetentionAndWaiterCapturesLatest(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		t.Run(map[bool]string{false: "graph", true: "receipt"}[receipt], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			block := false
			hook := func() {
				if block {
					once.Do(func() { close(entered); <-release })
				}
			}
			cfg := testConfig(t.TempDir())
			cfg.Retain = 0
			var b *Backupper
			if receipt {
				archive := wholeStateArchiveFixture(t)
				source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
				source.hook = func(context.Context) error { hook(); return nil }
				var err error
				b, err = NewReceipt(&fakeService{}, source, archive.Policy, cfg, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				b = New(&productionTestService{calls: make(chan struct{}, 4), hook: hook}, cfg, nil, nil)
				var stamp int64
				b.now = func() time.Time { stamp++; return time.Unix(0, stamp) }
			}
			for range 3 {
				if _, err := b.BackupNow(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			block = true
			done := make(chan error, 1)
			go func() { _, err := b.BackupNow(t.Context()); done <- err }()
			<-entered
			next := ProductionSnapshot{Generation: 1, Config: ProductionConfig{Interval: time.Hour, Retain: 1}}
			if err := b.ApplyProduction(next); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadDir(cfg.Dir)
			if err != nil {
				t.Fatal(err)
			}
			releaseOnce.Do(func() { close(release) })
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			pinned, err := b.PreviewRetention(t.Context(), 1)
			if err != nil || pinned.ValidBackups != 4 {
				t.Fatalf("in-flight retention changed: %+v, %v", pinned, err)
			}
			// A second completed generation-1 attempt prunes the first one.
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			preview, err := b.PreviewRetention(t.Context(), 1)
			if err != nil || !preview.Complete || preview.ValidBackups != 1 {
				t.Fatalf("new attempt did not capture latest retention: %+v, %v", preview, err)
			}
			if receipt && len(before) != 3*(receiptBackupSetExpectedMemberCount+1) {
				t.Fatal("Apply changed receipt files while capture was blocked")
			}
		})
	}
}
