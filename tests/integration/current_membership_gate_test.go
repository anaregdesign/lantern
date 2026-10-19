package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
)

// Invoked by the existing production native/public-wire setup with a short
// initial signed interval. The exported keys belong only to this synthetic
// cohort; the operator CLI runs separately and no server receives its signer.
func runCurrentMembershipGate(t *testing.T, f *oidcControlWireFixture, dir string, servers []*httptest.Server, runtimes []*provider.SecurityRuntime, workerStops []func(), restart func(int) (*httptest.Server, *provider.SecurityRuntime, func())) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 170*time.Second)
	defer cancel()
	wait := func(until time.Time) {
		t.Helper()
		timer := time.NewTimer(time.Until(until))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	waitReady := func() {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			all := true
			for _, r := range runtimes {
				all = all && r.Ready(ctx)
			}
			if all {
				return
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				t.Fatal("current cohort did not regain native readiness")
			}
			wait(time.Now().Add(100 * time.Millisecond))
		}
	}
	manifest := func(path string) (uint64, time.Time, [32]byte) {
		t.Helper()
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		var envelope struct {
			Payload struct {
				Version   uint64
				ExpiresAt time.Time `json:"expires_at"`
			}
		}
		if json.Unmarshal(raw, &envelope) != nil {
			t.Fatal("manifest envelope")
		}
		return envelope.Payload.Version, envelope.Payload.ExpiresAt, sha256.Sum256(raw)
	}
	// Declare the finite operator/runtime budget after native startup and before
	// creating any publisher state. The configuration is immutable thereafter.
	configPath := filepath.Join(dir, "publisher.json")
	rawConfig, e := os.ReadFile(configPath)
	if e != nil {
		t.Fatal(e)
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(rawConfig, &config) != nil {
		t.Fatal("operator configuration")
	}
	config["End"], _ = json.Marshal(time.Now().UTC().Add(3 * time.Minute))
	config["CleanupBy"], _ = json.Marshal(time.Now().UTC().Add(4 * time.Minute))
	rawConfig, e = json.Marshal(config)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(configPath, rawConfig, 0600); e != nil {
		t.Fatal(e)
	}
	startPublisher := func(mode string) func() {
		t.Helper()
		binary := os.Getenv("LANTERN_MEMBERSHIP_PUBLISHER")
		if !filepath.IsAbs(binary) {
			t.Fatal("build the exact publisher binary before this gate")
		}
		cmd := exec.Command(binary, "-config", filepath.Join(dir, "publisher.json"), "-state", filepath.Join(dir, "publisher-state.json"), "-mode", mode)
		// The CLI emits only fixed categories; do not include its private config.
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				select {
				case e := <-done:
					t.Errorf("publisher exited before bounded stop: %v", e)
					return
				default:
				}
				if e := cmd.Process.Signal(os.Interrupt); e != nil {
					t.Error(e)
				}
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("publisher did not join")
				}
			})
		}
		t.Cleanup(stop)
		return stop
	}
	stopPublisher := startPublisher("init")
	var initialVersion uint64
	var initialEnd time.Time
	deadline := time.Now().Add(20 * time.Second)
	for {
		initialVersion, initialEnd, _ = manifest(filepath.Join(dir, "node-1", "membership.signed"))
		if initialVersion >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publisher did not install the first short interval")
		}
		wait(time.Now().Add(100 * time.Millisecond))
	}
	waitReady()
	f.server = servers[0]
	token := f.token(t, "admin", func(c map[string]any) { c["iat"] = time.Now().Add(-time.Minute).Unix(); delete(c, "auth_time") })
	transport := &http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}
	control := graphv1connect.NewLanternSecurityServiceClient(transport, servers[0].URL)
	data := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
	checkData := func() {
		t.Helper()
		waitReady()
		if _, e := control.ListRoles(ctx, securityWireRequest(token, &pb.ListRolesRequest{})); e != nil {
			t.Fatal("Role read across renewal", e)
		}
		if _, e := data.PutVertex(ctx, securityWireRequest(token, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:renewal", Value: &pb.Vertex_String_{String_: "same cohort"}}})); e != nil {
			t.Fatal("allowed write across renewal", e)
		}
		got, e := data.GetVertex(ctx, securityWireRequest(token, &pb.GetVertexRequest{Key: "orders:renewal"}))
		if e != nil || got.Msg.GetVertex().GetString_() != "same cohort" {
			t.Fatal("allowed read across renewal", e)
		}
	}
	checkData()
	start := f.browserLoginStart(t, nil)
	callback := f.browserCallbackPath(t, start, "admin", false)
	wait(time.Now().Add(500 * time.Millisecond))
	response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, callback, start.Cookies(), ""))
	if response.StatusCode != http.StatusSeeOther {
		t.Fatal("synthetic login", response.StatusCode)
	}
	var cookies []*http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Value != "" {
			cookies = append(cookies, cookie)
		}
	}
	_ = response.Body.Close()
	if len(cookies) != 2 {
		t.Fatal("synthetic session cookies")
	}
	checkSession := func() {
		t.Helper()
		waitReady()
		response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, ""))
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK || len(response.Cookies()) != 0 {
			t.Fatal("same session lost or rewritten by renewal", response.StatusCode)
		}
	}
	checkSession()
	// Begin a second Code attempt immediately before the original membership
	// ends and consume it afterwards, within the original transaction deadline.
	wait(initialEnd.Add(-time.Second))
	waitReady()
	pending := f.browserLoginStart(t, nil)
	pendingCallback := f.browserCallbackPath(t, pending, "admin", false)
	wait(initialEnd.Add(time.Second))
	checkSession()
	checkData()
	completion := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, pendingCallback, pending.Cookies(), ""))
	if completion.StatusCode != http.StatusSeeOther {
		t.Fatal("Code attempt did not cross initial membership deadline", completion.StatusCode)
	}
	_ = completion.Body.Close()
	for _, cookie := range cookies {
		if !time.Now().Before(cookie.Expires) {
			t.Fatal("fixture exceeded original session lifetime")
		}
	}
	for i, r := range runtimes {
		node := filepath.Join(dir, "node-"+string(rune('1'+i)))
		v, _, digest := manifest(filepath.Join(node, "membership.signed"))
		dv, _, _ := manifest(filepath.Join(node, "data-input", "data.signed"))
		if v < initialVersion+2 || dv < initialVersion+2 {
			t.Fatal("both streams must have at least two successors", v, dv)
		}
		raw, e := r.ExportCurrentAuthorityFloors()
		if e != nil {
			t.Fatal(e)
		}
		var floor struct {
			M struct {
				Version uint64
				Digest  [32]byte
			}
		}
		if json.Unmarshal(raw, &floor) != nil || floor.M.Version < initialVersion+2 {
			t.Fatal("worker did not retain repeated control updates")
		}
		if floor.M.Version == v && floor.M.Digest != digest {
			t.Fatal("wrong retained exact bytes")
		}
	}
	t.Log("native: both streams renewed repeatedly; same session, Role read, Code callback and data read/write crossed the initial deadline")
	waitReady()
	reader := f.token(t, "reader", func(c map[string]any) { c["iat"] = time.Now().Add(-time.Minute).Unix(); delete(c, "auth_time") })
	if _, e := data.PutVertex(ctx, securityWireRequest(reader, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:denied"}})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("renewal widened scope", e)
	}
	expired := f.token(t, "admin", func(c map[string]any) {
		c["iat"] = time.Now().Add(-time.Hour).Unix()
		c["exp"] = time.Now().Add(-time.Minute).Unix()
		delete(c, "auth_time")
	})
	if _, e := control.ListRoles(ctx, securityWireRequest(expired, &pb.ListRolesRequest{})); e == nil {
		t.Fatal("renewal revived expired credential")
	}
	// Delay only node 1's data publication. Its checkpoint journals remain
	// writable; the running publisher retains one candidate while independently
	// distributing control and the other data targets. Consumers still run.
	dataInput := filepath.Join(dir, "node-1", "data-input")
	_, heldEnd, _ := manifest(filepath.Join(dataInput, "data.signed"))
	if e := os.Chmod(dataInput, 0500); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(dataInput, 0700) })
	wait(heldEnd.Add(time.Second))
	if runtimes[0].Ready(ctx) {
		t.Fatal("one-sided data expiry did not close readiness")
	}
	if !runtimes[1].Ready(ctx) {
		t.Fatal("one delayed data target blocked the other owners")
	}
	if _, e := data.GetVertex(ctx, securityWireRequest(token, &pb.GetVertexRequest{Key: "orders:renewal"})); e == nil {
		t.Fatal("one-sided expiry admitted data")
	}
	if e := os.Chmod(dataInput, 0700); e != nil {
		t.Fatal(e)
	}
	waitReady()
	checkSession()
	checkData()
	t.Log("native: one data target lagged through expiry while control/other targets advanced; exact pending delivery recovered it")
	stopPublisher()
	_, controlEnd, _ := manifest(filepath.Join(dir, "node-1", "membership.signed"))
	_, dataEnd, _ := manifest(filepath.Join(dir, "node-1", "data-input", "data.signed"))
	if dataEnd.After(controlEnd) {
		controlEnd = dataEnd
	}
	wait(controlEnd.Add(time.Second))
	if runtimes[0].Ready(ctx) {
		t.Fatal("stopped publisher silently extended membership")
	}
	if _, e := data.GetVertex(ctx, securityWireRequest(token, &pb.GetVertexRequest{Key: "orders:renewal"})); e == nil {
		t.Fatal("expired membership authorized data")
	}
	stopPublisher = startPublisher("resume")
	waitReady()
	checkSession()
	checkData()
	t.Log("native: expiry refused readiness/data; intact publisher resume recovered surviving owners without extending session")
	// Stop/join the actual worker, capture latest M, and check exact CLEAN
	// floor custody before a same-cohort restart. Publisher remains external.
	servers[1].Close()
	workerStops[1]()
	latest, e := runtimes[1].ExportCurrentAuthorityFloors()
	if e != nil {
		t.Fatal(e)
	}
	var before struct {
		M struct {
			Version uint64
			Digest  [32]byte
			Binding [32]byte
		}
	}
	if json.Unmarshal(latest, &before) != nil {
		t.Fatal("latest M")
	}
	if e = runtimes[1].Shutdown(); e != nil {
		t.Fatal("updated owner CLEAN shutdown", e)
	}
	floorsPath := filepath.Join(dir, "node-2", "floors.json")
	floors, e := os.ReadFile(floorsPath)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(floorsPath + ".state")
	if e != nil {
		t.Fatal(e)
	}
	var state struct {
		Phase  string
		Floors [32]byte
	}
	var after struct {
		M struct {
			Version uint64
			Digest  [32]byte
			Binding [32]byte
		}
	}
	if json.Unmarshal(raw, &state) != nil || state.Phase != "CLEAN" || state.Floors != sha256.Sum256(floors) || json.Unmarshal(floors, &after) != nil || before.M != after.M {
		t.Fatal("CLEAN did not capture latest exact membership floor")
	}
	servers[1], runtimes[1], workerStops[1] = restart(2)
	waitReady()
	checkSession()
	checkData()
	t.Log("native: latest M plus exact CLEAN hash and intact same-node resume passed")
	stopPublisher()
	for i := range runtimes {
		servers[i].Close()
		workerStops[i]()
		if e := runtimes[i].Shutdown(); e != nil {
			t.Fatal("final orderly shutdown", i, e)
		}
	}
}
