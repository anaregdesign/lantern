package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var productionReceiptEpoch = mutationreceipt.Epoch{
	0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
	0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
}

func productionReceiptEdgeDeleteMutation(
	t *testing.T,
	config mutationreceipt.Config,
	origin hlc.NodeID,
	seq uint64,
	tail, head string,
) *pb.Mutation {
	t.Helper()
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Second)
	id, err := mutationreceipt.NewID(config.Epoch, issued, [24]byte{0x39, byte(seq)})
	if err != nil {
		t.Fatal(err)
	}
	group := mutationreceipt.GroupID{0x4a}
	canonical := []byte{byte(mutationreceipt.DeleteEdge)}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(tail)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, tail...)
	binary.BigEndian.PutUint64(length[:], uint64(len(head)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, head...)
	digest := mutationreceipt.IntentDigest(canonical)
	policy := store.PolicyFingerprint()
	stamp := hlc.Timestamp{
		WallNs: time.Now().Add(100 * time.Millisecond).UnixNano(),
		NodeID: origin,
	}
	return &pb.Mutation{
		Seq: seq, Origin: origin[:], NamespaceFormat: "namespaced-v1",
		Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, NodeId: origin[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeDelete{
			ReplicatedReceiptEdgeDelete: &pb.ReplicatedReceiptEdgeDelete{
				DeploymentEpoch:     config.Epoch[:],
				PolicyFingerprint:   policy[:],
				TombstoneExpiration: timestamppb.New(time.Unix(0, stamp.WallNs).Add(30 * time.Minute)),
				Items: []*pb.ReplicatedReceiptEdgeDeleteItem{{
					Key: &pb.EdgeKey{Tail: "data:" + tail, Head: "data:" + head},
					Receipt: &pb.MutationReceipt{
						OperationId: id.Bytes(), LogicalCallId: group[:],
						ItemIndex: 0, ItemCount: 1, IntentSha256: digest[:],
						DeadlineUnixMs: uint64(issued.Add(config.Retention).UnixMilli()),
						OriginalResult: &pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{
							DeleteEdgeExisted: false,
						}},
					},
					CausallyAccepted: false,
				}},
			},
		}},
	}
}

func productionReceiptOperationID(t *testing.T, epoch mutationreceipt.Epoch, seed byte) []byte {
	t.Helper()
	id, err := mutationreceipt.NewID(epoch, time.Now().Add(-time.Second), [24]byte{seed})
	if err != nil {
		t.Fatal(err)
	}
	return id.Bytes()
}

func setDurableRuntimeEnv(t *testing.T, mode, path string, port int) {
	t.Helper()
	envconfig.ResetForTesting()
	t.Cleanup(envconfig.ResetForTesting)
	t.Setenv("LANTERN_PORT", strconv.Itoa(port))
	t.Setenv("LANTERN_NODE_ID", "31313131313131313131313131313131")
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", mode)
	t.Setenv("LANTERN_RECEIPT_WAL_PATH", path)
	t.Setenv("LANTERN_RECEIPT_EPOCH", "42424242424242424242424242424242")
	t.Setenv("LANTERN_RECEIPT_RETENTION", "1h")
	t.Setenv("LANTERN_RECEIPT_MAX_ENTRIES", "32")
	t.Setenv("LANTERN_RECEIPT_MAX_BYTES", "1048576")
	t.Setenv("LANTERN_BACKUP_ENABLED", "false")
	t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")
}

func reserveRuntimeTestPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return listener, port
}

func durableRuntimeClockHighWater(t *testing.T, path string) int64 {
	t.Helper()
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
		},
		Retention:  time.Hour,
		MaxEntries: 32,
		MaxBytes:   1 << 20,
	}
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := mutationreceipt.ResumeClockJournal(
		lease.Path(),
		config.Epoch,
		store.PolicyFingerprint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	return journal.HighWaterMillis()
}

