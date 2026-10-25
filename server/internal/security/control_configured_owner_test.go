package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func configuredOwnerStatement(k *s2cParticipant) configuredTrustStatement {
	return configuredTrustStatement{configuredTrustVersion, k.trust.scope, k.replayState.projection.cut, k.replayState.prefix, k.config.configured.Digest(), k.replayState.slot, sha256.Sum256([]byte(authorityTimeProfileDescription))}
}

func configuredOwnerVotes(t *testing.T, owners map[uint32]*authorityOriginOwner, s configuredTrustStatement) []configuredTrustEndorsement {
	t.Helper()
	var votes []configuredTrustEndorsement
	for _, o := range owners {
		k := o.network.kernel
		k.gate.Lock()
		vote, err := o.endorseConfiguredLocked(s)
		k.gate.Unlock()
		if err == nil {
			votes = append(votes, vote)
		}
	}
	sort.Slice(votes, func(i, j int) bool { return votes[i].Member < votes[j].Member })
	return votes
}

func configuredOwnerConverge(t *testing.T, n *s2cTestNetwork, proposer uint32) {
	t.Helper()
	k := n.nodes[proposer]
	raw, err := k.ExportChosen(k.replayState.slot, 1, k.config.OutboxBytes)
	if err != nil || len(raw) != 1 {
		t.Fatal("chosen export", err)
	}
	for member, node := range n.nodes {
		if member == proposer {
			continue
		}
		if _, err = node.Receive(raw[0]); err != nil {
			t.Fatal("install authentic chosen", member, err)
		}
	}
	n.queue = nil
}

func TestConfiguredOwnerDetachedSelectionAndMissingInput(t *testing.T) {
	f, _, desired := configuredTestFixture(t, 3)
	p := &CurrentProvisioning{config: s3aConfig{Participant: s2cParticipantConfig{Trust: f.trust, Member: 1, OwnedOrigin: 1}}}
	observer := &CurrentProvisioning{config: s3aConfig{Participant: s2cParticipantConfig{Trust: f.trust, Member: 3, OwnedOrigin: 0}}}
	if _, err := observer.WithConfiguredTrust(desired); err != nil {
		t.Fatal("fixed voter without consume origin could not select desired intent", err)
	}
	before := f.genesis.state.projection.cut
	selected, err := p.WithConfiguredTrust(desired)
	if err != nil || selected == p || selected.config.Participant.configured == desired || p.config.Participant.configured != nil || f.genesis.state.projection.cut != before {
		t.Fatal("desired selection aliased original or changed genesis", err)
	}
	desired.issuers[0].ClientID = "changed-after-selection"
	if !selected.config.Participant.configured.valid() {
		t.Fatal("caller changed detached startup intent")
	}
	for _, bad := range []*ConfiguredTrust{nil, {}} {
		if _, err = p.WithConfiguredTrust(bad); err == nil {
			t.Fatal("missing desired intent accepted")
		}
	}
	if _, err = OpenCurrentAuthority(t.Context(), p, "resume", "https://admin.example"); err == nil {
		t.Fatal("enrolled profile opened without supplied desired intent")
	}
	k := &s2cParticipant{trust: f.trust, replayState: f.genesis.state}
	if k.configuredReadyLocked() {
		t.Fatal("enrolled profile used persisted configuration fallback")
	}
	old, _ := authorityTestFixture(t, 3)
	k.trust = old.trust
	if !k.configuredReadyLocked() {
		t.Fatal("independently enrolled historical profile changed semantics")
	}
}

