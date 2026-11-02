package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestServingLoggerRetainedAndDefaultShareLevel(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	var output bytes.Buffer
	owner := newServingLogger(ObservabilityConfig{LogLevel: slog.LevelWarn, LogFormat: "json"}, &output)
	logger := NewLogger(owner)
	retained := logger.With("scope", "retained")
	defaultRetained := slog.Default().With("scope", "default-retained")
	if logger != owner.logger || slog.Default() != logger {
		t.Fatal("NewLogger did not select and install the owned logger")
	}
	logger.Info("suppressed")
	retained.Debug("suppressed")
	slog.Info("suppressed")
	if output.Len() != 0 {
		t.Fatalf("startup level did not filter records: %s", &output)
	}
	owner.applyLevel(slog.LevelDebug)
	logger.Debug("direct")
	retained.Debug("retained")
	slog.Debug("default")
	defaultRetained.Debug("default-retained")
	decoder := json.NewDecoder(&output)
	for _, message := range []string{"direct", "retained", "default", "default-retained"} {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode %s: %v", message, err)
		}
		if record["msg"] != message || record["level"] != "DEBUG" || record["service"] != "lantern" {
			t.Fatalf("record = %+v", record)
		}
		if strings.Contains(message, "retained") && record["scope"] != message {
			t.Fatalf("retained attributes changed: %+v", record)
		}
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unexpected additional record: %+v, %v", extra, err)
	}
	owner.applyLevel(slog.LevelError)
	if logger.Enabled(context.Background(), slog.LevelWarn) || retained.Enabled(context.Background(), slog.LevelWarn) || defaultRetained.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("raising the level did not reach retained loggers")
	}
}

func TestServingLoggerOwnersAndBootstrapAreIndependent(t *testing.T) {
	first := newServingLogger(ObservabilityConfig{LogLevel: slog.LevelInfo}, io.Discard)
	second := newServingLogger(ObservabilityConfig{LogLevel: slog.LevelError}, io.Discard)
	bootstrap := newLogger(ObservabilityConfig{LogLevel: slog.LevelWarn}, io.Discard)
	first.applyLevel(slog.LevelDebug)
	ctx := context.Background()
	if !first.logger.Enabled(ctx, slog.LevelDebug) || second.logger.Enabled(ctx, slog.LevelWarn) || bootstrap.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("changing one owner changed another owner or bootstrap logger")
	}
	second.applyLevel(slog.LevelInfo)
	if !first.logger.Enabled(ctx, slog.LevelDebug) || !second.logger.Enabled(ctx, slog.LevelInfo) || bootstrap.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("independent owner levels diverged from their selected values")
	}
}

func TestServingLoggerFormats(t *testing.T) {
	for _, format := range []string{"json", "text", "TEXT"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			owner := newServingLogger(ObservabilityConfig{LogLevel: slog.LevelInfo, LogFormat: format}, &output)
			owner.logger.Warn("warning", "key", "value")
			if strings.EqualFold(format, "text") {
				for _, field := range []string{"level=WARN", "msg=warning", "service=lantern", "key=value"} {
					if !strings.Contains(output.String(), field) {
						t.Fatalf("text record missing %q: %s", field, &output)
					}
				}
			} else {
				var record map[string]any
				if err := json.Unmarshal(output.Bytes(), &record); err != nil {
					t.Fatal(err)
				}
				if record["level"] != "WARN" || record["msg"] != "warning" || record["service"] != "lantern" || record["key"] != "value" {
					t.Fatalf("json record = %+v", record)
				}
			}
		})
	}
}

func TestServingLoggerConcurrentApplication(t *testing.T) {
	owner := newServingLogger(ObservabilityConfig{LogLevel: slog.LevelInfo}, io.Discard)
	var workers sync.WaitGroup
	for i := range 16 {
		workers.Go(func() {
			logger := owner.logger.With("worker", i)
			for range 100 {
				owner.applyLevel(slog.LevelDebug)
				logger.Debug("debug")
				owner.applyLevel(slog.LevelError)
				logger.Error("error")
			}
		})
	}
	workers.Wait()
	owner.applyLevel(slog.LevelWarn)
	ctx := context.Background()
	if owner.logger.Enabled(ctx, slog.LevelInfo) || !owner.logger.Enabled(ctx, slog.LevelWarn) {
		t.Fatal("final owned level was not applied")
	}
}
