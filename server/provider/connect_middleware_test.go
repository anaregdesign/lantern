package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestSlowRPCManagedEntrySnapshotPersistsAcrossUpdate(t *testing.T) {
	var logs bytes.Buffer
	s := NewSlowRPCInterceptor(0, slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	var ticks atomic.Int64
	s.now = func() time.Time { return time.Unix(0, ticks.Add(int64(10*time.Millisecond))) }
	if err := s.ApplyManagedThresholdMilliseconds(0, 1); err != nil {
		t.Fatal(err)
	}
	nextMS, nextGeneration := int64(5), uint64(2)
	wrapped := s.ConnectInterceptor()(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		if err := s.ApplyManagedThresholdMilliseconds(nextMS, nextGeneration); err != nil {
			return nil, err
		}
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	// Disabled requests neither inspect request metadata nor time their handler.
	if _, err := wrapped(context.Background(), nil); err != nil || ticks.Load() != 0 || logs.Len() != 0 {
		t.Fatal("disabled request acquired timing or logging work", err)
	}
	nextMS, nextGeneration = 0, 3
	if _, err := wrapped(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
		t.Fatal(err)
	}
	var logged struct {
		ThresholdMS      int64  `json:"threshold_ms"`
		PolicyGeneration uint64 `json:"policy_generation"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &logged); err != nil {
		t.Fatal(err)
	}
	if logged.ThresholdMS != 5 || logged.PolicyGeneration != 2 {
		t.Fatalf("slow log mixed request-entry tuple with later policy: %+v", logged)
	}
	logs.Reset()
	before := ticks.Load()
	if _, err := wrapped(context.Background(), nil); err != nil || ticks.Load() != before || logs.Len() != 0 {
		t.Fatal("resident disabled owner still timed or logged the request", err)
	}
}

func TestSlowRPCLegacyLogsBootstrapGeneration(t *testing.T) {
	var logs bytes.Buffer
	s := NewSlowRPCInterceptor(time.Millisecond, slog.New(slog.NewJSONHandler(&logs, nil)))
	var ticks atomic.Int64
	s.now = func() time.Time { return time.Unix(0, ticks.Add(int64(10*time.Millisecond))) }
	wrapped := s.ConnectInterceptor()(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	if _, err := wrapped(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
		t.Fatal(err)
	}
	var logged map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &logged); err != nil || string(logged["policy_generation"]) != "0" {
		t.Fatal("bootstrap log omitted or invented managed generation", err)
	}
}