func TestConfiguredOwnerSealsOnlyCertifiedNativeIntent(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			n, keys, desired := configuredTestNativeCluster(t, count)
			owners := map[uint32]*authorityOriginOwner{}
			for id := range n.nodes {
				owners[id] = configuredTestEndorser(t, n, keys, id)
			}
			owner := owners[1]
			k := n.nodes[1]
			s := configuredOwnerStatement(k)
			votes := configuredOwnerVotes(t, owners, s)
			original := k.replayState.projection.Image()
			before := k.p.count
			if count == 3 {
				if _, err := owner.sealConfigured(t.Context(), s, votes[:1]); err == nil || k.p.count != before {
					t.Fatal("minority configuration changed native state", err)
				}
			}
			digest, err := owner.sealConfigured(t.Context(), s, votes)
			if err != nil || digest == [32]byte{} || k.originSerial != 1 || k.p.count != before+2 || len(k.pending) != 1 || !reflect.DeepEqual(original, k.replayState.projection.Image()) {
				t.Fatal("native sealing skipped reservation or changed projection", err)
			}
			retained := k.pending[digest]
			facade := &CurrentAuthority{origin: owner}
			if _, err := facade.resultLocked(retained.handoff.id, retained.handoff.operation.digest); !errors.Is(err, ErrPermissionDenied) {
				t.Fatal("configuration entered public original status surface", err)
			}
			if _, err := owner.carryOriginal(t.Context(), []byte(retained.raw)); !errors.Is(err, ErrPermissionDenied) {
				t.Fatal("ordinary original recovery admitted configuration class", err)
			}
			query, _ := json.Marshal(currentOriginalQuery{retained.handoff.id, retained.handoff.operation.digest})
			if response, err := owner.network.originalResponse(t.Context(), query); err != nil || len(response) != 0 {
				t.Fatal("ordinary original query disclosed configuration class", err)
			}
			replay, err := owner.sealConfigured(t.Context(), s, votes)
			if err != nil || replay != digest || k.p.count != before+2 {
				t.Fatal("pending retry did not retain exact native original", err)
			}
			quorum := []uint32{1}
			if count == 3 {
				quorum = append(quorum, 2)
			}
			n.selectValue(1, digest, quorum...)
			n.choose(1, quorum...)
			configuredOwnerConverge(t, n, 1)
			for _, node := range n.nodes {
				if !node.configuredReadyLocked() || node.replayState.projection.Image().ConfiguredTrustDigest != hex.EncodeToString(desired.digest[:]) || len(node.pending) != 0 || len(node.replayState.ledger) != 0 {
					t.Fatal("certified config failed to install clean trust-only projection")
				}
			}
			n.restart(1)
			if !n.nodes[1].configuredReadyLocked() {
				t.Fatal("intact replay lost certified desired digest")
			}
		})
	}
}

