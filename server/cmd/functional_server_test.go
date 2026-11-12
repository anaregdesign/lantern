//go:build lantern_test_time

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
)

// A private serving executable made with go test -c. It runs the real App,
// listener adoption, certification and cleanup; only UTC composition is the
// compile-time controlled fixture. Normal binaries cannot select this entry or
// mint fixture time. go test itself supplies -test.* arguments and runs tests.
func TestMain(m *testing.M) {
	if len(os.Args) == 1 || os.Args[1] == listenerlaunch.Argument || os.Args[1] == "init-security" || os.Args[1] == "provision-security" || os.Args[1] == "renew-security" || os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "help" {
		if err := runFunctionalMain(len(os.Args) == 1, runMain, os.Stderr); err != nil {
			decisiontrace.WriteStartupFailure(os.Stderr, err)
			slog.Error("functional server exited with error", slog.Any("err", err))
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Only the controlled serving child owns this bounded passive capture. Normal
// binaries do not compile this file, and m.Run retains its existing captures.
// Finalize after runMain returns, before TestMain calls os.Exit. Overflow remains
// explicit diagnostic loss; neither capture nor its records authorize a request.
func runFunctionalMain(serving bool, run func() error, output io.Writer) error {
	if !serving {
		return run()
	}
	capture := decisiontrace.Start(4096)
	if capture == nil {
		return errors.New("controlled public decision capture already owned")
	}
	err := run()
	capture.Stop()
	events, dropped := capture.Drain()
	traceErr := json.NewEncoder(output).Encode(struct {
		Kind    string                `json:"kind"`
		Events  []decisiontrace.Event `json:"events"`
		Dropped uint64                `json:"dropped"`
	}{"controlled_public_trace", events, dropped})
	return errors.Join(err, traceErr)
}

func TestFunctionalMainCaptureRetainsTerminalLossAndOwnership(t *testing.T) {
	primary := errors.New("synthetic primary failure")
	var output strings.Builder
	err := runFunctionalMain(true, func() error {
		for range 4097 {
			decisiontrace.Emit(decisiontrace.Event{Kind: "public_refusal", Stage: "admit", Reason: "cut", Turn: 7})
		}
		return primary
	}, &output)
	if !errors.Is(err, primary) || decisiontrace.Enabled() {
		t.Fatal("capture masked primary failure or retained ownership", err)
	}
	var record struct {
		Kind    string
		Events  []decisiontrace.Event
		Dropped uint64
	}
	if err := json.Unmarshal([]byte(output.String()), &record); err != nil || record.Kind != "controlled_public_trace" || len(record.Events) != 4096 || record.Dropped != 1 {
		t.Fatal("bounded terminal capture lost explicit overflow", err)
	}
	existing := decisiontrace.Start(1)
	if existing == nil {
		t.Fatal("capture was not released")
	}
	defer existing.Stop()
	called := false
	if err := runFunctionalMain(true, func() error { called = true; return nil }, io.Discard); err == nil || called || !decisiontrace.Enabled() {
		t.Fatal("serving capture stole existing ownership", err, called)
	}
	if err := runFunctionalMain(false, func() error { called = true; return primary }, io.Discard); !errors.Is(err, primary) || !called {
		t.Fatal("non-serving command changed its error or capture", err)
	}
}

// Controlled UDP supplies synthetic UTC for this real App lifetime test only.
// It cannot qualify the separate normal-build configured-source collector.
func TestOFFQualifiedAppFinalCounterLifecycle(t *testing.T) {
	if !security.NativeWorkloadTimeTarget() {
		t.Skip("controlled owned OFF time requires an eligible native target")
	}
	restore, err := neutralizeInheritedProfileControls()
	if err != nil {
		t.Fatal("functional inherited profile controls unavailable")
	}
	t.Cleanup(restore)
	launch, err := listenerlaunch.Reserve(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer launch.Close()
	launches := []*listenerlaunch.Owner{launch}
	public, peer := launch.Addresses()
	_, publicPort, _ := net.SplitHostPort(public)
	_, peerPort, _ := net.SplitHostPort(peer)
	publicPorts, peerPorts := []string{publicPort}, []string{peerPort}
	// The manifest has three members so the real private service has its
	// replication state provider. One App serves; a second supplies the client.
	for range 2 {
		reserved, err := listenerlaunch.Reserve(t.Context(), true)
		if err != nil {
			t.Fatal(err)
		}
		defer reserved.Close()
		launches = append(launches, reserved)
		public, peer := reserved.Addresses()
		_, publicPort, _ := net.SplitHostPort(public)
		_, peerPort, _ := net.SplitHostPort(peer)
		publicPorts, peerPorts = append(publicPorts, publicPort), append(peerPorts, peerPort)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "authfixture")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./server/cmd/authfixture")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v\n%s", err, output)
	}
	material := filepath.Join(dir, "material")
	command = exec.CommandContext(t.Context(), binary, "-directory", material, "-mode", "off", "-public-ports", strings.Join(publicPorts, ","), "-peer-ports", strings.Join(peerPorts, ","))
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("ephemeral OFF provisioning: %v", err)
	}
	var fixture struct {
		CAFile string `json:"ca_file"`
		Nodes  []struct {
			Public      string            `json:"public_origin"`
			Peer        string            `json:"peer_origin"`
			Environment map[string]string `json:"environment"`
		} `json:"nodes"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Nodes) != 3 {
		t.Fatal("invalid ephemeral OFF fixture", err)
	}
	for key, value := range fixture.Nodes[0].Environment {
		t.Setenv(key, value)
	}
	t.Setenv("LANTERN_AUTH_MODE", "off")
	t.Setenv("LANTERN_DRAIN_DELAY_SECONDS", "0")
	t.Setenv("LANTERN_BACKUP_ENABLED", "false")
	t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")
	app, cleanup, err := initializeAppWithListeners(t.Context(), launch)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	security := app.security.(*provider.SecurityRuntime)
	identity := app.peerIdentity.(*provider.PeerIdentityRuntime)
	before := security.TimeAcquisitionCounts()
	if before.Attempts == 0 || before.Writes == 0 || before.Installations == 0 || launch.Adopted() != nil {
		t.Fatal("production OFF App lacks owned controlled evidence", before)
	}
	// The client has a distinct real member identity. Self-directed private
	// calls are intentionally refused by the production workload transport.
	for key, value := range fixture.Nodes[1].Environment {
		t.Setenv(key, value)
	}
	probeApp, probeCleanup, err := initializeAppWithListeners(t.Context(), launches[1])
	if err != nil {
		t.Fatal("controlled peer client composition", err)
	}
	defer probeCleanup()
	probeIdentity := probeApp.peerIdentity.(*provider.PeerIdentityRuntime)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("controlled App cleanup did not join")
			}
		}
	}()
	ca, err := os.ReadFile(fixture.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixture.Nodes[0].Public+"/grpc.health.v1.Health/Check", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Connect-Protocol-Version", "1")
		response, err := client.Do(request)
		if err == nil {
			var health struct {
				Status string `json:"status"`
			}
			ready = response.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&health) == nil && health.Status == "SERVING_STATUS_SERVING"
			_ = response.Body.Close()
		}
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("controlled OFF App never served its verified public listener")
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer probeCancel()
	if err := offSourcePeerProbe(probeCtx, probeIdentity, fixture.Nodes[0].Peer); err != nil {
		t.Fatal("OFF source positive PeerStatus/client guard probe failed on the real private service", err)
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal("controlled OFF App shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controlled OFF App did not join")
	}
	cleanup()
	after := security.TimeAcquisitionCounts()
	if after != before || after.InFlight || after.Failed || after.Denied || security.TimeAcquisitionCounts() != after {
		t.Fatal("post-App/Wire counters missing or unstable", before, after)
	}
	if identity.CheckWorkload(context.Background()) == nil {
		t.Fatal("post-close diagnostics revived workload authority")
	}
}