func TestWireRuntimeCertificationPrecedesNetworkConsumers(t *testing.T) {
	source, err := os.ReadFile("wire_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	ordered := []string{
		"provider.NewServingRuntime(",
		"newLanternService(",
		"newLanternReplicationService(",
		"provider.NewRuntimeRestored(",
		"provider.NewRuntimeCertified(",
		"provider.NewBackupper(",
		"provider.NewPublicSecurityCertified(",
		"provider.NewOIDCPublicReceiptsCertified(",
		"provider.NewListener(",
		"provider.NewServingMetricsServer(",
		"provider.NewWorkloadPeerTransport(",
		"provider.NewSnapshotInstallerSelection(",
		"provider.NewReplicationPump(",
		"provider.NewAntiEntropyDriver(",
	}
	previous := -1
	for _, needle := range ordered {
		index := strings.Index(text, needle)
		if index < 0 {
			t.Fatalf("generated injector is missing %q", needle)
		}
		if index <= previous {
			t.Fatalf("generated injector constructs %q out of certification order", needle)
		}
		previous = index
	}
	if !strings.Contains(text, "cleanup4()\n\t\tcleanup3()\n\t\tcleanup2()\n\t\tcleanup()") {
		t.Fatal("generated injector does not release listener before the serving runtime")
	}
	if strings.Count(text, "provider.NewSnapshotInstallerSelection(") != 1 ||
		strings.Count(text, "provider.NewWorkloadPeerTransport(") != 1 ||
		!strings.Contains(text, "peerTransport, lanternService, graphCache, metrics, logger, snapshotInstallerSelection, runtimeCertified)") ||
		!strings.Contains(text, "peerTransport, lanternService, graphCache, pump, antiEntropyMetrics, logger, snapshotInstallerSelection)") {
		t.Fatal("generated injector does not share the certified peer transport and Snapshot installer across Pump and anti-entropy")
	}
}

func TestInitializeAppDurableBackupProductionUsesCertifiedRuntime(t *testing.T) {
	probe, port := reserveRuntimeTestPort(t)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "receipts.wal")
	backupDir := filepath.Join(root, "backups")
	setDurableRuntimeEnv(t, "fresh", path, port)
	t.Setenv("LANTERN_BACKUP_ENABLED", "true")
	t.Setenv("LANTERN_BACKUP_DIR", backupDir)
	t.Setenv("LANTERN_BACKUP_INSTANCE_ID", "production-receipt")
	t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")
	envconfig.ResetForTesting()

	app, cleanup, err := initializeApp()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	stats, err := app.backupper.BackupNow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Members != 3 || stats.Bytes <= 0 {
		t.Fatalf("production receipt backup stats = %+v", stats)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	var manifests int
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".set.json") {
			manifests++
		}
		if strings.HasSuffix(entry.Name(), ".lbk") || strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("production durable backup emitted legacy or temporary file %q", entry.Name())
		}
	}
	if len(entries) != 4 || manifests != 1 {
		t.Fatalf("production durable backup files = %+v, want three members and one manifest", entries)
	}
	capability, err := app.svc.GetReceiptCapability(t.Context(), &pb.GetReceiptCapabilityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if capability.GetEnabled() {
		t.Fatal("durable backup production enabled public receipt capability")
	}
	if _, err := app.svc.GetReceiptStatus(
		t.Context(),
		&pb.GetReceiptStatusRequest{
			OperationId: productionReceiptOperationID(t, productionReceiptEpoch, 0x81),
		},
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("durable backup production receipt status = %v, want FailedPrecondition", err)
	}
}

