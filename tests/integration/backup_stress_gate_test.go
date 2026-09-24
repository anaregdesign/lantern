package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

type backupStressCall struct {
	Producer   string `json:"producer"`
	StartNS    int64  `json:"start_ns"`
	EndNS      int64  `json:"end_ns"`
	DurationNS int64  `json:"duration_ns"`
	OK         bool   `json:"ok"`
}

type backupStressTick struct {
	Source            string `json:"source"`
	TickID            string `json:"tick_id"`
	StartNS           int64  `json:"started_unix_ns"`
	EndNS             int64  `json:"finished_unix_ns"`
	MaterializationNS int64  `json:"materialization_ns"`
	SendNS            int64  `json:"send_ns"`
	FinalizationNS    int64  `json:"finalization_ns"`
	Vertices          int    `json:"vertices"`
	Edges             int    `json:"edges"`
}

type backupStressWindow struct {
	Count     int     `json:"count"`
	Errors    int     `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
	P99NS     int64   `json:"p99_ns"`
	Complete  bool    `json:"complete"`
}

type backupStressCorrelation struct {
	TickID   string             `json:"tick_id"`
	Producer string             `json:"producer"`
	Before   backupStressWindow `json:"before"`
	During   backupStressWindow `json:"during"`
	After    backupStressWindow `json:"after"`
}

// TestPeriodicBackupStress is opt-in. It seeds a host-memory graph directly
// to avoid millions of setup RPCs, then drives real Connect/h2c read/write
// producers while the actual Backupper.Run writes periodic file snapshots.
// It emits only timing, counts, and build metadata; no keys or graph values.
func TestPeriodicBackupStress(t *testing.T) {
	out := os.Getenv("LANTERN_BACKUP_STRESS_OUT")
	if out == "" {
		t.Skip("set LANTERN_BACKUP_STRESS_OUT for host periodic-backup stress")
	}
	sha := os.Getenv("LANTERN_BACKUP_STRESS_SHA")
	if sha == "" {
		t.Fatal("LANTERN_BACKUP_STRESS_SHA must identify the measured build")
	}
	vertices := backupStressInt(t, "LANTERN_BACKUP_STRESS_VERTICES", 100_000)
	degree := backupStressInt(t, "LANTERN_BACKUP_STRESS_DEGREE", 32)
	intervalMS := backupStressInt(t, "LANTERN_BACKUP_STRESS_INTERVAL_MS", 30_000)
	readRPS := backupStressInt(t, "LANTERN_BACKUP_STRESS_READ_RPS", 200)
	writeRPS := backupStressInt(t, "LANTERN_BACKUP_STRESS_WRITE_RPS", 50)
	if vertices < 100 || degree < 2 || degree >= vertices || intervalMS < 10 || readRPS < 1 || writeRPS < 1 {
		t.Fatalf("invalid backup stress parameters: vertices=%d degree=%d interval_ms=%d read_rps=%d write_rps=%d", vertices, degree, intervalMS, readRPS, writeRPS)
	}
	interval := time.Duration(intervalMS) * time.Millisecond
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
	keys := make([]string, vertices)
	vertexBatch := make([]graphcache.VertexItem[string, *pb.Vertex], 0, 1000)
	liveUntil := time.Now().Add(time.Hour)
	for i := range keys {
		keys[i] = fmt.Sprintf("stress:%08d", i)
		vertexBatch = append(vertexBatch, graphcache.VertexItem[string, *pb.Vertex]{
			Key: keys[i], Value: &pb.Vertex{Key: keys[i]}, Expiration: liveUntil,
		})
		if len(vertexBatch) == cap(vertexBatch) {
			if err := cache.PutVerticesWithExpiration(vertexBatch); err != nil {
				t.Fatal(err)
			}
			vertexBatch = vertexBatch[:0]
		}
	}
	if len(vertexBatch) > 0 {
		if err := cache.PutVerticesWithExpiration(vertexBatch); err != nil {
			t.Fatal(err)
		}
	}
	edgeBatch := make([]graphcache.EdgeItem[string], 0, 10_000)
	for tail := range keys {
		for offset := 1; offset <= degree; offset++ {
			head := (tail + offset) % vertices
			edgeBatch = append(edgeBatch, graphcache.EdgeItem[string]{Tail: keys[tail], Head: keys[head], Weight: 1, Expiration: liveUntil})
			if len(edgeBatch) == cap(edgeBatch) {
				cache.AddEdgesWithExpiration(edgeBatch)
				edgeBatch = edgeBatch[:0]
			}
		}
	}
	if len(edgeBatch) > 0 {
		cache.AddEdgesWithExpiration(edgeBatch)
	}
	if got, want := cache.EdgeCount(), vertices*degree; got != want {
		t.Fatalf("seeded edges = %d, want %d", got, want)
	}

	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	sdk := newConnectClientFor(t, srv.url)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	backupper := backup.New(svc, backup.Config{
		Enabled: true, Dir: t.TempDir(), Interval: interval, Retain: 1, InstanceID: "stress",
	}, nil, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backupDone := make(chan error, 1)
	go func() { backupDone <- backupper.Run(ctx) }()

	var callMu sync.Mutex
	calls := make([]backupStressCall, 0, 4*intervalMS)
	var producers sync.WaitGroup
	startProducer := func(name string, rps int, call func(context.Context, int) error) {
		producers.Add(1)
		go func() {
			defer producers.Done()
			ticker := time.NewTicker(time.Second / time.Duration(rps))
			defer ticker.Stop()
			seq := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					requestCtx, requestCancel := context.WithTimeout(ctx, 10*time.Second)
					started := time.Now()
					err := call(requestCtx, seq)
					finished := time.Now()
					requestCancel()
					callMu.Lock()
					calls = append(calls, backupStressCall{name, started.UnixNano(), finished.UnixNano(), finished.Sub(started).Nanoseconds(), err == nil})
					callMu.Unlock()
					seq++
				}
			}
		}()
	}
	startProducer("GetVertex", readRPS, func(ctx context.Context, seq int) error {
		_, err := sdk.GetVertex(ctx, keys[seq%min(vertices, 1000)])
		return err
	})
	startProducer("ScanVertices", readRPS, func(ctx context.Context, _ int) error {
		_, _, err := sdk.ScanVertices(ctx, "stress:000", client.WithScanLimit(32))
		return err
	})
	startProducer("Illuminate", readRPS, func(ctx context.Context, seq int) error {
		_, err := sdk.Illuminate(ctx, keys[seq%min(vertices, 1000)], client.WithBFS(client.BFSOpts{Step: 1, FanOut: 8}))
		return err
	})
	startProducer("PutVertex", writeRPS, func(ctx context.Context, seq int) error {
		_, err := sdk.PutVertex(ctx, keys[seq%min(vertices, 1000)], int64(seq), time.Hour)
		return err
	})
	// Four intervals provide three complete periodic ticks and a matched
	// post-backup window if each dump finishes before the next interval.
	time.Sleep(4 * interval)
	cancel()
	producers.Wait()
	if err := <-backupDone; err != nil {
		t.Fatal(err)
	}

	var ticks []backupStressTick
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record struct {
			Msg string `json:"msg"`
			backupStressTick
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Msg == "backup: wrote dump" && record.Source == "periodic" {
			ticks = append(ticks, record.backupStressTick)
		}
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i].StartNS < ticks[j].StartNS })
	var correlations []backupStressCorrelation
	for _, tick := range ticks {
		span := tick.EndNS - tick.StartNS
		if span <= 0 {
			continue
		}
		for _, name := range []string{"GetVertex", "ScanVertices", "Illuminate", "PutVertex"} {
			correlations = append(correlations, backupStressCorrelation{
				TickID: tick.TickID, Producer: name,
				Before: backupStressWindowFor(calls, name, tick.StartNS-span, tick.StartNS),
				During: backupStressWindowFor(calls, name, tick.StartNS, tick.EndNS),
				After:  backupStressWindowFor(calls, name, tick.EndNS, tick.EndNS+span),
			})
		}
	}
	signal := backupStressSignal(ticks, correlations)
	artifact := struct {
		SHA          string                    `json:"sha"`
		GoVersion    string                    `json:"go_version"`
		GOOS         string                    `json:"goos"`
		GOARCH       string                    `json:"goarch"`
		CPUs         int                       `json:"cpus"`
		Vertices     int                       `json:"vertices"`
		SeededEdges  int                       `json:"seeded_edges"`
		IntervalMS   int                       `json:"backup_interval_ms"`
		ReadRPS      int                       `json:"offered_read_rps_per_producer"`
		WriteRPS     int                       `json:"offered_write_rps"`
		Ticks        []backupStressTick        `json:"periodic_ticks"`
		Calls        []backupStressCall        `json:"calls"`
		Correlations []backupStressCorrelation `json:"correlations"`
		Signal       string                    `json:"synthetic_backup_signal"`
	}{sha, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), vertices,
		vertices * degree, intervalMS, readRPS, writeRPS, ticks, calls, correlations, signal}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("periodic backup stress: ticks=%d calls=%d signal=%s artifact=%s", len(ticks), len(calls), signal, out)
	if len(ticks) < 3 {
		t.Fatalf("completed periodic ticks = %d, want at least 3", len(ticks))
	}
}

func backupStressInt(t *testing.T, key string, def int) int {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		return def
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return n
}

func backupStressWindowFor(calls []backupStressCall, name string, from, until int64) backupStressWindow {
	var latencies []int64
	var window backupStressWindow
	for _, call := range calls {
		if call.Producer != name || call.StartNS < from || call.EndNS > until {
			continue
		}
		window.Count++
		if !call.OK {
			window.Errors++
		} else {
			latencies = append(latencies, call.DurationNS)
		}
	}
	// A p99 over only a handful of successes cannot qualify a window,
	// even when failures pushed the total call count past 100.
	window.Complete = window.Count >= 100 && len(latencies) >= 100 && window.Errors == 0
	if window.Count > 0 {
		window.ErrorRate = float64(window.Errors) / float64(window.Count)
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		window.P99NS = latencies[(99*len(latencies)+99)/100-1]
	}
	return window
}

func backupStressSignal(ticks []backupStressTick, correlations []backupStressCorrelation) string {
	if len(ticks) < 3 {
		return "unknown"
	}
	allConclusive := true
	for _, name := range []string{"GetVertex", "ScanVertices", "Illuminate"} {
		var streak, completeStreak int
		conclusiveTriple := false
		for _, tick := range ticks {
			var matched *backupStressCorrelation
			for i := range correlations {
				if correlations[i].TickID == tick.TickID && correlations[i].Producer == name {
					matched = &correlations[i]
					break
				}
			}
			if matched == nil || !matched.Before.Complete || !matched.During.Complete || !matched.After.Complete {
				streak = 0
				completeStreak = 0
				continue
			}
			completeStreak++
			if completeStreak >= 3 {
				conclusiveTriple = true
			}
			if matched.During.P99NS > 2*matched.Before.P99NS {
				streak++
				if streak >= 3 {
					return "fired"
				}
			} else {
				streak = 0
			}
		}
		if !conclusiveTriple {
			allConclusive = false
		}
	}
	if !allConclusive {
		return "unknown"
	}
	return "not_fired"
}

func TestBackupStressSignalIgnoresIncompleteTrailingTick(t *testing.T) {
	ticks := []backupStressTick{{TickID: "1"}, {TickID: "2"}, {TickID: "3"}, {TickID: "4"}}
	var rows []backupStressCorrelation
	for _, tick := range ticks[:3] {
		for _, name := range []string{"GetVertex", "ScanVertices", "Illuminate"} {
			rows = append(rows, backupStressCorrelation{
				TickID: tick.TickID, Producer: name,
				Before: backupStressWindow{Count: 100, P99NS: 100, Complete: true},
				During: backupStressWindow{Count: 100, P99NS: 110, Complete: true},
				After:  backupStressWindow{Count: 100, P99NS: 100, Complete: true},
			})
		}
	}
	if got := backupStressSignal(ticks, rows); got != "not_fired" {
		t.Fatalf("signal = %s, want not_fired with 3 complete ticks", got)
	}
	for i := range rows {
		if rows[i].Producer == "ScanVertices" {
			rows[i].During.P99NS = 201
		}
	}
	if got := backupStressSignal(ticks, rows); got != "fired" {
		t.Fatalf("signal = %s, want fired", got)
	}
}