func TestConfiguredOwnerRenewalRequiresLocallySuppliedInstalledIntent(t *testing.T) {
	n, keys, desired := configuredTestNativeCluster(t, 3)
	owners := map[uint32]*authorityOriginOwner{}
	for id := range n.nodes {
		owners[id] = configuredTestEndorser(t, n, keys, id)
	}
	s := configuredOwnerStatement(n.nodes[1])
	votes := configuredOwnerVotes(t, owners, s)
	digest, err := owners[1].sealConfigured(t.Context(), s, votes)
	if err != nil {
		t.Fatal(err)
	}
	n.selectValue(1, digest, 1, 2)
	n.choose(1, 1, 2)
	configuredOwnerConverge(t, n, 1)
	clock, _ := fakeAuthorityTimeOwnerAt(t, configuredTestUTC)
	receiver := &authorityRenewalReceiver{kernel: n.nodes[1], clock: clock, workloads: [32]byte{9}}
	request, err := receiver.challenge()
	if err != nil {
		t.Fatal("installed matching input refused renewal", err)
	}
	for _, id := range []uint32{1, 2} {
		vote, err := n.nodes[id].signAuthorityRenewal(request, receiver.workloads, func() error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if err = receiver.receive(request, vote); err != nil {
			t.Fatal(err)
		}
	}
	nextIssuer := desired.issuers[0].clone()
	nextIssuer.ClientID += "-next"
	next, err := NewConfiguredTrust([]Issuer{nextIssuer})
	if err != nil {
		t.Fatal(err)
	}
	// Model a restarted majority with new operator input while the historical
	// projection is still A. Their signing gate cannot keep an A minority live.
	for _, id := range []uint32{2, 3} {
		c := n.configs[id]
		c.configured = next
		n.configs[id] = c
		n.restart(id)
	}
	for _, id := range []uint32{2, 3} {
		before := n.nodes[id].p.count
		if _, err = n.nodes[id].signAuthorityRenewal(request, receiver.workloads, func() error { return nil }); err == nil || n.nodes[id].p.count != before {
			t.Fatal("different-input voter renewed old public config", id, err)
		}
	}
	if _, err = n.nodes[1].signAuthorityRenewal(request, receiver.workloads, func() error { return nil }); err != nil {
		t.Fatal("matching minority's local vote unexpectedly refused", err)
	}
	n.nodes[1].config.configured = next
	n.nodes[1].gate.Lock()
	_, _, err = receiver.currentLocked()
	n.nodes[1].gate.Unlock()
	if err == nil {
		t.Fatal("old active public lease survived locally supplied mismatch")
	}
	if _, err = receiver.challenge(); err == nil {
		t.Fatal("mismatched input started public renewal challenge")
	}
}

func TestConfiguredOwnerMinorityPollingDoesNotConsumeNoopHistory(t *testing.T) {
	n, keys, desired := configuredTestNativeCluster(t, 3)
	owners := map[uint32]*authorityOriginOwner{}
	for id := range n.nodes {
		owners[id] = configuredTestEndorser(t, n, keys, id)
	}
	s := configuredOwnerStatement(n.nodes[1])
	digest, err := owners[1].sealConfigured(t.Context(), s, configuredOwnerVotes(t, owners, s))
	if err != nil {
		t.Fatal(err)
	}
	n.selectValue(1, digest, 1, 2)
	n.choose(1, 1, 2)
	configuredOwnerConverge(t, n, 1)
	issuer := desired.issuers[0].clone()
	issuer.ClientID += "-new"
	next, err := NewConfiguredTrust([]Issuer{issuer})
	if err != nil {
		t.Fatal(err)
	}
	c := n.configs[2]
	c.configured = next
	n.configs[2] = c
	n.restart(2)
	owners[2] = configuredTestEndorser(t, n, keys, 2)
	s = configuredOwnerStatement(n.nodes[2])
	if len(configuredOwnerVotes(t, owners, s)) != 1 {
		t.Fatal("minority startup input unexpectedly received majority endorsements")
	}
	var recovery configuredRecovery
	if !recovery.neededLocked(n.nodes[2]) {
		t.Fatal("initial unresolved suffix was not recoverable")
	}
	n.selectValue(2, [32]byte{}, 2, 3)
	n.choose(2, 2, 3)
	configuredOwnerConverge(t, n, 2)
	recovery.completedLocked(n.nodes[2])
	before := n.nodes[2].p.count
	for poll := 0; poll < 32; poll++ {
		s = configuredOwnerStatement(n.nodes[2])
		votes := configuredOwnerVotes(t, owners, s)
		if verifyConfiguredEndorsements(n.fixture.trust, s, votes) || recovery.neededLocked(n.nodes[2]) || n.nodes[2].configuredReadyLocked() {
			t.Fatal("minority poll started another empty recovery or became ready")
		}
	}
	if n.nodes[2].p.count != before {
		t.Fatal("minority polling consumed bounded native history")
	}
	// A later matching second voter can approve B at the current exact prefix,
	// without requiring another recovery NOOP or losing the old state.
	c = n.configs[3]
	c.configured = next
	n.configs[3] = c
	n.restart(3)
	owners[3] = configuredTestEndorser(t, n, keys, 3)
	s = configuredOwnerStatement(n.nodes[2])
	votes := configuredOwnerVotes(t, owners, s)
	digest, err = owners[2].sealConfigured(t.Context(), s, votes)
	if err != nil {
		t.Fatal("later matching quorum could not seal", err)
	}
	n.selectValue(2, digest, 2, 3)
	n.choose(2, 2, 3)
	configuredOwnerConverge(t, n, 2)
	if !n.nodes[2].configuredReadyLocked() || !n.nodes[3].configuredReadyLocked() || n.nodes[1].configuredReadyLocked() {
		t.Fatal("later matching quorum did not converge while old-input node stayed closed")
	}
}

func TestConfiguredOwnerCurrentLocalListenPreservesPinnedAuthority(t *testing.T) {
	options := provisioningTestOptions(t, 1, runtime.GOOS)
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	node := options.Nodes[0]
	for _, dir := range []string{node.StateDir, node.RenewableDir} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(node.RenewableDir, "control.json"), bundle.Operator.ControlManifest, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadCurrentProvisioningJSON(bundle.Nodes[0].AuthorityJSON)
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse(node.ControlOrigin)
	listen := "127.0.0.1:" + origin.Port()
	if err = ValidateProvisioningLocalListen(bundle.Nodes[0].AuthorityJSON, listen); err != nil {
		t.Fatal("same pinned port with supplied local host", err)
	}
	desired, _ := NewConfiguredTrust([]Issuer{})
	selected, err := p.WithLocalListen(listen)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = selected.WithConfiguredTrust(desired)
	if err != nil {
		t.Fatal(err)
	}
	if p.listen != node.ControlListen || p.localListen != "" || selected.listen != listen || selected.binding != p.binding || !reflect.DeepEqual(selected.jsonSource, p.jsonSource) || selected.profile != p.profile {
		t.Fatal("local selection rewrote original immutable authority")
	}
	reloaded, err := selected.reload()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.listen != listen || reloaded.localListen != listen || reloaded.config.Participant.configured.Digest() != desired.Digest() || reloaded.binding != p.binding || !reflect.DeepEqual(reloaded.jsonSource, p.jsonSource) {
		t.Fatal("custody reload discarded current input or rebased history")
	}
	for _, bad := range []string{"", ":0", ":16381", ":016380", "bad\n:16380", "relative", "127.0.0.1:-1"} {
		if ValidateProvisioningLocalListen(bundle.Nodes[0].AuthorityJSON, bad) == nil {
			t.Fatal("invalid or different fixed control port accepted", bad)
		}
		if _, err = p.WithLocalListen(bad); err == nil {
			t.Fatal("invalid local control bind selected", bad)
		}
	}
	for _, path := range []string{p.config.Participant.PPath, p.config.Participant.BPath, p.config.Membership.Path, p.floors} {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("bind selection opened native state", path, err)
		}
	}
}

