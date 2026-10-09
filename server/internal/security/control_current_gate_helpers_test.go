package security

// Test-only bridge into the private composition. External gate tests import the
// real provider/oidc packages without reversing production dependencies. Every
// genesis below is independently provisioned before M/P/B creation; no running
// Store/revision is relabeled as authenticated S1 history.
import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type CurrentAuthorityGate struct {
	t      *testing.T
	n      *s3aTestNetwork
	owners map[uint32]*authorityOriginOwner
	ticks  map[uint32]*atomic.Uint64
}
type CurrentAuthorityGateRequest struct{ request *authorityOriginRequest }

func NewCurrentAuthorityGate(t *testing.T, issuer Issuer, sessions []Session, native bool) *CurrentAuthorityGate {
	t.Helper()
	n := s3aTestCluster(t, nil)
	image := s1Image()
	image.Issuers = []Issuer{issuer}
	for i := range image.Principals {
		image.Principals[i].Identity.Issuer = issuer.URL
	}
	image.Sessions = sessions
	f, keys := authorityTestFixtureState(t, s2cTestClusterState(t, 3, s1Fixture(t, image)))
	n.f, n.manifest.Profile.ProtocolScope = f, f.trust.scope
	// Operator profile admits enough UTC error for the conditional native
	// source; source error is not hidden by widening final credential checks.
	n.manifest.IssuedAt = time.Now().UTC().Add(-time.Minute)
	raw := n.sign(n.manifest)
	g := &CurrentAuthorityGate{t, n, make(map[uint32]*authorityOriginOwner), make(map[uint32]*atomic.Uint64)}
	for id, c := range n.configs {
		c.Participant.Trust, c.Membership.Profile, c.Manifest = f.trust, n.manifest.Profile, raw
		c.Participant.BScope = s2cTestConfig(t, f, id, filepath.Dir(c.Participant.PPath)).BScope
		c.hooks = &s3aHooks{beforeRenewal: func(ctx context.Context) { <-ctx.Done() }}
		keyPath := filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key")
		if err := os.WriteFile(keyPath, keys[id], 0600); err != nil {
			t.Fatal(err)
		}
		var o *authorityOriginOwner
		var err error
		if native {
			o, err = openCurrentAuthorityOwner(t.Context(), c, keyPath, "https://admin.example", true, s3aFloors{})
		} else {
			clock, ticks := fakeAuthorityTimeOwnerAt(t, time.Now().UTC())
			g.ticks[id] = ticks
			if err = bindAuthorityNetworkTime(&c, clock); err != nil {
				t.Fatal(err)
			}
			node, openErr := createS3AOwner(c)
			err = openErr
			if err == nil {
				o, err = attachAuthorityOrigin(node, keys[id], "https://admin.example")
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		g.owners[id], n.nodes[id], n.configs[id] = o, o.network, c
	}
	for id, o := range g.owners {
		if err := o.network.Start(n.listeners[id]); err != nil {
			t.Fatal(err)
		}
	}
	return g
}
func (g *CurrentAuthorityGate) TimeBounds() (time.Time, time.Time, error) {
	k := g.owners[1].network.kernel
	k.gate.Lock()
	v := CurrentCredentialView{k.replayState.projection, g.owners[1].network.timeOwner, "https://admin.example"}
	k.gate.Unlock()
	return v.TimeBounds()
}
func (g *CurrentAuthorityGate) Now() time.Time {
	_, high, err := g.TimeBounds()
	if err != nil {
		return time.Unix(0, int64(^uint64(0)>>1)).UTC()
	}
	return high
}
func (g *CurrentAuthorityGate) LogEvidence(native bool) {
	g.t.Helper()
	for id, o := range g.owners {
		now, err := o.network.timeOwner.current()
		if err != nil {
			g.t.Fatal(err)
		}
		if native {
			o.network.timeOwner.mu.Lock()
			observation := o.network.timeOwner.lastObservation
			o.network.timeOwner.mu.Unlock()
			if observation == nil {
				g.t.Fatal("missing actual anchor transcript")
			}
			g.t.Logf("native anchor member=%d host=%s configuration=%x endpoint=%s sequence=%d sent=%d received=%d request=%x response=%x; conditional honest-source/path/1000ppm assumptions; no authenticated NTP", id, observation.source.host, observation.source.configuration, observation.endpoint, observation.sequence, observation.sent.nanos, observation.received.nanos, observation.request, observation.response)
		}
		g.t.Logf("native_source=%t member=%d profile=%x boot=%x process=%x counter=%d source_sequence=%d UTC=[%d,%d] P_serial=%d", native, id, now.profile, now.stamp.boot, now.stamp.process, now.stamp.nanos, now.sequence, now.utc.low, now.utc.high, o.network.kernel.originSerial)
	}
}
func (g *CurrentAuthorityGate) Cut() SemanticCut {
	k := g.owners[1].network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	return k.replayState.projection.cut
}
func (g *CurrentAuthorityGate) Advance(d time.Duration) {
	if len(g.ticks) == 0 {
		time.Sleep(d)
		return
	}
	for _, ticks := range g.ticks {
		ticks.Add(uint64(d))
	}
}
func (g *CurrentAuthorityGate) Renew() {
	g.t.Helper()
	authorityTestConverge(g.t, g.owners)
	for id, o := range g.owners {
		if !o.network.closed.Load() {
			if err := o.network.renewAuthority(s3aTestContext(g.t)); err != nil {
				g.t.Fatal("renew member", id, err)
			}
		}
	}
}

func (g *CurrentAuthorityGate) Prepare(p CurrentCredentialProducer, command S1Command) (CurrentAuthorityGateRequest, error) {
	r, err := g.owners[1].prepare(g.t.Context(), p, g.Cut(), command)
	return CurrentAuthorityGateRequest{r}, err
}
func (g *CurrentAuthorityGate) Consume(r CurrentAuthorityGateRequest, p CurrentCredentialProducer, proof [32]byte) ([]byte, error) {
	h, err := g.owners[1].consume(g.t.Context(), r.request, p, proof)
	return []byte(h.raw), err
}
func (g *CurrentAuthorityGate) Begin(r CurrentAuthorityGateRequest, p CurrentCredentialProducer) (AuthorizationStart, error) {
	return g.owners[1].beginPurpose(g.t.Context(), r.request, p)
}
func (g *CurrentAuthorityGate) Start(ticket [32]byte) ([32]byte, error) {
	id, _, err := g.owners[1].startPurpose(g.t.Context(), ticket)
	return id, err
}
func (g *CurrentAuthorityGate) Complete(id [32]byte, p CurrentCredentialProducer) (AuthorizationStatus, error) {
	return g.owners[1].completePurpose(g.t.Context(), id, p)
}
func (g *CurrentAuthorityGate) Apply(raw []byte) {
	g.t.Helper()
	o := g.owners[1]
	h, err := o.carryOriginal(g.t.Context(), raw)
	if err != nil {
		g.t.Fatal(err)
	}
	if _, err := o.network.Drive(s3aTestContext(g.t), h.digest); err != nil {
		g.t.Fatal(err)
	}
	verified, err := verifyHistoricalH(o.network.kernel.trust, raw)
	if err != nil {
		g.t.Fatal(err)
	}
	_, outcome, _, err := o.network.kernel.LookupOriginal(verified.handoff.id)
	if err != nil || outcome == nil || outcome.disposition != S1Applied {
		g.t.Fatal("not applied", outcome, err)
	}
}
func (g *CurrentAuthorityGate) ExerciseOutput(p CurrentCredentialProducer) {
	g.t.Helper()
	o := g.owners[1]
	routes := []authorityOutputRoute{
		authorityOutputUnary("/private.Unary/Call", func(ctx context.Context, r *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
			result := connect.NewResponse(wrapperspb.String("genuine protected unary"))
			result.Header().Set("Set-Cookie", "genuine-unit-token")
			return result, nil
		}),
		authorityOutputStreaming("/private.Stream/Call", func(ctx context.Context, r *connect.Request[wrapperspb.StringValue], stream *connect.ServerStream[wrapperspb.StringValue]) error {
			for _, v := range []string{"first", "second"} {
				if err := stream.Send(wrapperspb.String(v)); err != nil {
					return err
				}
			}
			return nil
		}),
	}
	private, err := o.newOutputServer(routes, func(*http.Request) (CurrentCredentialProducer, error) { return p, nil }, func(*http.Request) authorityOutputRequirement {
		return authorityOutputRequirement{action: SecurityManage}
	})
	if err != nil {
		g.t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(private.Handler)
	server.Config = private
	server.TLS = private.TLSConfig
	server.StartTLS()
	defer server.Close()
	client := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+"/private.Unary/Call")
	result, err := client.CallUnary(g.t.Context(), connect.NewRequest(wrapperspb.String("go")))
	if err != nil || result.Msg.Value != "genuine protected unary" || result.Header().Get("Set-Cookie") != "genuine-unit-token" {
		g.t.Fatal("genuine unary path", err)
	}
	streams := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+"/private.Stream/Call")
	stream, err := streams.CallServerStream(g.t.Context(), connect.NewRequest(wrapperspb.String("go")))
	if err != nil {
		g.t.Fatal(err)
	}
	defer stream.Close()
	count := 0
	for stream.Receive() {
		count++
	}
	if count != 2 || stream.Err() != nil {
		g.t.Fatal("genuine bounded stream", count, stream.Err())
	}
}

func CurrentAuthorityOutputOptions() []connect.HandlerOption { return authorityOutputOptions() }
func (g *CurrentAuthorityGate) OutputHooks(before, after func()) {
	g.owners[1].outputHooks.Store(&authorityOutputHooks{beforeSample: before, afterAuthorize: after})
}

func (g *CurrentAuthorityGate) RestartAndRecover(raw []byte) {
	g.t.Helper()
	old := g.owners[1]
	original, err := verifyHistoricalH(old.network.kernel.trust, raw)
	if err != nil {
		g.t.Fatal(err)
	}
	stamp, err := old.network.timeOwner.current()
	if err != nil {
		g.t.Fatal(err)
	}
	floors, err := old.network.Floors()
	if err != nil {
		g.t.Fatal(err)
	}
	address := g.n.listeners[1].Addr().String()
	if err := old.network.Close(); err != nil {
		g.t.Fatal(err)
	}
	c := g.n.configs[1]
	var current *authorityOriginOwner
	if len(g.ticks) == 0 {
		current, err = openCurrentAuthorityOwner(g.t.Context(), c, filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key"), "https://admin.example", false, floors)
	} else {
		var network *s3aOwner
		network, err = resumeS3AOwner(c, floors)
		if err == nil {
			var key []byte
			key, err = loadAuthorityOriginKey(c, filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key"))
			if err == nil {
				current, err = attachAuthorityOrigin(network, key, "https://admin.example")
			}
		}
	}
	if err != nil {
		g.t.Fatal("intact M/P/B native resume", err)
	}
	g.owners[1], g.n.nodes[1] = current, current.network
	if current.network.receiver.active != nil || current.network.receiver.pending != nil || len(current.purposes.pending) != 0 {
		g.t.Fatal("volatile capability restored")
	}
	now, err := current.network.timeOwner.current()
	if err != nil {
		g.t.Fatal(err)
	}
	if len(g.ticks) == 0 && now.stamp.process == stamp.stamp.process {
		g.t.Fatal("process time epoch reused")
	}
	recovered, err := current.lookupOriginal(g.t.Context(), original.handoff.id, original.handoff.operation)
	if err != nil || recovered.raw != string(raw) {
		g.t.Fatal("original H was replaced on recovery", err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		g.t.Fatal(err)
	}
	g.n.listeners[1] = listener
	if err := current.network.Start(listener); err != nil {
		g.t.Fatal(err)
	}
	g.Renew()
	g.t.Logf("intact process-owner restart: old_process=%x new_process=%x exact_original_H=%x; no OS reboot performed", stamp.stamp.process, now.stamp.process, recovered.digest)
}

func (g *CurrentAuthorityGate) AwaitLower(target time.Time) {
	g.t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for {
		low, _, err := g.TimeBounds()
		if err != nil {
			g.t.Fatal(err)
		}
		if !low.Before(target) {
			return
		}
		if time.Now().After(deadline) {
			g.t.Fatal("strict lower bound did not reach original event")
		}
		g.Advance(20 * time.Millisecond)
	}
}

func (g *CurrentAuthorityGate) ExercisePausedOutput(p CurrentCredentialProducer, phase, dir string) {
	g.t.Helper()
	o := g.owners[1]
	if phase != "before" && phase != "after" {
		g.t.Fatal("unknown pause phase")
	}
	var once sync.Once
	pause := func() {
		once.Do(func() {
			if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(phase), 0600); err != nil {
				panic(err)
			}
			deadline := time.Now().Add(45 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "continue")); err == nil {
					return
				}
				if time.Now().After(deadline) {
					panic("parent did not resume owned child")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
	hooks := &authorityOutputHooks{record: func(sample authorityCurrentTime, frame uint64) {
		raw, err := json.Marshal(struct {
			Counter, Low, High, Frame uint64
			Process                   [16]byte
		}{sample.stamp.nanos, sample.utc.low, sample.utc.high, frame, sample.stamp.process})
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "authorization.json"), raw, 0600); err != nil {
			panic(err)
		}
	}}
	if phase == "before" {
		hooks.beforeSample = pause
	} else {
		hooks.afterAuthorize = pause
	}
	o.outputHooks.Store(hooks)
	routes := []authorityOutputRoute{authorityOutputUnary("/private.Unary/Call", func(ctx context.Context, r *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
		result := connect.NewResponse(wrapperspb.String("exact paused unit"))
		result.Header().Set("Set-Cookie", "exact-paused-token")
		return result, nil
	})}
	private, err := o.newOutputServer(routes, func(*http.Request) (CurrentCredentialProducer, error) { return p, nil }, func(*http.Request) authorityOutputRequirement {
		return authorityOutputRequirement{action: SecurityManage}
	})
	if err != nil {
		g.t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(private.Handler)
	server.Config = private
	server.TLS = private.TLSConfig
	server.StartTLS()
	defer server.Close()
	client := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+"/private.Unary/Call")
	response, err := client.CallUnary(g.t.Context(), connect.NewRequest(wrapperspb.String("go")))
	if phase == "before" {
		if err == nil || response != nil {
			g.t.Fatal("pre-event pause leaked output")
		}
		if _, err := os.Stat(filepath.Join(dir, "authorization.json")); !os.IsNotExist(err) {
			g.t.Fatal("expired output got final authorization")
		}
	} else {
		if err != nil || response.Msg.Value != "exact paused unit" || response.Header().Get("Set-Cookie") != "exact-paused-token" {
			g.t.Fatal("immutable preexpiry unit did not finish", err)
		}
		o.outputHooks.Store(nil)
		if _, err := client.CallUnary(g.t.Context(), connect.NewRequest(wrapperspb.String("next"))); err == nil {
			g.t.Fatal("next output inherited expired event")
		}
	}
	current, err := o.network.timeOwner.current()
	if err != nil {
		g.t.Fatal(err)
	}
	receipt, err := json.Marshal(struct {
		Phase          string
		ArrivalCounter uint64
		CurrentUpper   uint64
		NextRefused    bool
	}{phase, current.stamp.nanos, current.utc.high, true})
	if err != nil {
		g.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "receipt.json"), receipt, 0600); err != nil {
		g.t.Fatal(err)
	}
}

func (g *CurrentAuthorityGate) ExpiredOriginalAfterOriginLoss(raw []byte) {
	g.t.Helper()
	origin := g.owners[1]
	h, err := verifyHistoricalH(origin.network.kernel.trust, raw)
	if err != nil {
		g.t.Fatal(err)
	}
	g.AwaitLower(h.handoff.authorization.credentialDeadline.Add(time.Nanosecond))
	if err := origin.network.Close(); err != nil {
		g.t.Fatal(err)
	}
	relay := g.owners[2]
	carried, err := relay.carryOriginal(g.t.Context(), raw)
	if err != nil || carried.raw != string(raw) || relay.network.kernel.originSerial != 0 {
		g.t.Fatal("expired original transfer", err)
	}
	if _, err := relay.network.Drive(s3aTestContext(g.t), carried.digest); err != nil {
		g.t.Fatal(err)
	}
	result, err := relay.lookupOriginal(g.t.Context(), h.handoff.id, h.handoff.operation)
	if err != nil || result.outcome == nil || result.outcome.disposition != S1RejectedCAS || result.outcome.id != h.handoff.id {
		g.t.Fatal("expired H lost exact original CAS result", err)
	}
	g.t.Logf("expired original origin-loss completion: original_digest=%x namespace=%d exact_disposition=%s; no new credential/purpose consumed", carried.digest, h.handoff.id.Namespace, result.outcome.disposition)
}

func (g *CurrentAuthorityGate) MeasureWarmOutput(p CurrentCredentialProducer) {
	g.t.Helper()
	o := g.owners[1]
	if !o.network.enterCall() {
		g.t.Fatal("closed measurement owner")
	}
	defer o.network.calls.Done()
	c, err := o.authenticate(g.t.Context(), p)
	if err != nil {
		g.t.Fatal(err)
	}
	o.network.kernel.gate.Lock()
	beforeP := o.network.kernel.p.count
	o.network.kernel.gate.Unlock()
	const count = 1024
	samples := make([]int64, count)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range samples {
		start := time.Now()
		if _, err := o.authorizeOutput(g.t.Context(), c, authorityOutputRequirement{action: SecurityManage}); err != nil {
			g.t.Fatal(err)
		}
		samples[i] = time.Since(start).Nanoseconds()
	}
	runtime.ReadMemStats(&after)
	slices.Sort(samples)
	o.network.kernel.gate.Lock()
	afterP := o.network.kernel.p.count
	o.network.kernel.gate.Unlock()
	if beforeP != afterP {
		g.t.Fatal("warm output appended protocol state")
	}
	g.t.Logf("focused warm native final-authorization predicate: samples=%d median_ns=%d p99_ns=%d max_ns=%d total_allocated_bytes=%d P_append_delta=%d; excludes OIDC verification/encoding/transport; no quorum/fsync/network-time call in predicate; observations are not latency safety bounds", count, samples[count/2], samples[count*99/100], samples[count-1], after.TotalAlloc-before.TotalAlloc, afterP-beforeP)
}
