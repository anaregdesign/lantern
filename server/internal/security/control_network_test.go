package security

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func TestS3AOwnerExplicitResumeFamiliesAndFloors(t *testing.T) {
	for _, damage := range []string{"M-missing", "P-missing", "B-missing", "P-tip-missing", "B-tip-missing", "M-corrupt", "P-corrupt", "B-corrupt", "incarnation", "epoch", "M-rollback", "P-floor", "B-floor"} {
		t.Run(damage, func(t *testing.T) {
			n := s3aTestCluster(t, nil)
			o := n.start(1)
			c := n.configs[1]
			floors, err := o.Floors()
			if err != nil {
				t.Fatal(err)
			}
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "M-missing":
				err = os.Remove(c.Membership.Path)
			case "P-missing":
				err = os.Remove(c.Participant.PPath)
			case "B-missing":
				err = os.Remove(c.Participant.BPath)
			case "P-tip-missing":
				err = os.Remove(c.Participant.PPath + ".tip")
			case "B-tip-missing":
				err = os.Remove(c.Participant.BPath + ".tip")
			case "M-corrupt", "P-corrupt", "B-corrupt":
				path := map[string]string{"M-corrupt": c.Membership.Path, "P-corrupt": c.Participant.PPath, "B-corrupt": c.Participant.BPath}[damage]
				err = os.WriteFile(path, []byte("partial unauthenticated family"), 0600)
			case "incarnation":
				c.Participant.Incarnation[0]++
			case "epoch":
				c.Participant.PEpoch[0]++
			case "M-rollback":
				floors.M.Version++
			case "P-floor":
				floors.P.Index++
			case "B-floor":
				floors.B.LocalIndex++
			}
			if err != nil {
				t.Fatal(err)
			}
			if next, err := resumeS3AOwner(c, floors); err == nil {
				_ = next.Close()
				t.Fatal("unqualified resume")
			}
			if next, err := createS3AOwner(c); err == nil {
				_ = next.Close()
				t.Fatal("fresh fallback")
			}
		})
	}
}