func TestInitializeAppDurableProductionWritesRestart(t *testing.T) {
	probe, port := reserveRuntimeTestPort(t)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipts.wal")
	setDurableRuntimeEnv(t, "fresh", path, port)
	t.Setenv("LANTERN_MAX_VERTEX_CAUSAL_ENTRIES", "2")
	envconfig.ResetForTesting()

	app, cleanup, err := initializeApp()
	if err != nil {
		t.Fatal(err)
	}
	if app == nil || cleanup == nil || !app.runtime.DurableReceiptWAL() {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("fresh durable app = %p, cleanup %v", app, cleanup != nil)
	}
	generation, err := os.ReadFile(path + ".generation")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}

	ctx := context.Background()
	future := timestamppb.New(time.Now().Add(time.Hour))
	for _, key := range []string{"local-live", "local-remove"} {
		response, err := app.svc.PutVertex(ctx, &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "data:" + key, Expiration: future},
		})
		if err != nil {
			cleanup()
			t.Fatalf("local Put %q: %v", key, err)
		}
		if response.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
			cleanup()
			t.Fatalf("local Put %q outcome = %v", key, response.GetOutcome())
		}
	}
	if response, err := app.svc.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "data:local-remove"}); err != nil {
		cleanup()
		t.Fatalf("local Delete: %v", err)
	} else if !response.GetExisted() {
		cleanup()
		t.Fatal("local Delete did not remove the existing vertex")
	}
	receiptTail, receiptHead := "receipt-tail", "receipt-head"
	if response, err := app.svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: "data:" + receiptTail, Head: "data:" + receiptHead, Weight: 1, Expiration: future,
	}}); err != nil || response.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		cleanup()
		t.Fatalf("local receipt fixture PutEdge = (%v, %v)", response, err)
	}

	remote := hlc.NodeID{0x72}
	base := time.Now()
	remoteOps := []*pb.MutationOp{
		{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{
				Key:        "data:remote-barrier",
				Expiration: timestamppb.New(base.Add(-time.Minute)),
			},
		}}},
		{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "data:remote-live", Expiration: future},
		}}},
		{Op: &pb.MutationOp_DeleteVertex{DeleteVertex: &pb.DeleteVertexRequest{
			Key: "data:remote-absent",
		}}},
	}
	for i, op := range remoteOps {
		seq := uint64(i + 1)
		mutation := &pb.Mutation{
			NamespaceFormat: "namespaced-v1",
			Origin:          remote[:],
			Seq:             seq,
			Hlc: &pb.HLCTimestamp{
				NodeId: remote[:],
				WallNs: base.Add(time.Duration(i) * time.Nanosecond).UnixNano(),
			},
			Op: op,
		}
		if _, ok := op.GetOp().(*pb.MutationOp_DeleteVertex); ok {
			mutation.TombstoneExpiration = timestamppb.New(base.Add(time.Hour))
		}
		if err := app.svc.ApplyMutation(ctx, mutation); err != nil {
			cleanup()
			t.Fatalf("remote mutation %d: %v", seq, err)
		}
	}
	receiptConfig := mutationreceipt.Config{
		Epoch: app.cfg.ReceiptWAL.Epoch, Retention: app.cfg.ReceiptWAL.Retention,
		MaxEntries: app.cfg.ReceiptWAL.MaxEntries, MaxBytes: uint64(app.cfg.ReceiptWAL.MaxBytes),
	}
	receiptMutation := productionReceiptEdgeDeleteMutation(
		t, receiptConfig, remote, 4, receiptTail, receiptHead,
	)
	if err := app.svc.ApplyMutation(ctx, receiptMutation); err != nil {
		cleanup()
		t.Fatalf("production durable receipt follower apply: %v", err)
	}
	if response, err := app.svc.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "data:" + receiptTail, Head: "data:" + receiptHead}); response != nil ||
		connect.CodeOf(err) != connect.CodeNotFound {
		cleanup()
		t.Fatalf("receipt follower graph decision = %v, %v, want absent", response, err)
	}
	if stats := app.runtime.GraphCache().CausalMetadataStats(); stats.VertexEntries != 5 ||
		!stats.VertexOverLimit || stats.VertexRejected != 0 {
		cleanup()
		t.Fatalf("fresh causal metadata = %+v, want five accepted over a limit of two", stats)
	}
	if length, capacity, evicted := app.runtime.MutationLogStats(); length != 8 ||
		capacity < length || evicted != 0 {
		cleanup()
		t.Fatalf("fresh mutation Log = len %d cap %d evicted %d, want 8, >=8, 0", length, capacity, evicted)
	}
	if got := app.svc.LocalSeq(app.cfg.Replication.NodeID); got != 4 {
		cleanup()
		t.Fatalf("fresh local origin sequence = %d, want 4", got)
	}
	if got := app.svc.LocalSeq(remote); got != 4 {
		cleanup()
		t.Fatalf("fresh remote origin sequence = %d, want 4", got)
	}
	cleanup()

	firstHighWater := durableRuntimeClockHighWater(t, path)
	if firstHighWater <= 0 {
		t.Fatalf("fresh clock journal high-water = %d, want positive", firstHighWater)
	}
	setDurableRuntimeEnv(t, "restart", path, port)
	t.Setenv("LANTERN_MAX_VERTEX_CAUSAL_ENTRIES", "2")
	envconfig.ResetForTesting()
	restarted, cleanupRestart, err := initializeApp()
	if err != nil {
		t.Fatal(err)
	}
	if restarted == nil || cleanupRestart == nil || !restarted.runtime.DurableReceiptWAL() {
		if cleanupRestart != nil {
			cleanupRestart()
		}
		t.Fatalf("restarted durable app = %p, cleanup %v", restarted, cleanupRestart != nil)
	}
	restartedGeneration, err := os.ReadFile(path + ".generation")
	if err != nil {
		cleanupRestart()
		t.Fatal(err)
	}
	if string(restartedGeneration) != string(generation) {
		cleanupRestart()
		t.Fatal("durable generation bytes changed across restart")
	}
	if length, capacity, evicted := restarted.runtime.MutationLogStats(); length != 8 ||
		capacity < length || evicted != 0 {
		cleanupRestart()
		t.Fatalf("restarted mutation Log = len %d cap %d evicted %d, want 8, >=8, 0", length, capacity, evicted)
	}
	if got := restarted.svc.LocalSeq(restarted.cfg.Replication.NodeID); got != 4 {
		cleanupRestart()
		t.Fatalf("restarted local origin sequence = %d, want 4", got)
	}
	if got := restarted.svc.LocalSeq(remote); got != 4 {
		cleanupRestart()
		t.Fatalf("restarted remote origin sequence = %d, want 4", got)
	}
	for _, key := range []string{"local-live", "remote-live"} {
		response, err := restarted.svc.GetVertex(ctx, &pb.GetVertexRequest{Key: "data:" + key})
		if err != nil || response.GetVertex().GetKey() != "data:"+key {
			cleanupRestart()
			t.Fatalf("restarted live vertex %q = %v, %v", key, response.GetVertex(), err)
		}
	}
	for _, key := range []string{"local-remove", "remote-barrier", "remote-absent"} {
		if response, err := restarted.svc.GetVertex(ctx, &pb.GetVertexRequest{Key: "data:" + key}); response != nil ||
			connect.CodeOf(err) != connect.CodeNotFound {
			cleanupRestart()
			t.Fatalf("restarted absent vertex %q = %v, %v", key, response, err)
		}
	}
	if got := restarted.runtime.GraphCache().CountByPrefix("data:remote-"); got != 1 {
		cleanupRestart()
		t.Fatalf("rebuilt prefix index count = %d, want 1", got)
	}
	if response, err := restarted.svc.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "data:" + receiptTail, Head: "data:" + receiptHead}); response != nil ||
		connect.CodeOf(err) != connect.CodeNotFound {
		cleanupRestart()
		t.Fatalf("restarted receipt follower graph decision = %v, %v, want absent", response, err)
	}
	if err := restarted.svc.ApplyMutation(ctx, receiptMutation); err != nil {
		cleanupRestart()
		t.Fatalf("restarted receipt follower duplicate: %v", err)
	}
	if length, _, _ := restarted.runtime.MutationLogStats(); length != 8 {
		cleanupRestart()
		t.Fatalf("receipt follower duplicate changed mutation Log length to %d", length)
	}
	if stats := restarted.runtime.GraphCache().CausalMetadataStats(); stats.VertexEntries != 5 ||
		!stats.VertexOverLimit || stats.VertexRejected != 0 {
		cleanupRestart()
		t.Fatalf("restarted causal metadata = %+v, want five certified effects over a limit of two", stats)
	}
	capability, err := restarted.svc.GetReceiptCapability(ctx, &pb.GetReceiptCapabilityRequest{})
	if err != nil {
		cleanupRestart()
		t.Fatal(err)
	}
	if capability.GetEnabled() {
		cleanupRestart()
		t.Fatal("durable restart enabled public receipt capability")
	}
	if _, err := restarted.svc.GetReceiptStatus(ctx, &pb.GetReceiptStatusRequest{
		OperationId: productionReceiptOperationID(t, productionReceiptEpoch, 0x82),
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		cleanupRestart()
		t.Fatalf("durable restart receipt status error = %v, want failed precondition", err)
	}
	cleanupRestart()

	secondHighWater := durableRuntimeClockHighWater(t, path)
	if secondHighWater < firstHighWater {
		t.Fatalf("clock journal high-water moved backward across restart: %d < %d", secondHighWater, firstHighWater)
	}
}

