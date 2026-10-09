//go:build linux && (amd64 || arm64)

package security

// Opt-in test-only container fixture. These records are evidence
// and independently provisioned fixture inputs, never serialized authority.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type containerConfig struct {
	Participant    s2cParticipantConfig
	Identity       s3aIdentityFiles
	Limits         s3aLimits
	MembershipPath string
	OperatorPublic ed25519.PublicKey
	Self           peerauth.Member
}
type containerBootstrap struct {
	Head, Tree            string
	Image                 Image
	Cut                   SemanticCut
	Execution             S1ExecutionConfig
	Roots                 s2CapsuleRoots
	Members               []s2cMember
	Origins               []s2cOriginDescriptor
	Bounds                s2cBounds
	Scope, GenesisCapsule [32]byte
	Manifest              peerauth.ControlManifest
	Nodes                 map[uint32]containerConfig
}
type containerStamp struct {
	Boot                         [32]byte
	Process                      [16]byte
	Counter, Sequence, Low, High uint64
	Profile                      [32]byte
}
type containerCheckpoint struct {
	Phase, Mode                    string
	Bootstrap                      [32]byte
	Stamps                         map[uint32]containerStamp
	Anchors                        map[uint32]containerStamp
	Floors                         map[uint32]s3aFloors
	Serial                         map[uint32]uint64
	Files                          map[string][32]byte
	AppliedH, PendingH             []byte
	AppliedOutcome                 []byte
	PurposeTicket                  [32]byte
	AuthorityActive                bool
	PendingPurposes, ActiveOutputs int
}
type ContainerAuthorityGate struct {
	timeFixture *containerTimeFixture
	*CurrentAuthorityGate
	dir        string
	bootstrap  containerBootstrap
	checkpoint containerCheckpoint
	anchors    map[uint32]authorityTimeEstimate
	stop       chan os.Signal
	peers      map[uint32]*containerPeerListener
}

func containerMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func containerRead(t *testing.T, path string, value any) {
	t.Helper()
	b, err := os.ReadFile(path)
	containerMust(t, err)
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	containerMust(t, d.Decode(value))
}
func containerWrite(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	containerMust(t, err)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	containerMust(t, err)
	_, err = f.Write(append(b, '\n'))
	containerMust(t, err)
	containerMust(t, f.Sync())
	containerMust(t, f.Close())
	d, err := os.Open(filepath.Dir(path))
	containerMust(t, err)
	containerMust(t, d.Sync())
	containerMust(t, d.Close())
}
func containerDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	containerMust(t, err)
	return sha256.Sum256(b)
}
func containerCopy(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	containerMust(t, err)
	f, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	containerMust(t, err)
	_, err = f.Write(b)
	containerMust(t, err)
	containerMust(t, f.Sync())
	containerMust(t, f.Close())
}
func containerKey(t *testing.T, path string, raw []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	containerMust(t, err)
	_, err = f.Write(raw)
	containerMust(t, err)
	containerMust(t, f.Sync())
	containerMust(t, f.Close())
}
func containerObserve(t *testing.T, o *authorityTimeOwner) containerStamp {
	t.Helper()
	s, err := o.producer.sample()
	containerMust(t, err)
	r := containerStamp{Boot: s.boot, Process: s.process, Counter: s.nanos, Profile: o.profile}
	if now, err := o.current(); err == nil {
		r.Sequence, r.Low, r.High = now.sequence, now.utc.low, now.utc.high
	}
	return r
}

