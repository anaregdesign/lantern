package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/proto"
)

func TestCompactGraphStoreRequiresExistingNode(t *testing.T) {
	ctx := t.Context()
	var help strings.Builder
	if err := writeServerHelp(&help); err != nil || !strings.Contains(help.String(), "compact-graph-store") {
		t.Fatalf("server help %q, %v", help.String(), err)
	}
	help.Reset()
	if err := compactGraphStore(ctx, []string{"--help"}, &help); err != nil || !strings.Contains(help.String(), "--target DIRECTORY") || !strings.Contains(help.String(), "stopped Lantern node") {
		t.Fatalf("command help %q, %v", help.String(), err)
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	for _, args := range [][]string{nil, {"--target", missing}, {"--target", dir, "extra"}} {
		if err := compactGraphStore(ctx, args, io.Discard); err == nil {
			t.Fatalf("unexpected success for %q", args)
		}
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("refused invocation created files %v, %v", files, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := compactGraphStore(ctx, []string{"--target", dir}, io.Discard); err == nil {
		t.Fatal("accepted an unrelated directory")
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 1 || files[0].Name() != "unrelated" {
		t.Fatalf("refusal modified unrelated directory %v, %v", files, err)
	}

	rawDir := filepath.Join(t.TempDir(), "other-tarp")
	raw, err := tarp.Open(rawDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Put([]byte("other-application"), []byte("preserve")); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compactGraphStore(ctx, []string{"--target", rawDir}, io.Discard); !errors.Is(err, graphstore.ErrInvalidManifest) {
		t.Fatalf("foreign Tarp database refusal: %v", err)
	}
	raw, err = tarp.OpenExisting(ctx, rawDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	value, err := raw.Get([]byte("other-application"))
	if err != nil || string(value) != "preserve" || raw.Stats().LiveKeys != 1 {
		t.Fatalf("foreign database changed: value %q, live keys %d, error %v", value, raw.Stats().LiveKeys, err)
	}
}

func TestCompactGraphStorePreservesReceiptOnRestart(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	cfg := dataruntime.ReceiptDatabaseConfig{
		Path: filepath.Join(t.TempDir(), "graph"), NodeID: hlc.NodeID{0x31},
		Receipt: mutationreceipt.Config{Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
			MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: now},
		Log: mutationlog.Options{Capacity: 16, SubscriberBuffer: 2}, DefaultTTL: time.Hour,
		ConfigureGraph:  func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil },
		NamespaceFormat: keyspace.Version, Now: now, BaselineCodec: backup.ReceiptBaselineCodec{},
	}
	open := func(fresh bool) (*dataruntime.ServingRuntime, *dataruntime.Executor) {
		t.Helper()
		rt, err := dataruntime.OpenReceiptDatabase(ctx, cfg, fresh)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.Close() })
		svc := rt.NewExecutor(nil).WithTombstoneTTL(time.Hour)
		repl, err := rt.NewReplication(svc)
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.CertifyInstallation(svc, repl); err != nil {
			t.Fatal(err)
		}
		if err := rt.CertifyReceiptBackup(svc, repl); err != nil {
			t.Fatal(err)
		}
		if err := rt.ActivatePublicReceipts(svc, repl); err != nil {
			t.Fatal(err)
		}
		return rt, svc
	}
	rt, svc := open(true)
	args := []string{"--target", cfg.Path}
	var out strings.Builder
	if err := compactGraphStore(ctx, args, &out); !errors.Is(err, tarp.ErrLocked) || out.Len() != 0 {
		t.Fatalf("running node: %q, %v", out.String(), err)
	}
	capability, err := svc.Public().GetReceiptCapability(ctx, &pb.GetReceiptCapabilityRequest{})
	if err != nil || !capability.GetEnabled() {
		t.Fatalf("receipt capability %v, %v", capability, err)
	}
	id, err := mutationreceipt.NewID(cfg.Receipt.Epoch, now.Add(-time.Second), [24]byte{0x73})
	if err != nil {
		t.Fatal(err)
	}
	put := &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: "preserved", Value: &pb.Vertex_String_{String_: "original"}}, IfAbsent: true,
		ReceiptContext: &pb.MutationReceiptContext{OperationIds: [][]byte{id.Bytes()},
			LogicalCallId: bytes.Repeat([]byte{0x74}, 16), Endpoint: capability.GetEndpoint()},
	}
	first, err := svc.Public().PutVertex(ctx, put)
	if err != nil || first.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("original Put: %v, %v", first, err)
	}
	status := &pb.GetReceiptStatusRequest{OperationId: id.Bytes()}
	beforeStatus, err := svc.Public().GetReceiptStatus(ctx, status)
	if err != nil || beforeStatus.GetStatus().GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED {
		t.Fatalf("original status: %v, %v", beforeStatus, err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	cut := durableRuntimeCut(t, cfg.Path)
	if cut.ReceiptCount != 1 || cut.LocalSeq != 1 || cut.ReceiptEpoch != cfg.Receipt.Epoch {
		t.Fatalf("unexpected original cut %+v", cut)
	}
	if err := compactGraphStore(ctx, args, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("Node %x, unlinked ", cfg.NodeID)) {
		t.Fatalf("unexpected completion %q", out.String())
	}
	if after := durableRuntimeCut(t, cfg.Path); cut != after {
		t.Fatalf("changed recovery witness: before %+v, after %+v", cut, after)
	}
	rt, svc = open(false)
	got, err := svc.Public().GetVertex(ctx, &pb.GetVertexRequest{Key: put.Vertex.Key})
	if err != nil || got.GetVertex().GetString_() != "original" {
		t.Fatalf("recovered Vertex %v, %v", got, err)
	}
	afterStatus, err := svc.Public().GetReceiptStatus(ctx, status)
	if err != nil || !proto.Equal(beforeStatus.GetStatus().GetReceipt(), afterStatus.GetStatus().GetReceipt()) {
		t.Fatalf("recovered receipt %v, %v", afterStatus, err)
	}
	retried, err := svc.Public().PutVertex(ctx, put)
	if err != nil || !proto.Equal(first, retried) || svc.LocalSeq(cfg.NodeID) != 1 || rt.ReceiptStats().Entries != 1 {
		t.Fatalf("retried Put %v, origin %d, receipts %d, error %v", retried, svc.LocalSeq(cfg.NodeID), rt.ReceiptStats().Entries, err)
	}
}
