package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

func TestManagedSlowRPCRuntimeActualStateAndBootstrapSources(t *testing.T) {
	for _, test := range []struct {
		name      string
		input     []string
		source    securitytransport.OperationalSettingsSource
		threshold time.Duration
	}{
		{"default", nil, securitytransport.OperationalSourceDefault, 500 * time.Millisecond},
		{"explicit-disabled", []string{"LANTERN_SLOW_RPC_THRESHOLD_MS=0"}, securitytransport.OperationalSourceEnvironment, 0},
		{"empty", []string{"LANTERN_SLOW_RPC_THRESHOLD_MS="}, securitytransport.OperationalSourceDefault, 500 * time.Millisecond},
		{"malformed", []string{"LANTERN_SLOW_RPC_THRESHOLD_MS=bad"}, securitytransport.OperationalSourceDefault, 500 * time.Millisecond},
		{"derived-disable", []string{"LANTERN_SLOW_RPC_THRESHOLD_MS=-1"}, securitytransport.OperationalSourceDerived, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadConfig(envconfig.NewReader(test.input))
			if err != nil {
				t.Fatal(err)
			}
			runtime := &SecurityRuntime{mode: "off"}
			manager, err := NewManagedOperationalRuntime(t.Context(), runtime, NewOperationalBootstrapConfig(cfg))
			if err != nil {
				t.Fatal(err)
			}
			owner := NewSlowRPCInterceptorProvider(cfg.Observability, slog.New(slog.NewTextHandler(io.Discard, nil)))
			cert, err := NewManagedSlowRPCRuntime(t.Context(), manager, owner, cfg.Observability)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || actual.Effective == nil || actual.EffectiveGeneration != 0 || actual.Effective.SlowRPC.ThresholdMS != test.threshold.Milliseconds() || actual.Sources[securitytransport.OperationalSetting(1)] != test.source {
				t.Fatal("bootstrap value/source invented", actual, err)
			}
			if err := owner.SetThresholdMilliseconds(123); err != nil {
				t.Fatal(err)
			}
			changed, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || changed.Effective == nil || changed.EffectiveGeneration != 0 || changed.Effective.SlowRPC.ThresholdMS != 123 || changed.Sources[securitytransport.OperationalSetting(1)] != securitytransport.OperationalSourceDerived {
				t.Fatal("bootstrap setter retained the original source", changed, err)
			}
			if err := owner.SetThresholdMilliseconds(test.threshold.Milliseconds()); err != nil {
				t.Fatal(err)
			}
			restored, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || restored.Effective == nil || restored.EffectiveGeneration != 0 || restored.Effective.SlowRPC.ThresholdMS != test.threshold.Milliseconds() || restored.Sources[securitytransport.OperationalSetting(1)] != test.source {
				t.Fatal("returning to bootstrap value changed its source", restored, err)
			}
			policy := operationalpolicy.Policy{Version: operationalpolicy.Version, Generation: 1, Values: operationalpolicy.Values{SlowRPC: &operationalpolicy.SlowRPC{ThresholdMS: 12}}}
			if err := cert.adapter.ApplyOperationalPolicy(policy); err != nil {
				t.Fatal(err)
			}
			applied, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || applied.EffectiveGeneration != 1 || applied.Effective.SlowRPC.ThresholdMS != 12 || applied.Sources[securitytransport.OperationalSetting(1)] != securitytransport.OperationalSourceManaged {
				t.Fatal("managed application was not actual", applied, err)
			}
			applied.Effective.SlowRPC.ThresholdMS = 900
			applied.Sources[securitytransport.OperationalSetting(1)] = securitytransport.OperationalSourceDefault
			fresh, _ := cert.adapter.ObserveOperationalPolicy()
			if fresh.Effective.SlowRPC.ThresholdMS != 12 || fresh.Sources[securitytransport.OperationalSetting(1)] != securitytransport.OperationalSourceManaged {
				t.Fatal("actual snapshot aliases caller")
			}
			if err := owner.SetThresholdMilliseconds(999); err == nil || owner.Threshold() != 12*time.Millisecond {
				t.Fatal("bootstrap setter overrode adoption")
			}
		})
	}
}

type operationalSlowWireLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *operationalSlowWireLog) Write(raw []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(raw)
}
func (w *operationalSlowWireLog) records(t *testing.T) []map[string]any {
	w.mu.Lock()
	raw := append([]byte(nil), w.buffer.Bytes()...)
	w.mu.Unlock()
	return decodeRecords(t, bytes.NewBuffer(raw))
}

func TestManagedSlowRPCRealConnectResidentEnableDisableAndRefusal(t *testing.T) {
	// Real HTTP/Connect exercises the resident production interceptor and
	// actual owner. Native history/authentication use separate isolated
	// authority tests; this local fixture changes only verified projections.
	cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_SLOW_RPC_THRESHOLD_MS=0"}))
	if err != nil {
		t.Fatal(err)
	}
	p := operationalRuntimeTestProjection(1)
	manager, authority := operationalRuntimeTestManager(t, p)
	manager.bootstrap = NewOperationalBootstrapConfig(cfg)
	logs := &operationalSlowWireLog{}
	owner := NewSlowRPCInterceptor(0, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	var ticks atomic.Int64
	owner.now = func() time.Time { return time.Unix(0, ticks.Add(int64(2*time.Millisecond))) }
	if _, err := NewManagedSlowRPCRuntime(t.Context(), manager, owner, cfg.Observability); err != nil {
		t.Fatal(err)
	}
	const path = "/graph.v1.LanternService/GetVertex"
	handler := connect.NewUnaryHandler(path, func(_ context.Context, request *connect.Request[pb.GetVertexRequest]) (*connect.Response[pb.GetVertexResponse], error) {
		if request.Msg.Key == "denied" {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("fixture denied"))
		}
		return connect.NewResponse(&pb.GetVertexResponse{Vertex: &pb.Vertex{Key: request.Msg.Key}}), nil
	}, connect.WithInterceptors(owner.ConnectInterceptor()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := connect.NewClient[pb.GetVertexRequest, pb.GetVertexResponse](server.Client(), server.URL+path)
	call := func(key string) error {
		_, err := client.CallUnary(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: key}))
		return err
	}
	if err := call("disabled"); err != nil || len(logs.records(t)) != 0 || ticks.Load() != 0 {
		t.Fatal("disabled resident owner measured or logged", err)
	}
	enabled := operationalRuntimeTestManaged(t, 2, 1)
	enabled.Policies.SlowRPC.Values.SlowRPC.ThresholdMS = 1
	authority.setProjection(enabled)
	pending, err := manager.Observe(enabled)
	if err != nil || !pending[operationalpolicy.GroupSlowRPC].Pending || pending[operationalpolicy.GroupSlowRPC].AppliedGeneration != 0 {
		t.Fatal("commit alone claimed application", err)
	}
	if err := manager.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := call("enabled"); err != nil {
		t.Fatal(err)
	}
	if err := call("denied"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("middleware changed RPC refusal", err)
	}
	records := logs.records(t)
	if len(records) != 2 || records[0]["threshold_ms"] != float64(1) || records[0]["policy_generation"] != float64(1) || records[1]["code"] != "permission_denied" {
		t.Fatal("actual captured tuple/refusal telemetry changed", records)
	}
	disabled := operationalRuntimeTestManaged(t, 3, 2)
	disabled.Policies.SlowRPC.Values.SlowRPC.ThresholdMS = 0
	authority.setProjection(disabled)
	if err := manager.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := ticks.Load()
	if err := call("disabled-again"); err != nil || len(logs.records(t)) != 2 || ticks.Load() != before {
		t.Fatal("mounted owner did not disable without restart", err)
	}
	if owner.Snapshot().Generation != 2 {
		t.Fatal("actual effective generation was not installed")
	}
}