func NewContainerAuthorityGate(t *testing.T, issuer Issuer, dir string, resume bool) *ContainerAuthorityGate {
	t.Helper()
	var n *s3aTestNetwork
	var boot containerBootstrap
	var checkpoint containerCheckpoint
	if !resume {
		n = s3aTestCluster(t, nil)
		image := s1Image()
		image.Issuers = []Issuer{issuer}
		for i := range image.Principals {
			image.Principals[i].Identity.Issuer = issuer.URL
		}
		f, keys := authorityTestFixtureState(t, s2cTestClusterState(t, 3, s1Fixture(t, image)))
		n.f, n.manifest.Profile.ProtocolScope = f, f.trust.scope
		n.manifest.IssuedAt = time.Now().UTC().Add(-time.Minute)
		capsule, _, err := s2EncodeCapsule(f.genesis.state, f.genesis.roots, f.trust.bounds.Capsule)
		containerMust(t, err)
		boot = containerBootstrap{Head: os.Getenv("LANTERN_CONTAINER_HEAD"), Tree: os.Getenv("LANTERN_CONTAINER_TREE"), Image: image, Cut: f.genesis.state.projection.cut, Execution: f.genesis.state.configuration, Roots: f.genesis.roots, Members: f.members, Origins: f.origins, Bounds: f.trust.bounds, Scope: f.trust.scope, GenesisCapsule: s2LocalCapsuleDigest(capsule), Manifest: n.manifest, Nodes: map[uint32]containerConfig{}}
		for id, old := range n.configs {
			nodeDir := filepath.Join(dir, fmt.Sprintf("node-%d", id))
			containerMust(t, os.Mkdir(nodeDir, 0700))
			participant := s2cTestConfig(t, f, id, nodeDir)
			participant.Key = nil
			identity := s3aIdentityFiles{Roots: filepath.Join(nodeDir, "roots.pem"), Certificate: filepath.Join(nodeDir, "cert.pem"), TLSKey: filepath.Join(nodeDir, "tls.key"), VotingKey: filepath.Join(nodeDir, "vote.key")}
			for from, to := range map[string]string{old.Identity.Roots: identity.Roots, old.Identity.Certificate: identity.Certificate, old.Identity.TLSKey: identity.TLSKey, old.Identity.VotingKey: identity.VotingKey} {
				containerCopy(t, from, to)
			}
			containerKey(t, filepath.Join(nodeDir, "origin.key"), keys[id])
			directory, err := os.Open(nodeDir)
			containerMust(t, err)
			containerMust(t, directory.Sync())
			containerMust(t, directory.Close())
			participant.Trust = nil
			boot.Nodes[id] = containerConfig{participant, identity, old.Limits, filepath.Join(nodeDir, "membership"), old.Membership.Key, old.Membership.Self}
		}
		containerKey(t, filepath.Join(dir, "operator.key"), n.operator)
		// Written and synced before the first M/P/B family exists.
		containerWrite(t, filepath.Join(dir, "bootstrap.json"), boot)
	} else {
		containerRead(t, filepath.Join(dir, "checkpoint.json"), &checkpoint)
		if containerDigest(t, filepath.Join(dir, "bootstrap.json")) != checkpoint.Bootstrap {
			t.Fatal("independent bootstrap changed")
		}
		containerRead(t, filepath.Join(dir, "bootstrap.json"), &boot)
		if boot.Head != os.Getenv("LANTERN_CONTAINER_HEAD") || boot.Tree != os.Getenv("LANTERN_CONTAINER_TREE") {
			t.Fatal("source identity changed")
		}
		for path, digest := range checkpoint.Files {
			if containerDigest(t, path) != digest {
				t.Fatal("intact family changed before resume", path)
			}
		}
		projection, err := NewS1Projection(boot.Image, boot.Execution.Policy, boot.Cut.Domain, boot.Cut.Cohort, boot.Cut.Fences, boot.Cut.Generation)
		containerMust(t, err)
		if projection.cut != boot.Cut {
			t.Fatal("independent initial image drift")
		}
		members, err := s2cMemberSetDigest(boot.Members)
		containerMust(t, err)
		state, err := NewS1ApplyState(projection, members, boot.Execution.Capacity, S1Retention{})
		containerMust(t, err)
		genesis := &s2TrustedGenesis{state, boot.Roots}
		capsule, _, err := s2EncodeCapsule(state, boot.Roots, boot.Bounds.Capsule)
		containerMust(t, err)
		if s2LocalCapsuleDigest(capsule) != boot.GenesisCapsule {
			t.Fatal("original independent genesis differs")
		}
		trust, err := newS2CTrust(s2cBootstrap{genesis, boot.Members, boot.Origins, boot.Bounds})
		containerMust(t, err)
		if trust.scope != boot.Scope {
			t.Fatal("original trust scope differs")
		}
		n = &s3aTestNetwork{t: t, f: &s2cTestFixture{trust: trust, genesis: genesis, members: boot.Members, origins: boot.Origins}, configs: map[uint32]s3aConfig{}, nodes: map[uint32]*s3aOwner{}, listeners: map[uint32]net.Listener{}, manifest: boot.Manifest}
		n.operator, err = os.ReadFile(filepath.Join(dir, "operator.key"))
		containerMust(t, err)
		// Renew only the independently signed workload manifest, keeping the
		// identical profile, keys, origins and original genesis. No WAL fallback.
		for _, f := range checkpoint.Floors {
			if n.manifest.Version <= f.M.Version {
				n.manifest.Version = f.M.Version + 1
			}
		}
		n.manifest.IssuedAt = time.Now().UTC().Add(-time.Minute)
		n.manifest.ExpiresAt = time.Now().UTC().Add(5 * time.Minute)
		for _, v := range n.manifest.Profile.Voters {
			u, err := url.Parse(v.Workload.Origin)
			containerMust(t, err)
			host, _, err := net.SplitHostPort(u.Host)
			containerMust(t, err)
			if host != "127.0.0.1" {
				t.Fatal("fixture listener must remain loopback")
			}
			l, err := net.Listen("tcp", u.Host)
			containerMust(t, err)
			n.listeners[v.Voter] = l
		}
		t.Cleanup(func() {
			for _, o := range n.nodes {
				_ = o.Close()
			}
			for _, l := range n.listeners {
				_ = l.Close()
			}
		})
	}
	raw := n.sign(n.manifest)
	g := &ContainerAuthorityGate{CurrentAuthorityGate: &CurrentAuthorityGate{t, n, map[uint32]*authorityOriginOwner{}, map[uint32]*atomic.Uint64{}}, dir: dir, bootstrap: boot, checkpoint: checkpoint, anchors: map[uint32]authorityTimeEstimate{}}
	if os.Getenv("LANTERN_CONTAINER_PHASE") == "time-loss-fixture" {
		g.timeFixture = newContainerTimeFixture(t)
	}
	for id := uint32(1); id <= 3; id++ {
		b := boot.Nodes[id]
		b.Participant.Trust = n.f.trust
		c := s3aConfig{Participant: b.Participant, Identity: b.Identity, Limits: b.Limits, Membership: peerauth.ControlStoreOptions{Path: b.MembershipPath, Key: b.OperatorPublic, Profile: n.manifest.Profile, Self: b.Self}, Manifest: raw, hooks: &s3aHooks{beforeRenewal: func(ctx context.Context) { <-ctx.Done() }}}
		var o *authorityOriginOwner
		var err error
		originPath := filepath.Join(filepath.Dir(b.Identity.VotingKey), "origin.key")
		if g.timeFixture == nil {
			o, err = openCurrentAuthorityOwner(t.Context(), c, originPath, "https://admin.example", !resume, checkpoint.Floors[id])
		} else {
			clock := g.timeFixture.owner(t, id)
			containerMust(t, bindAuthorityNetworkTime(&c, clock))
			node, openErr := openS3AOwner(c, true, s3aFloors{})
			containerMust(t, openErr)
			node.ownedTime = true
			key, keyErr := loadAuthorityOriginKey(c, originPath)
			containerMust(t, keyErr)
			o, err = attachAuthorityOrigin(node, key, "https://admin.example")
		}
		containerMust(t, err)
		g.owners[id], n.nodes[id], n.configs[id] = o, o.network, c
		if resume {
			s := containerObserve(t, o.network.timeOwner)
			old := checkpoint.Stamps[id]
			if s.Process == old.Process || s.Profile != old.Profile {
				t.Fatal("process/profile recovery failure")
			}

			if o.network.receiver.active != nil || o.network.receiver.pending != nil || len(o.purposes.pending) != 0 || len(o.requests) != 0 || len(o.outputs.connections) != 0 || o.outputs.active != 0 || o.outputs.bytes != 0 {
				t.Fatal("volatile authority/purpose/output restored")
			}
			if o.network.kernel.originSerial != checkpoint.Serial[id] {
				t.Fatal("durable serial changed on reopen")
			}
		}
	}
	g.stop = make(chan os.Signal, 1)
	signal.Notify(g.stop, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(g.stop) })
	g.peers = map[uint32]*containerPeerListener{}
	for id, o := range g.owners {
		peer := &containerPeerListener{Listener: n.listeners[id]}
		g.peers[id] = peer
		containerMust(t, o.network.Start(peer))
	}
	if g.timeFixture == nil {
		g.LogEvidence(true)
	} else {
		t.Log("time-loss fixture: real Linux sampler/UDP/private quorum; synthetic fixture UTC; NOT configured-source production-constructor evidence")
	}
	return g
}