func TestInitializeAppDurableFailureClosesPartialOwners(t *testing.T) {
	blocker, port := reserveRuntimeTestPort(t)
	path := filepath.Join(t.TempDir(), "receipts.wal")
	setDurableRuntimeEnv(t, "fresh", path, port)

	app, cleanup, err := initializeApp()
	if app != nil || cleanup != nil || err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("initializeApp with occupied listener = %p, %v, %v", app, cleanup != nil, err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("listener construction failure leaked durable lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}

	envconfig.ResetForTesting()
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "restart")
	app, cleanup, err = initializeApp()
	if err != nil {
		t.Fatalf("restart after partial owner cleanup: %v", err)
	}
	if app == nil || cleanup == nil || !app.runtime.DurableReceiptWAL() {
		t.Fatalf("restart app = %p, cleanup %v", app, cleanup != nil)
	}
	cleanup()
	lease, err = mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("injector cleanup leaked durable lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInitializeAppRejectsUncertifiedRestartBeforeListener(t *testing.T) {
	probe, port := reserveRuntimeTestPort(t)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "missing.wal")
	setDurableRuntimeEnv(t, "restart", path, port)

	app, cleanup, err := initializeApp()
	if app != nil || cleanup != nil || !errors.Is(err, os.ErrNotExist) {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("uncertified restart = %p, %v, %v", app, cleanup != nil, err)
	}
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("uncertified restart reached listener construction: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInitializeAppRejectsImplicitDurableNodeIDBeforeFilesOrListener(t *testing.T) {
	probe, port := reserveRuntimeTestPort(t)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipts.wal")
	setDurableRuntimeEnv(t, "fresh", path, port)
	t.Setenv("LANTERN_NODE_ID", "")
	envconfig.ResetForTesting()

	app, cleanup, err := initializeApp()
	if app != nil || cleanup != nil || err == nil || !strings.Contains(err.Error(), "LANTERN_NODE_ID") {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("implicit durable node ID = %p, %v, %v", app, cleanup != nil, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("implicit durable node ID created WAL bytes: %v", err)
	}
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("implicit durable node ID reached listener construction: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInitializeAppGraphOnlyDefault(t *testing.T) {
	probe, port := reserveRuntimeTestPort(t)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	envconfig.ResetForTesting()
	t.Cleanup(envconfig.ResetForTesting)
	t.Setenv("LANTERN_PORT", strconv.Itoa(port))
	t.Setenv("LANTERN_NODE_ID", "31313131313131313131313131313131")
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "graph-only")
	t.Setenv("LANTERN_RECEIPT_WAL_PATH", "")
	t.Setenv("LANTERN_RECEIPT_EPOCH", "")
	t.Setenv("LANTERN_RECEIPT_RETENTION", "0s")
	t.Setenv("LANTERN_RECEIPT_MAX_ENTRIES", "0")
	t.Setenv("LANTERN_RECEIPT_MAX_BYTES", "0")
	t.Setenv("LANTERN_BACKUP_ENABLED", "false")
	t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")

	app, cleanup, err := initializeApp()
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || app == nil || app.runtime.DurableReceiptWAL() ||
		app.cfg.ReceiptWAL.Mode != "graph-only" {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("graph-only app = %p, cleanup %v, mode %q", app, cleanup != nil, app.cfg.ReceiptWAL.Mode)
	}
	graphOnlyReceipt := productionReceiptEdgeDeleteMutation(t, mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20,
	}, hlc.NodeID{0x72}, 1, "graph-only-tail", "graph-only-head")
	if err := app.svc.ApplyMutation(context.Background(), graphOnlyReceipt); connect.CodeOf(err) != connect.CodeUnimplemented {
		cleanup()
		t.Fatalf("graph-only receipt follower apply = %v, want Unimplemented", err)
	}
	if got := app.svc.LocalSeq(hlc.NodeID{0x72}); got != 0 {
		cleanup()
		t.Fatalf("graph-only receipt rejection advanced origin to %d", got)
	}
	if length, _, _ := app.runtime.MutationLogStats(); length != 0 {
		cleanup()
		t.Fatalf("graph-only receipt rejection changed mutation Log length to %d", length)
	}
	cleanup()
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("graph-only injector cleanup leaked listener: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

// Exercise the generated legacy production injector, its real restart fence
// and public TLS listener. Current-v2 has independent provisioning/native-time
// gates; this fixed-writer fixture must select its legacy profile explicitly.
func TestInitializeAppOIDCLegacyProductionSurface(t *testing.T) {
	probe, port := reserveRuntimeTestPort(t)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	setDurableRuntimeEnv(t, "fresh", filepath.Join(dir, "receipts.wal"), port)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	tlsSource := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsSource.Close()
	tlsKey, err := x509.MarshalPKCS8PrivateKey(tlsSource.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, block := range map[string]*pem.Block{
		"writer.key": {Type: "PRIVATE KEY", Bytes: privateDER}, "writer.pub": {Type: "PUBLIC KEY", Bytes: publicDER},
		"tls.key": {Type: "PRIVATE KEY", Bytes: tlsKey}, "tls.pem": {Type: "CERTIFICATE", Bytes: tlsSource.Certificate().Raw},
	} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	token, err := security.NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	raw, err := json.Marshal([]map[string]any{{"name": "worker", "role_ids": []string{"worker"}, "credentials": []map[string]any{{"token": token, "created_at": now, "expires_at": now.Add(time.Hour)}}}})
	if err != nil {
		t.Fatal(err)
	}
	machines := filepath.Join(dir, "machines.json")
	if err := os.WriteFile(machines, raw, 0600); err != nil {
		t.Fatal(err)
	}
	origin := fmt.Sprintf("https://127.0.0.1:%d", port)
	for name, value := range map[string]string{
		"LANTERN_SECURITY_PROFILE": "legacy-v1",
		"LANTERN_AUTH_MODE":        "oidc", "LANTERN_OIDC_ADMIN_ISSUER": "https://issuer.example", "LANTERN_OIDC_ADMIN_SUBJECTS": `["administrator"]`,
		"LANTERN_OIDC_CLIENT_ID": "admin", "LANTERN_OIDC_API_AUDIENCE": "api", "LANTERN_OIDC_BROWSER_ORIGIN": origin,
		"LANTERN_OIDC_REDIRECT_URI":   origin + oidc.CallbackPath("https://issuer.example"),
		"LANTERN_SECURITY_STORE_MODE": "fresh", "LANTERN_SECURITY_STORE_PATH": filepath.Join(dir, "security.wal"),
		"LANTERN_SECURITY_GENERATION": "01000000000000000000000000000000", "LANTERN_SECURITY_NODE_ROLE": "writer",
		"LANTERN_SECURITY_WRITER_KEY_FILE": filepath.Join(dir, "writer.key"), "LANTERN_SECURITY_WRITER_PUBLIC_KEY_FILE": filepath.Join(dir, "writer.pub"),
		"LANTERN_SECURITY_WRITER_ENDPOINT": "https://peer.example", "LANTERN_SECURITY_BOOTSTRAP_REVISION": "1", "LANTERN_SECURITY_CLOCK_QUALIFIED": "true",
		"LANTERN_SECURITY_BOOTSTRAP_ROLES":        `[{"id":"worker","name":"Worker","rules":[{"id":"read","effect":"allow","action":"vertex.read","resource":"data","prefix":"tenant:"},{"id":"receipts","effect":"allow","action":"receipt.read","resource":"data","prefix":"tenant:"},{"id":"write","effect":"allow","action":"vertex.write","resource":"data","prefix":"tenant:"}]}]`,
		"LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE": machines, "LANTERN_CDC_ENABLED": "false",
		"LANTERN_TLS_CERT_FILE": filepath.Join(dir, "tls.pem"), "LANTERN_TLS_KEY_FILE": filepath.Join(dir, "tls.key"),
		"LANTERN_LOG_LEVEL": "error", "LANTERN_METRICS_ADDR": "", "LANTERN_DRAIN_DELAY_MS": "0",
	} {
		t.Setenv(name, value)
	}
	t.Setenv("LANTERN_METRICS_ADDR", "")
	envconfig.ResetForTesting()
	app, cleanup, err := initializeApp()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("production App did not join")
		}
	}()
	control := graphv1connect.NewLanternSecurityServiceClient(tlsSource.Client(), origin)
	data := graphv1connect.NewLanternServiceClient(tlsSource.Client(), origin)
	request := func(key string) *connect.Request[pb.GetVertexRequest] {
		req := connect.NewRequest(&pb.GetVertexRequest{Key: key})
		req.Header().Set("Authorization", "Bearer "+token)
		return req
	}
	deadline := time.Now().Add(40 * time.Second)
	// A capability response and a subsequent data RPC may straddle the real
	// restart-fence expiry. Fixed-clock public TLS tests assert data denial at
	// startup and the final fenced nanosecond, as well as that boundary crossing.
	for time.Now().Before(deadline) {
		caps, err := control.GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
		if err == nil {
			if caps.Msg.Mode != pb.AuthMode_AUTH_MODE_OIDC {
				t.Fatal("production mode downgraded")
			}
			if caps.Msg.Ready {
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	caps, err := control.GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
	if err != nil || caps.Msg.Mode != pb.AuthMode_AUTH_MODE_OIDC || !caps.Msg.Ready {
		t.Fatal("production authority readiness", err)
	}
	put := connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "tenant:one", Value: &pb.Vertex_String_{String_: "live"}, Expiration: timestamppb.New(time.Now().Add(time.Minute))}})
	put.Header().Set("Authorization", "Bearer "+token)
	if _, err := data.PutVertex(ctx, put); err != nil {
		t.Fatal("Role-scoped production Put", err)
	}
	got, err := data.GetVertex(ctx, request("tenant:one"))
	if err != nil || got.Msg.Vertex.Key != "tenant:one" || got.Msg.Vertex.GetString_() != "live" {
		t.Fatal("logical production read", err)
	}
	if _, err := data.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "tenant:one"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous protected read", err)
	}
	if _, err := data.GetVertex(ctx, request("private:one")); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("prefix denial", err)
	}
	capability := connect.NewRequest(&pb.GetReceiptCapabilityRequest{})
	capability.Header().Set("Authorization", "Bearer "+token)
	cap, err := data.GetReceiptCapability(ctx, capability)
	if err != nil || !cap.Msg.GetEnabled() {
		t.Fatal("production native receipt activation", err)
	}
	peer := graphv1connect.NewLanternReplicationServiceClient(tlsSource.Client(), origin)
	peerReq := connect.NewRequest(&pb.PeerStatusRequest{})
	peerReq.Header().Set("Authorization", "Bearer "+token)
	if _, err := peer.PeerStatus(ctx, peerReq); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal("public surface exposed private replication", err)
	}
}