func TestManagedSlowRPCVerifiedRecoveryOverridesEnvironmentAndRetainsOnFailure(t *testing.T) {
	cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_SLOW_RPC_THRESHOLD_MS=999"}))
	if err != nil {
		t.Fatal(err)
	}
	p := operationalRuntimeTestManaged(t, 1, 3)
	manager, authority := operationalRuntimeTestManager(t, p)
	manager.bootstrap = NewOperationalBootstrapConfig(cfg)
	owner := NewSlowRPCInterceptorProvider(cfg.Observability, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := NewManagedSlowRPCRuntime(t.Context(), manager, owner, cfg.Observability); err != nil {
		t.Fatal(err)
	}
	if actual := owner.Snapshot(); actual.Generation != 3 || actual.Threshold != 30*time.Millisecond {
		t.Fatal("verified recovery fell back to environment", actual)
	}
	observed, err := manager.Observe(p)
	if err != nil || observed[operationalpolicy.GroupSlowRPC].AppliedGeneration != 3 || observed[operationalpolicy.GroupSlowRPC].AppliedCut != p.Cut {
		t.Fatal("restored actual state lacks verified application evidence", err)
	}
	next := operationalRuntimeTestManaged(t, 2, 4)
	authority.setProjection(next)
	if err := manager.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if owner.Snapshot().Generation != 4 || owner.Threshold() != 40*time.Millisecond {
		t.Fatal("committed successor did not install")
	}
	authority.mu.Lock()
	authority.readErr = errors.New("fixture authority unavailable")
	authority.mu.Unlock()
	if err := manager.Reconcile(t.Context()); err == nil || owner.Snapshot().Generation != 4 || owner.Threshold() != 40*time.Millisecond {
		t.Fatal("authority failure replaced actual policy with environment")
	}
	restarted, _ := operationalRuntimeTestManager(t, next)
	restarted.bootstrap = NewOperationalBootstrapConfig(cfg)
	restartedOwner := NewSlowRPCInterceptorProvider(cfg.Observability, owner.logger)
	if _, err := NewManagedSlowRPCRuntime(t.Context(), restarted, restartedOwner, cfg.Observability); err != nil || restartedOwner.Snapshot().Generation != 4 || restartedOwner.Threshold() != 40*time.Millisecond {
		t.Fatal("restart did not install sys-owned policy before binding", err)
	}
	rollback := operationalRuntimeTestManaged(t, 3, 5)
	rollback.Policies.SlowRPC.Values.SlowRPC.ThresholdMS = 30
	authority.mu.Lock()
	authority.readErr = nil
	authority.mu.Unlock()
	authority.setProjection(rollback)
	if err := manager.Reconcile(t.Context()); err != nil || owner.Snapshot().Generation != 5 || owner.Threshold() != 30*time.Millisecond {
		t.Fatal("rollback did not become a new generation", err)
	}
}

func TestManagedSlowRPCRuntimePreparedValuesDoNotApply(t *testing.T) {
	owner := NewSlowRPCInterceptor(500*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter := &managedSlowRPCRuntime{owner: owner}
	change := operationalpolicy.Change{Values: operationalpolicy.Values{SlowRPC: &operationalpolicy.SlowRPC{ThresholdMS: 0}}}
	if err := adapter.PrepareOperationalPolicy(change); err != nil || owner.Snapshot().Generation != 0 || owner.Threshold() != 500*time.Millisecond {
		t.Fatal("preparation applied uncommitted values", err)
	}
	change.Values.SlowRPC.ThresholdMS = -1
	if err := adapter.PrepareOperationalPolicy(change); err == nil {
		t.Fatal("negative threshold prepared")
	}
	if err := adapter.AvailableOperationalPolicy(operationalpolicy.GroupRate, operationalpolicy.Version); err == nil {
		t.Fatal("Slow owner advertised another group")
	}
	if err := adapter.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, 2); err == nil {
		t.Fatal("unsupported application version prepared")
	}
	if err := (&managedSlowRPCRuntime{}).AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, 1); err == nil {
		t.Fatal("missing actual logger advertised support")
	}
}

func TestManagedSlowRPCRuntimeRequiresCapturedSourcesBeforeBinding(t *testing.T) {
	manager, authority := operationalRuntimeTestManager(t, operationalRuntimeTestManaged(t, 1, 1))
	owner := NewSlowRPCInterceptor(999*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	before := owner.Snapshot()
	if _, err := NewManagedSlowRPCRuntime(t.Context(), manager, owner, ObservabilityConfig{SlowRPCThreshold: 999 * time.Millisecond}); err == nil {
		t.Fatal("uncaptured provenance was invented")
	}
	if owner.Snapshot() != before || authority.binds != 0 || manager.slowRPC != nil {
		t.Fatal("missing provenance refusal applied or bound an owner")
	}
}