func (g *ContainerAuthorityGate) saveCheckpoint(phase string, applied, pending []byte, ticket [32]byte) {
	t := g.t
	c := containerCheckpoint{Phase: phase, Mode: os.Getenv("LANTERN_CONTAINER_MODE"), Bootstrap: containerDigest(t, filepath.Join(g.dir, "bootstrap.json")), Stamps: map[uint32]containerStamp{}, Floors: map[uint32]s3aFloors{}, Serial: map[uint32]uint64{}, Files: map[string][32]byte{}, AppliedH: applied, PendingH: pending, PurposeTicket: ticket}
	c.Anchors = map[uint32]containerStamp{}
	for id, o := range g.owners {
		c.Stamps[id] = containerObserve(t, o.network.timeOwner)
		clock := o.network.timeOwner
		clock.mu.Lock()
		if clock.anchor == nil {
			clock.mu.Unlock()
			t.Fatal("missing actual pre-interruption anchor")
		}
		anchor := *clock.anchor
		g.anchors[id] = anchor
		c.Anchors[id] = containerStamp{Boot: anchor.at.boot, Process: anchor.at.process, Counter: anchor.at.nanos, Sequence: clock.sequence, Low: anchor.utc.low, High: anchor.utc.high, Profile: clock.profile}
		clock.mu.Unlock()
		f, err := o.network.Floors()
		containerMust(t, err)
		c.Floors[id] = f
		o.network.kernel.gate.Lock()
		c.Serial[id] = o.network.kernel.originSerial
		o.network.kernel.gate.Unlock()
		cfg := g.n.configs[id]
		for _, path := range []string{cfg.Membership.Path, cfg.Participant.PPath, cfg.Participant.PPath + ".tip", cfg.Participant.BPath, cfg.Participant.BPath + ".tip", cfg.Identity.Roots, cfg.Identity.Certificate, cfg.Identity.TLSKey, cfg.Identity.VotingKey, filepath.Join(filepath.Dir(cfg.Identity.VotingKey), "origin.key")} {
			c.Files[path] = containerDigest(t, path)
		}
	}
	o := g.owners[1]
	o.network.kernel.gate.Lock()
	c.AuthorityActive = o.network.receiver.active != nil
	o.network.kernel.gate.Unlock()
	o.purposes.mu.Lock()
	c.PendingPurposes = len(o.purposes.pending)
	o.purposes.mu.Unlock()
	o.outputs.mu.Lock()
	c.ActiveOutputs = o.outputs.active
	o.outputs.mu.Unlock()
	if len(applied) > 0 {
		h, err := verifyHistoricalH(g.n.f.trust, applied)
		containerMust(t, err)
		r, err := o.lookupOriginal(t.Context(), h.handoff.id, h.handoff.operation)
		containerMust(t, err)
		if r.outcome == nil {
			t.Fatal("missing applied outcome")
		}
		c.AppliedOutcome, err = json.Marshal(s2Outcome(r.outcome))
		containerMust(t, err)
	}
	g.checkpoint = c
	containerWrite(t, filepath.Join(g.dir, "checkpoint.json"), c)
}