// Real private TLS and native journals exercise the production scheduler here.
// UTC/elapsed facts are explicit deterministic fixtures; this is not configured
// external-source, provider, deployment or release qualification.
func TestConfiguredOwnerRunningSurvivorRecoversAcceptedTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	desiredA := configuredIntent(t)
	pauseRenewals, renewalsParked := make(chan struct{}), make(chan struct{})
	var parkOnce sync.Once
	beforeRenewal := func(id uint32, ownerCtx context.Context) {
		if id != 2 {
			<-ownerCtx.Done()
			return
		}
		select {
		case <-pauseRenewals:
			parkOnce.Do(func() { close(renewalsParked) })
			<-ownerCtx.Done()
		default:
		}
	}
	n, owners, ticks := authorityTestCompositeConfiguredMembersWithRenewal(t, 3, 2, nil, desiredA, true, beforeRenewal)
	var hideB atomic.Bool
	hook := func(source uint32) func(context.Context, uint32, []byte) error {
		return func(_ context.Context, target uint32, raw []byte) error {
			if !hideB.Load() || len(raw) <= len(s2cMessageMagic) {
				return nil
			}
			kind := raw[len(s2cMessageMagic)]
			if source == 1 && target == 2 && kind == s2cAccept || target == 1 && kind == s2cAccepted {
				return errS3AWire
			}
			return nil
		}
	}
	// Install once before the first consensus send. Only the atomic fault switch
	// changes while a running transport can observe these immutable hooks.
	for id, owner := range owners {
		owner.network.hooks.beforeSend = hook(id)
	}
	state := func(id uint32) (uint64, [32]byte, string, uint64, bool) {
		k := owners[id].network.kernel
		k.gate.Lock()
		defer k.gate.Unlock()
		accepted := [32]byte{}
		if k.accepted != nil {
			accepted = k.accepted.value
		}
		return k.replayState.slot, accepted, k.replayState.projection.snapshot.configuredTrustDigest, k.p.count, k.configuredReadyLocked()
	}
	wait := func(label string, predicate func() bool) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if predicate() {
				return
			}
			select {
			case <-ctx.Done():
				slot, accepted, digest, count, ready := state(2)
				t.Fatalf("%s: %v; survivor slot=%d accepted=%x digest=%s P=%d ready=%v", label, ctx.Err(), slot, accepted, digest, count, ready)
			case <-ticker.C:
			}
		}
	}
	statement := configuredOwnerStatement(owners[1].network.kernel)
	votes, err := owners[1].collectConfiguredEndorsements(ctx, statement)
	if err != nil {
		t.Fatal("initial native endorsements", err)
	}
	valueA, err := owners[1].sealConfigured(ctx, statement, votes)
	if err != nil {
		t.Fatal("initial native seal", err)
	}
	if _, err := owners[1].network.Drive(ctx, valueA); err != nil {
		t.Fatal("initial native configuration", err)
	}
	authorityTestConverge(t, owners)
	workerOwner := owners[2]
	done := make(chan struct{})
	workerOwner.network.workers.Add(1)
	go func() { defer close(done); workerOwner.configuredLoop() }()
	wait("initial matching worker must close its unresolved suffix", func() bool {
		for _, id := range []uint32{1, 2, 3} {
			slot, accepted, _, _, ready := state(id)
			if slot != 2 || accepted != [32]byte{} || !ready {
				return false
			}
		}
		return (&CurrentAuthority{origin: workerOwner}).Ready(ctx)
	})
	// The real ordinary renewal loop supplies eventual startup renewal. Park it
	// only after public readiness, and join its previous cycle before injecting
	// the later expiry fault. No test-issued renewal competes with startup.
	close(pauseRenewals)
	select {
	case <-renewalsParked:
	case <-done:
		t.Fatal("configured worker exited before ordinary renewals parked")
	case <-ctx.Done():
		t.Fatal("ordinary renewal cycle did not join before expiry phase", ctx.Err())
	}
	wait("parked fault phase requires no pending renewal challenge", func() bool {
		k := workerOwner.network.kernel
		k.gate.Lock()
		defer k.gate.Unlock()
		return workerOwner.network.receiver.pending == nil
	})
	issuerB := n.f.genesis.state.projection.Image().Issuers[0].clone()
	issuerB.URL = "https://later-configured.example"
	desiredB := configuredIntent(t, issuerB)
	restart := func(id uint32, desired *ConfiguredTrust) {
		t.Helper()
		previous := owners[id].network
		floors, err := previous.Floors()
		if err != nil {
			t.Fatal("retain native restart floors", id, err)
		}
		address := n.listeners[id].Addr().String()
		if err := previous.Close(); err != nil {
			t.Fatal("close native workload before restart", id, err)
		}
		c := n.configs[id]
		c.Participant.configured = desired
		c.hooks = &s3aHooks{beforeSend: hook(id), beforeRenewal: func(ctx context.Context) { <-ctx.Done() }}
		resumed, err := resumeS3AOwner(c, floors)
		if err != nil {
			t.Fatal("resume original native profile", id, err)
		}
		var originKey []byte
		if c.Participant.OwnedOrigin != 0 {
			originKey, err = loadAuthorityOriginKey(c, filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key"))
			if err != nil {
				_ = resumed.Close()
				t.Fatal("reload independently enrolled origin key", id, err)
			}
		}
		origin, err := attachAuthorityOrigin(resumed, originKey, "https://admin.example")
		if err != nil {
			_ = resumed.Close()
			t.Fatal("attach original origin/observer", id, err)
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			_ = resumed.Close()
			t.Fatal("rebind original private TLS address", id, err)
		}
		if err := resumed.Start(listener); err != nil {
			_ = listener.Close()
			_ = resumed.Close()
			t.Fatal("restart original private TLS listener", id, err)
		}
		n.configs[id], n.nodes[id], n.listeners[id], owners[id] = c, resumed, listener, origin
	}
	restart(1, desiredB)
	restart(3, desiredB)
	observer := owners[3]
	if observer.descriptor != (s2cOriginDescriptor{}) || len(observer.key) != 0 || observer.network.kernel.config.OwnedOrigin != 0 {
		t.Fatal("observer acquired a consume origin during restart")
	}
	member, _ := observer.network.kernel.trust.member(3)
	if member.Proposer {
		t.Fatal("observer unexpectedly acquired proposer authority")
	}
	statement = configuredOwnerStatement(owners[1].network.kernel)
	votes, err = owners[1].collectConfiguredEndorsements(ctx, statement)
	if err != nil || len(votes) != 2 || votes[0].Member != 1 || votes[1].Member != 3 {
		t.Fatal("changed intent did not require the matching proposer and origin-free fixed voter", err)
	}
	valueB, err := owners[1].sealConfigured(ctx, statement, votes)
	if err != nil {
		t.Fatal("seal majority-approved changed intent", err)
	}
	hideB.Store(true)
	if err := owners[1].network.Begin(ctx, valueB); err != nil {
		t.Fatal("begin hidden native acceptance", err)
	}
	wait("real TLS must retain B only on members one and three", func() bool {
		for _, id := range []uint32{1, 3} {
			slot, accepted, _, _, _ := state(id)
			if slot != 2 || accepted != valueB {
				return false
			}
		}
		slot, accepted, _, _, _ := state(2)
		return slot == 2 && accepted == [32]byte{}
	})
	if err := owners[1].network.Close(); err != nil {
		t.Fatal("crash original B proposer", err)
	}
	hideB.Store(false)
	// The original B consume was valid. Let the old A serving lease expire;
	// historical accepted proof must remain recoverable without refreshing it.
	for _, counter := range ticks {
		counter.Add(uint64(16 * time.Second))
	}
	workerOwner.network.kernel.gate.Lock()
	_, _, err = workerOwner.network.receiver.currentLocked()
	workerOwner.network.kernel.gate.Unlock()
	if err == nil {
		t.Fatal("old A lease survived deterministic elapsed expiry")
	}
	digestB := hex.EncodeToString(desiredB.digest[:])
	wait("already-running survivor must recover observer's authenticated accepted hint", func() bool {
		for _, id := range []uint32{2, 3} {
			slot, _, digest, _, _ := state(id)
			if slot < 3 || digest != digestB {
				return false
			}
		}
		return true
	})
	if _, _, _, _, ready := state(2); ready {
		t.Fatal("survivor publicly served recovered B while its supplied intent remained A")
	}
	if _, err := workerOwner.network.receiver.challenge(); err == nil {
		t.Fatal("mismatched survivor renewed recovered B")
	}
	// The observer restarts with A, preserving its non-proposer profile. Only
	// the survivor's production worker can now reconcile A at the new prefix.
	restart(3, desiredA)
	digestA := hex.EncodeToString(desiredA.digest[:])
	wait("surviving worker must reconcile its desired A after observer input changes", func() bool {
		for _, id := range []uint32{2, 3} {
			slot, _, digest, _, ready := state(id)
			if slot < 4 || digest != digestA || !ready {
				return false
			}
		}
		return true
	})
	if err := workerOwner.network.renewAuthority(ctx); err != nil {
		t.Fatal("reconciled matching quorum could not renew public authority", err)
	}
	workerOwner.network.kernel.gate.Lock()
	_, _, err = workerOwner.network.receiver.currentLocked()
	workerOwner.network.kernel.gate.Unlock()
	if err != nil {
		t.Fatal("matching reconciled public authority unavailable", err)
	}
	// A normal authorized management operation changes the semantic cut. It is
	// business work, not a manual recovery drive; the healthy background worker
	// must not interpret that cut change as another reason to append a NOOP.
	workerOwner.network.kernel.gate.Lock()
	reviewed := workerOwner.network.kernel.replayState.projection.cut
	workerOwner.network.kernel.gate.Unlock()
	request, err := workerOwner.prepare(ctx, authorityFakeCredentialProducer{}, reviewed, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal("prepare ordinary management at recovered current cut", err)
	}
	result, err := workerOwner.consume(ctx, request, authorityFakeCredentialProducer{}, [32]byte{})
	if err != nil {
		t.Fatal("consume ordinary management after config recovery", err)
	}
	if _, err := workerOwner.network.Drive(ctx, result.digest); err != nil {
		t.Fatal("certify ordinary management business work", err)
	}
	wait("ordinary management must converge without config repair", func() bool {
		for _, id := range []uint32{2, 3} {
			slot, _, digest, _, ready := state(id)
			if slot != 5 || digest != digestA || !ready {
				return false
			}
		}
		return true
	})
	type retained struct{ slot, records, revision uint64 }
	retainedState := func(id uint32) retained {
		k := owners[id].network.kernel
		k.gate.Lock()
		defer k.gate.Unlock()
		return retained{k.replayState.slot, k.p.count, k.replayState.projection.Image().ConfiguredTrustRevision}
	}
	before2, before3 := retainedState(2), retainedState(3)
	select {
	case <-done:
		t.Fatal("healthy configured worker exited after reaching matching input")
	case <-ctx.Done():
		t.Fatal("deadline before read-only idle observation", ctx.Err())
	case <-time.After(5500 * time.Millisecond):
	}
	after2, after3 := retainedState(2), retainedState(3)
	if after2 != before2 || after3 != before3 {
		t.Fatal("healthy idle polling appended native NOOP history", before2, after2, before3, after3)
	}
	select {
	case <-done:
		t.Fatal("healthy worker stopped during idle polling")
	default:
	}
}
