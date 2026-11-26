package backup

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
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
	b := testBackupper(t, testConfig(t.TempDir()))
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
	missing := testBackupper(t, Config{})
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
	b := testBackupper(t, testConfig(t.TempDir()))
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

func TestProductionResidentEnableDisableAndRestoreIndependence(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Enabled = false
	b := testBackupper(t, cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	config := ProductionConfig{Enabled: true, Interval: 10 * time.Millisecond}
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 1, Config: config}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		points, err := b.graphPoints(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(points) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disabled owner did not start periodic production")
		}
		time.Sleep(time.Millisecond)
	}
	config.Enabled = false
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 2, Config: config}); err != nil {
		t.Fatal(err)
	}
	if stats, err := b.backupOnceWithSource(ctx, "periodic"); err != nil || stats.Bytes != 0 {
		t.Fatal(stats, err)
	}
	if stats, err := b.BackupNow(ctx); err != nil || stats.Bytes == 0 {
		t.Fatal(stats, err)
	}
	if !reflect.DeepEqual(b.cfg, cfg) {
		t.Fatal("managed policy altered startup configuration")
	}
}

func TestProductionAttemptPinsRetentionAndWaiterCapturesLatest(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Retain = 0
	b := testBackupper(t, cfg)
	for range 3 {
		if _, err := b.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	syncDir := b.fs.syncDir
	b.fs.syncDir = func(p string) error { once.Do(func() { close(entered); <-release }); return syncDir(p) }
	done := make(chan error, 1)
	go func() { _, err := b.BackupNow(t.Context()); done <- err }()
	<-entered
	next := ProductionSnapshot{Generation: 1, Config: ProductionConfig{Interval: time.Hour, Retain: 1}}
	err := b.ApplyProduction(next)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		p, err := b.PreviewRetention(t.Context(), 1)
		if err != nil || !p.Complete || p.ValidBackups != want {
			t.Fatal(p, err)
		}
	}
	check(4)
	if _, err := b.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(1)
}