// The external controller operates only this task container. The worker never
// controls the host clock, power, namespaces, Docker socket or other processes.
func (g *ContainerAuthorityGate) checkpointWait(phase string, applied, pending []byte, ticket [32]byte) {
	g.saveCheckpoint(phase, applied, pending, ticket)
	containerWrite(g.t, filepath.Join(g.dir, "ready.json"), map[string]any{"phase": phase, "mode": os.Getenv("LANTERN_CONTAINER_MODE"), "checkpoint": containerDigest(g.t, filepath.Join(g.dir, "checkpoint.json")), "pid": os.Getpid()})
	deadline := time.Now().Add(7 * time.Minute)
	for {
		if _, err := os.Stat(filepath.Join(g.dir, "release.json")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			panic("bounded container checkpoint wait expired")
		}
		select {
		case <-g.stop:
			if phase != "pre-stop" {
				panic("unexpected stop outside graceful case")
			}
			containerWrite(g.t, filepath.Join(g.dir, "stop-request.json"), map[string]any{"signal": "SIGTERM"})
			return
		case <-g.t.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (g *ContainerAuthorityGate) ExerciseContainerBoundary(p CurrentCredentialProducer, phase string, applied, pending []byte, ticket [32]byte) {
	t := g.t
	o := g.owners[1]
	var staleRequest CurrentAuthorityGateRequest
	if phase != "pre-stop" {
		var err error
		staleRequest, err = g.Prepare(p, s1Changes(s1ReaderRole()))
		containerMust(t, err)
	}
	var once sync.Once
	pause := func() { once.Do(func() { g.checkpointWait(phase, applied, pending, ticket) }) }
	hooks := &authorityOutputHooks{record: func(now authorityCurrentTime, frame uint64) {
		containerWrite(t, filepath.Join(g.dir, "authorization.json"), map[string]any{"stamp": containerStamp{Boot: now.stamp.boot, Process: now.stamp.process, Counter: now.stamp.nanos, Sequence: now.sequence, Low: now.utc.low, High: now.utc.high, Profile: now.profile}, "frame": frame, "payload": "container-exact-unit", "cookie": "container-exact-token"})
	}}
	if phase == "pause-before" {
		hooks.beforeSample = pause
	} else {
		hooks.afterAuthorize = pause
	}
	o.outputHooks.Store(hooks)
	routes := []authorityOutputRoute{authorityOutputUnary("/private.Container/Call", func(ctx context.Context, r *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
		v := connect.NewResponse(wrapperspb.String("container-exact-unit"))
		v.Header().Set("Set-Cookie", "container-exact-token")
		return v, nil
	})}
	srv, err := o.newOutputServer(routes, func(*http.Request) (CurrentCredentialProducer, error) { return p, nil }, func(*http.Request) authorityOutputRequirement {
		return authorityOutputRequirement{action: SecurityManage}
	})
	containerMust(t, err)
	server := httptest.NewUnstartedServer(srv.Handler)
	server.Config = srv
	server.TLS = srv.TLSConfig
	server.StartTLS()
	defer server.Close()
	client := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+"/private.Container/Call")
	response, callErr := client.CallUnary(t.Context(), connect.NewRequest(wrapperspb.String("one")))
	if phase == "pre-stop" {
		// The SIGTERM handler released the entered output. Close joins the real
		// owners before the process returns; an abrupt kill never reaches this file.
		containerMust(t, callErr)
		for _, owner := range g.owners {
			containerMust(t, owner.network.Close())
		}
		containerWrite(t, filepath.Join(g.dir, "graceful-close.json"), map[string]any{"owners_joined": 3, "output_completed": true})
		return
	}

	end := containerObserve(t, o.network.timeOwner)
	start := g.checkpoint.Stamps[1]
	if end.Boot != start.Boot || end.Process != start.Process || end.Counter < start.Counter {
		t.Fatal("pause replaced/regressed epoch")
	}
	elapsed, err := authorityOperationalTimePremises().elapsed(authorityTimeStamp{start.Boot, start.Process, start.Counter}, authorityTimeStamp{end.Boot, end.Process, end.Counter})
	containerMust(t, err)
	if elapsed.low < uint64(75*time.Second) {
		t.Fatal("independent pause campaign duration not reached; not container acceptance")
	}
	// Test the retained pre-pause anchor, independent of a background fresh
	// measurement that may legitimately arrive immediately after wake.
	old := g.anchors[1]
	_, oldErr := old.atSample(authorityTimeStamp{end.Boot, end.Process, end.Counter})
	if oldErr == nil {
		t.Fatal("old anchor survived beyond holdover")
	}
	if phase == "pause-before" {
		if callErr == nil || response != nil {
			t.Fatal("new output authorized after expired challenge")
		}
		if _, err := os.Stat(filepath.Join(g.dir, "authorization.json")); !os.IsNotExist(err) {
			t.Fatal("pre-event output got authorization")
		}
	} else if callErr == nil {
		if response.Msg.Value != "container-exact-unit" || response.Header().Get("Set-Cookie") != "container-exact-token" {
			t.Fatal("authorized unit mutated")
		}
	}
	o.outputHooks.Store(nil)
	if _, err := client.CallUnary(t.Context(), connect.NewRequest(wrapperspb.String("next"))); err == nil {
		t.Fatal("next output inherited old permit")
	}
	if raw, err := g.Consume(staleRequest, p, [32]byte{}); err == nil || len(raw) > 0 {
		t.Fatal("pre-pause request consumed into new H after expiry")
	}
	containerWrite(t, filepath.Join(g.dir, "boundary-result.json"), map[string]any{"phase": phase, "mode": os.Getenv("LANTERN_CONTAINER_MODE"), "before": start, "after": end, "elapsed_low_ns": elapsed.low, "elapsed_high_ns": elapsed.high, "old_anchor_refused": true, "next_output_refused": true, "new_H_consume_refused": true, "exact_late_unit_delivered": phase == "pause-after" && callErr == nil, "transport_interrupted": phase == "pause-after" && callErr != nil, "call_error": fmt.Sprint(callErr)})
	// Recovery is independently requalified by the unchanged production owner.
	for _, member := range g.owners {
		deadline := time.Now().Add(150 * time.Second)
		for {
			if _, err := member.network.timeOwner.current(); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("bounded fresh native time recovery unavailable")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	g.Renew()
	r, err := g.Prepare(p, s1Changes(s1ReaderRole()))
	containerMust(t, err)
	_, err = g.Consume(r, p, [32]byte{})
	containerMust(t, err)
	containerWrite(t, filepath.Join(g.dir, "recovery-result.json"), map[string]any{"fresh_native_time_and_quorum": true, "new_H_after_fresh_renewal": true, "stamp": containerObserve(t, o.network.timeOwner)})
}

func (g *ContainerAuthorityGate) ResumeContainerProcess(p CurrentCredentialProducer) {
	t := g.t
	o := g.owners[1]
	c := g.checkpoint
	if !c.AuthorityActive || c.PendingPurposes == 0 || c.ActiveOutputs != 1 {
		t.Fatal("pre-stop evidence did not retain live volatile state")
	}
	if _, err := g.Start(c.PurposeTicket); err == nil {
		t.Fatal("purpose ticket survived restart")
	}
	for _, raw := range [][]byte{c.AppliedH, c.PendingH} {
		h, err := verifyHistoricalH(g.n.f.trust, raw)
		containerMust(t, err)
		r, err := o.lookupOriginal(t.Context(), h.handoff.id, h.handoff.operation)
		containerMust(t, err)
		// Terminal lookup deliberately returns an outcome rather than the H
		// body. Verify the separately replayed original bytes as well.
		o.network.kernel.gate.Lock()
		retained := o.network.kernel.origins[h.digest()]
		exact := retained != nil && retained.raw == string(raw)
		o.network.kernel.gate.Unlock()
		if !exact || r.digest != h.digest() {
			t.Fatal("retained original H replaced")
		}
		if bytes.Equal(raw, c.AppliedH) {
			if r.outcome == nil {
				t.Fatal("applied outcome lost")
			}
			b, err := json.Marshal(s2Outcome(r.outcome))
			containerMust(t, err)
			if !bytes.Equal(b, c.AppliedOutcome) {
				t.Fatal("original applied outcome changed")
			}
		}
	}
	old, err := verifyHistoricalH(g.n.f.trust, c.PendingH)
	containerMust(t, err)
	g.AwaitLower(old.handoff.authorization.credentialDeadline.Add(time.Nanosecond))
	// Historical completion needs no new user credential or restored permit.
	result, err := o.lookupOriginal(t.Context(), old.handoff.id, old.handoff.operation)
	containerMust(t, err)
	_, err = o.network.Drive(s3aTestContext(t), result.digest)
	containerMust(t, err)
	result, err = o.lookupOriginal(t.Context(), old.handoff.id, old.handoff.operation)
	containerMust(t, err)
	if result.digest != old.digest() || result.outcome == nil || result.outcome.disposition != S1Applied {
		t.Fatal("expired original completion changed")
	}
	// Prepare freezes a review; only final consume requires current quorum.
	// Test after pending completion so pending-capacity refusal cannot mask it.
	beforeRenewal, err := g.Prepare(p, s1Changes(s1ReaderRole()))
	containerMust(t, err)
	if raw, err := g.Consume(beforeRenewal, p, [32]byte{}); err == nil || len(raw) != 0 {
		t.Fatal("new H consumed before fresh quorum")
	}
	g.Renew()
	r, err := g.Prepare(p, s1Changes(s1ReaderRole()))
	containerMust(t, err)
	raw, err := g.Consume(r, p, [32]byte{})
	containerMust(t, err)
	newH, err := verifyHistoricalH(g.n.f.trust, raw)
	containerMust(t, err)
	if newH.handoff.serial <= c.Serial[1] {
		t.Fatal("origin serial reused")
	}
	containerWrite(t, filepath.Join(g.dir, "restart-result.json"), map[string]any{"mode": os.Getenv("LANTERN_CONTAINER_MODE"), "old": c.Stamps[1], "new": containerObserve(t, o.network.timeOwner), "original_applied_outcome_identical": true, "pending_original_H_identical": true, "expired_original_completed": true, "volatile_authority_purpose_output_absent": true, "fresh_time_quorum_required": true, "old_serial": c.Serial[1], "new_serial": newH.handoff.serial, "bootstrap": c.Bootstrap})
}

// A test-only real TCP acceptance fault, not a fake renewal/credential/clock.
// Existing short RPCs finish before the controller's outage wait; new sockets
// close before TLS while the same private owners and enrolled listeners live.
type containerPeerListener struct {
	net.Listener
	unavailable atomic.Bool
	rejected    atomic.Uint64
}

func (l *containerPeerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if !l.unavailable.Load() {
			return c, nil
		}
		l.rejected.Add(1)
		_ = c.Close()
	}
}
func (g *ContainerAuthorityGate) ExerciseNetwork(p CurrentCredentialProducer, phase string) {
	t := g.t
	o := g.owners[1]
	r, err := g.Prepare(p, s1Changes(s1ReaderRole()))
	containerMust(t, err)
	if phase == "peer-loss" {
		g.peers[2].unavailable.Store(true)
		g.peers[3].unavailable.Store(true)
	}
	if g.timeFixture != nil {
		g.stopTimeFixture()
	}
	g.saveCheckpoint(phase, nil, nil, [32]byte{})
	containerWrite(t, filepath.Join(g.dir, "ready.json"), map[string]any{"phase": phase, "pid": os.Getpid()})
	containerWaitMarker(t, g.dir, "check.json")
	if g.timeFixture != nil {
		g.verifyTimeFixtureLoss()
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	err = o.network.renewAuthority(ctx)
	cancel()
	if err == nil {
		t.Fatal("outage renewed authority")
	}
	if phase == "peer-loss" && g.peers[2].rejected.Load()+g.peers[3].rejected.Load() == 0 {
		t.Fatal("no real peer socket was rejected")
	}
	if raw, err := g.Consume(r, p, [32]byte{}); err == nil || len(raw) > 0 {
		t.Fatal("outage consumed new H")
	}
	containerWrite(t, filepath.Join(g.dir, "fault-result.json"), map[string]any{"phase": phase, "new_H_refused": true, "fresh_renewal_refused": true, "rejected_peer_sockets": g.peers[2].rejected.Load() + g.peers[3].rejected.Load(), "stamp": containerObserve(t, o.network.timeOwner)})
	containerWaitMarker(t, g.dir, "release.json")
	if g.timeFixture != nil {
		g.timeFixture.mu.Lock()
		g.timeFixture.drop = false
		g.timeFixture.mu.Unlock()
	}
	g.peers[2].unavailable.Store(false)
	g.peers[3].unavailable.Store(false)
	deadline := time.Now().Add(150 * time.Second)
	for {
		valid := true
		for _, owner := range g.owners {
			if _, err := owner.network.timeOwner.current(); err != nil {
				valid = false
			}
		}
		if valid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source recovery unavailable within unchanged backoff")
		}
		time.Sleep(50 * time.Millisecond)
	}
	g.Renew()
	if g.timeFixture != nil {
		g.verifyTimeFixtureRecovery()
	}
	r, err = g.Prepare(p, s1Changes(s1ReaderRole()))
	containerMust(t, err)
	raw, err := g.Consume(r, p, [32]byte{})
	containerMust(t, err)
	g.Apply(raw)
	containerWrite(t, filepath.Join(g.dir, "recovery-result.json"), map[string]any{"phase": phase, "fresh_native_time_and_quorum": true, "new_H_applied": true, "stamp": containerObserve(t, o.network.timeOwner)})
}