func TestS3AOwnerMembershipRefreshAndDurableFence(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o := n.start(1)
	c := n.configs[1]
	foreign := n.manifest
	foreign.Version++
	foreign.Profile.Lineage.Instance[0]++
	if o.Refresh(n.sign(foreign)) == nil || o.check(t.Context()) != nil {
		t.Fatal("foreign lineage fenced owner")
	}
	m := n.manifest
	m.Version++
	if err := o.Refresh(n.sign(m)); err != nil {
		t.Fatal(err)
	}
	floors, err := o.Floors()
	if err != nil || floors.M.Version != 2 {
		t.Fatal(floors, err)
	}
	m.Version++
	m.Profile.TrustDigest[0]++
	if o.Refresh(n.sign(m)) == nil || o.Begin(t.Context(), [32]byte{}) == nil {
		t.Fatal("binding change admitted")
	}
	fence, err := o.MembershipFloor()
	if err != nil || fence.Version != 3 {
		t.Fatal("terminal M receipt unavailable for external retention", fence, err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if next, err := resumeS3AOwner(c, s3aFloors{}); err == nil {
		_ = next.Close()
		t.Fatal("persisted binding fence forgotten")
	}
}

func TestS3AOwnerCloseRacesAndReleasesLeases(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o, err := createS3AOwner(n.configs[1])
	if err != nil {
		t.Fatal(err)
	}
	n.nodes[1] = o
	floors, err := o.Floors()
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Go(func() { _ = o.Begin(context.Background(), [32]byte{}); _ = o.Close() })
	}
	group.Go(func() { _ = o.Start(n.listeners[1]) })
	group.Wait()
	if o.queued != 0 || len(o.kernel.key) != 0 || o.identity.signer.key != nil {
		t.Fatal("owned resources retained after close")
	}
	next, err := resumeS3AOwner(n.configs[1], floors)
	if err != nil {
		t.Fatal("leases escaped close", err)
	}
	_ = next.Close()
}

func TestS3AOwnerKnownNativeRollbackWithIndependentFloors(t *testing.T) {
	for _, family := range []string{"P-with-unchanged-B", "B", "M"} {
		t.Run(family, func(t *testing.T) {
			n := s3aTestCluster(t, nil)
			n.startAll()
			o := n.nodes[2]
			c := n.configs[2]
			old := s2cGateSnapshot(t, c.Participant)
			oldM, err := os.ReadFile(c.Membership.Path)
			if err != nil {
				t.Fatal(err)
			}
			initial, err := o.Floors()
			if err != nil {
				t.Fatal(err)
			}
			switch family {
			case "P-with-unchanged-B":
				m, err := s2cSignPrepare(n.f.trust, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
				if err != nil {
					t.Fatal(err)
				}
				if err := n.nodes[1].send(t.Context(), 2, []byte(m.raw)); err != nil {
					t.Fatal(err)
				}
			case "B":
				if _, err := n.nodes[1].Drive(s3aTestContext(t), [32]byte{}); err != nil {
					t.Fatal(err)
				}
				n.waitCut(2, 1)
			case "M":
				m := n.manifest
				m.Version++
				if err := o.Refresh(n.sign(m)); err != nil {
					t.Fatal(err)
				}
			}
			floors, err := o.Floors()
			if err != nil {
				t.Fatal(err)
			}
			if family == "P-with-unchanged-B" && (floors.B != initial.B || floors.P.Index <= initial.P.Index) {
				t.Fatal("fixture did not isolate P", initial, floors)
			}
			_ = o.Close()
			switch family {
			case "P-with-unchanged-B":
				s2cGateRestore(t, old, c.Participant.PPath, c.Participant.PPath+".tip")
			case "B":
				s2cGateRestore(t, old, c.Participant.BPath, c.Participant.BPath+".tip")
			case "M":
				if err := os.WriteFile(c.Membership.Path, oldM, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if next, err := resumeS3AOwner(c, floors); err == nil {
				_ = next.Close()
				t.Fatal("known rollback accepted")
			}
		})
	}
}

func TestS3AOwnerCloseDrainsAdmittedHTTPBeforeReleasingState(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
		if id == 2 {
			c.hooks = &s3aHooks{beforeResponse: func(context.Context) { once.Do(func() { close(entered) }); <-release }}
		}
	})
	n.start(1)
	n.start(2)
	m, err := s2cSignPrepare(n.f.trust, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- n.nodes[1].send(t.Context(), 2, []byte(m.raw)) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP not entered")
	}
	closed := make(chan error, 1)
	go func() { closed <- n.nodes[2].Close() }()
	select {
	case <-closed:
		t.Fatal("Close escaped active handler")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("closed owner acknowledged work")
	}
	resumed, err := resumeS3AOwner(n.configs[2], s3aFloors{})
	if err != nil {
		t.Fatal("intact zero-witness resume", err)
	}
	_ = resumed.Close()
}

func TestS3AOwnerRejectsUnrepresentableLimitsBeforeState(t *testing.T) {
	n := s3aTestCluster(t, nil)
	for _, kind := range []string{"nil-trust", "empty-voters", "overflow-bounds", "outbox", "queue", "peer-count", "transition", "range-slots", "range-bytes", "connections", "retry", "rounds", "rpc-time", "backoff", "ballot", "window"} {
		t.Run(kind, func(t *testing.T) {
			c := n.configs[1]
			switch kind {
			case "nil-trust":
				c.Participant.Trust = nil
			case "empty-voters":
				v := *c.Participant.Trust
				v.members = nil
				c.Participant.Trust = &v
			case "overflow-bounds":
				v := *c.Participant.Trust
				v.bounds.ProofBytes = ^uint64(0)
				c.Participant.Trust = &v
			case "outbox":
				c.Participant.OutboxBytes = 1
			case "queue":
				c.Limits.QueueBytes = ^uint64(0)
			case "peer-count":
				c.Limits.PerPeerQueue = 33
			case "transition":
				c.Limits.TransitionCount = 3
			case "range-slots":
				c.Limits.RangeSlots = ^uint64(0)
			case "range-bytes":
				c.Limits.RangeBytes = 1
			case "connections":
				c.Limits.MaxConnections = 2
			case "retry":
				c.Limits.Attempts = 17
			case "rounds":
				c.Limits.Rounds = 65
			case "rpc-time":
				c.Limits.RPCTimeout = -time.Second
			case "backoff":
				c.Limits.Backoff = 0
			case "ballot":
				c.Limits.BallotBackoff = time.Millisecond
			case "window":
				c.Limits.ScheduleWindow = 6 * time.Minute
			}
			if o, err := createS3AOwner(c); err == nil {
				_ = o.Close()
				t.Fatal("unbounded configuration admitted")
			}
			for _, path := range []string{c.Membership.Path, c.Participant.PPath, c.Participant.BPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("invalid limits created state", path, err)
				}
			}
		})
	}
}

// Independent finite probe: the inherited kernel attempts its owned native
// shutdown, finishes P/B cleanup, and re-panics. The outer M/TLS owner must
// still complete cleanup, including on a repeated Close.
func TestS3AIndependentOwnerClosePanicMustReleaseMAndSigner(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o, err := createS3AOwner(n.configs[1])
	if err != nil {
		t.Fatal(err)
	}
	n.nodes[1] = o
	// Fallback cleanup keeps a regression failure from stranding the
	// real lease/key in this test process. Assertions precede this fallback.
	defer func() {
		_ = o.membership.Close()
		o.identity.clear()
	}()
	o.kernel.b.walOwner = s2cPanicCloser{o.kernel.b.walOwner}
	var recovered any
	var closeErr error
	func() {
		defer func() { recovered = recover() }()
		closeErr = o.Close()
	}()
	if recovered == nil && closeErr == nil {
		t.Fatal("owned native closer fault was silently lost")
	}
	if err := o.Close(); !errors.Is(err, errS3ACleanup) {
		t.Errorf("repeated Close lost the recorded cleanup failure: %v", err)
	}
	t.Logf("close_panic=%v close_error=%v owner_closed=%v kernel_key_bytes=%d tls_signer_retained=%v",
		recovered, closeErr, o.closed.Load(), len(o.kernel.key), o.identity.signer.key != nil)
	if o.identity.signer.key != nil {
		t.Error("SAFETY: terminal outer Close retained the TLS private signer")
	}
	for _, path := range []string{n.configs[1].Membership.Path, n.configs[1].Participant.PPath, n.configs[1].Participant.BPath} {
		lease, err := mutationlog.AcquireFileWALLease(path)
		if err != nil {
			t.Errorf("SAFETY: terminal Close stranded ownership at %s: %v", path, err)
			continue
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestS3AOwnerRefreshPanicStopsScheduling(t *testing.T) {
	var armed atomic.Bool
	failure := errors.New("configured M clock callback panic")
	n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
		if id == 1 {
			now := c.Membership.Now
			c.Membership.Now = func() time.Time {
				if armed.CompareAndSwap(true, false) {
					panic(failure)
				}
				return now()
			}
		}
	})
	o, err := createS3AOwner(n.configs[1])
	if err != nil {
		t.Fatal(err)
	}
	n.nodes[1] = o
	// Stop otherwise idle workers before arming the configured callback; only
	// the synchronous Refresh owns this panic. Persistence-panic cutpoints are
	// separately exercised inside peerauth without exporting its private hook.
	o.cancel()
	o.workers.Wait()
	m := n.manifest
	m.Version++
	armed.Store(true)
	var recovered any
	func() { defer func() { recovered = recover() }(); _ = o.Refresh(n.sign(m)) }()
	if recovered != failure {
		t.Fatalf("original M panic lost: %v", recovered)
	}
	if !o.closed.Load() || o.ctx.Err() == nil {
		t.Fatal("Refresh panic left scheduling admitted")
	}
	if err := o.Begin(t.Context(), [32]byte{}); !errors.Is(err, errS3AClosed) {
		t.Fatal("Begin remained open", err)
	}
	if _, err := o.Floors(); !errors.Is(err, errS3AClosed) {
		t.Fatal("floors remained open", err)
	}
	if _, err := o.CatchUp(t.Context(), 2); !errors.Is(err, errS3AClosed) {
		t.Fatal("CatchUp remained open", err)
	}
	if _, err := o.Drive(t.Context(), [32]byte{}); !errors.Is(err, errS3AClosed) {
		t.Fatal("Drive remained open", err)
	}
	if err := o.Start(n.listeners[1]); !errors.Is(err, errS3AClosed) {
		t.Fatal("listener remained open", err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
}

type s3aErrorCloser struct {
	io.Closer
	failure error
}

func (c s3aErrorCloser) Close() error { return errors.Join(c.Closer.Close(), c.failure) }

func TestS3AOwnerCloseRetainsCombinedErrors(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o, err := createS3AOwner(n.configs[1])
	if err != nil {
		t.Fatal(err)
	}
	n.nodes[1] = o
	failure := errors.New("native close failure combined with closed sentinel")
	o.kernel.b.walOwner = s3aErrorCloser{o.kernel.b.walOwner, errors.Join(net.ErrClosed, failure)}
	for i := 0; i < 2; i++ {
		if err := o.Close(); !errors.Is(err, failure) {
			t.Errorf("Close %d lost combined failure: %v", i, err)
		}
	}
	if o.identity.signer.key != nil || len(o.kernel.key) != 0 {
		t.Fatal("failed close retained a signing key")
	}
	for _, path := range []string{n.configs[1].Membership.Path, n.configs[1].Participant.PPath, n.configs[1].Participant.BPath} {
		lease, err := mutationlog.AcquireFileWALLease(path)
		if err != nil {
			t.Fatal("failed close retained native ownership", path, err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestS3AOwnerTerminalFloorWaitsForAdmittedProducer(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o := n.start(1)
	// Model an operation already inside enterCall, with its authenticated
	// membership write still pending. Closing must not read a floor ahead of it.
	if !o.enterCall() {
		t.Fatal("producer not admitted")
	}
	result := make(chan s3aFloors, 1)
	errors := make(chan error, 1)
	go func() {
		f, err := o.finish(true)
		result <- f
		errors <- err
	}()
	deadline := time.After(3 * time.Second)
	for !o.closed.Load() {
		select {
		case <-deadline:
			t.Fatal("terminal admission did not close")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-result:
		t.Fatal("terminal floor preceded the admitted producer")
	default:
	}
	manifest := n.manifest
	manifest.Version++
	if err := o.membership.Apply(n.sign(manifest)); err != nil {
		o.calls.Done()
		t.Fatal(err)
	}
	o.calls.Done()
	select {
	case f := <-result:
		if err := <-errors; err != nil || f.M.Version != manifest.Version {
			t.Fatal("final floor missed the last joined write", f.M.Version, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal owner did not join")
	}
	if o.enterCall() {
		o.calls.Done()
		t.Fatal("terminal floor reopened ordinary admission")
	}
}
