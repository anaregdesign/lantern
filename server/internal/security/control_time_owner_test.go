package security

import (
	"context"
	"crypto/sha256"
	"errors"

	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestAuthorityTimeOwner(t *testing.T) {
	for _, mode := range []string{"success", "long holdover", "delayed installation", "old sequence", "wrong source", "contradiction", "epoch change", "counter failure", "recovery after holdover"} {
		t.Run(mode, func(t *testing.T) {
			var ticks atomic.Uint64
			ticks.Store(100_000_000)
			var sampleFailure atomic.Bool
			source, _ := parseAuthorityTimeSource([]byte("server time.asia.apple.com"))
			p := &authorityTimeProducer{source: source, sample: func() (authorityTimeStamp, error) {
				if sampleFailure.Load() {
					return authorityTimeStamp{}, errAuthorityTime
				}
				return fakeAuthorityTimeStamp(ticks.Load()), nil
			}}
			o := &authorityTimeOwner{producer: p, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription))}
			nonce := [8]byte{1}
			raw := fakeAuthorityNTPResponse(nonce)
			packet, err := parseAuthorityNTP(raw[:], nonce)
			if err != nil {
				t.Fatal(err)
			}
			m := authorityTimeMeasurement{source: source, sent: fakeAuthorityTimeStamp(0), received: fakeAuthorityTimeStamp(ticks.Load()), packet: packet, sequence: 1}
			if mode == "delayed installation" {
				ticks.Store(61_000_000_000)
			}
			err = o.installLocked(m)
			if err != nil {
				t.Fatal(err)
			}
			first, err := o.current()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "long holdover":
				ticks.Store(m.received.nanos + uint64(time.Hour))
				if sample, err := o.current(); err != nil || sample.utc.high-sample.utc.low < uint64(7*time.Second) {
					t.Fatal("sound holdover lost", err)
				}
			case "old sequence":
				if err = o.installLocked(m); err == nil {
					t.Fatal("sequence replay")
				}
			case "wrong source":
				m.sequence++
				m.source.host = "other.example"
				if err = o.installLocked(m); err == nil {
					t.Fatal("source replacement")
				}
			case "contradiction":
				m.sequence++
				m.packet.receive += 10 << 32
				m.packet.transmit += 10 << 32
				if err = o.installLocked(m); err == nil || !o.failed {
					t.Fatal("contradictory live UTC silently recentered")
				}
				if _, err = o.current(); err == nil {
					t.Fatal("failed owner issued sample")
				}
			case "epoch change":
				m.sequence++
				m.sent.boot[0]++
				m.received.boot[0]++
				if err = o.installLocked(m); err == nil {
					t.Fatal("foreign boot anchor installed")
				}
			case "counter failure":
				sampleFailure.Store(true)
				if _, err = o.current(); err == nil || !o.failed {
					t.Fatal("native error did not stop owner")
				}
				sampleFailure.Store(false)
				if _, err = o.current(); err == nil {
					t.Fatal("failed sampler repaired by cached fact")
				}
			case "recovery after holdover":
				ticks.Store(61_000_000_000)
				if _, err = o.current(); err != nil {
					t.Fatal("sound holdover refused", err)
				}
				m.sequence++
				m.sent.nanos = 60_900_000_000
				m.received.nanos = ticks.Load()
				m.packet.receive += 60<<32 | uint64(9)*(1<<32)/10
				m.packet.transmit += 60<<32 | uint64(9)*(1<<32)/10
				if err = o.installLocked(m); err != nil {
					t.Fatal(err)
				}
				if _, err = o.current(); err != nil {
					t.Fatal("fresh exchange failed recovery", err)
				}
			default:
				ticks.Add(1_000_000_000)
				now, err := o.current()
				if err != nil {
					t.Fatal(err)
				}
				d, err := o.elapsed(first, now)
				if err != nil || d.low > 1_000_000_000 || d.high < 1_000_000_000 {
					t.Fatalf("elapsed: %+v %v", d, err)
				}
				foreign := first
				foreign.owner = &authorityTimeOwner{}
				if _, err = o.elapsed(foreign, now); err == nil {
					t.Fatal("foreign owner sample")
				}
			}
		})
	}
}

func TestAuthorityTimeOwnerStopsAndJoins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &authorityTimeProducer{}
	o := &authorityTimeOwner{producer: p, cancel: cancel, done: make(chan struct{})}
	entered := make(chan struct{})
	go o.run(ctx, func(ctx context.Context) (authorityTimeMeasurement, error) {
		close(entered)
		<-ctx.Done()
		return authorityTimeMeasurement{}, ctx.Err()
	})
	<-entered
	done := make(chan struct{})
	go func() { o.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not join measurement worker")
	}
	if _, err := o.current(); err == nil {
		t.Fatal("closed owner issued sample")
	}
	o.close()
}

// The actual run loop owns its real timers inside synctest. Native ticks are
// deliberately independent: timer progress never manufactures time evidence.
type timeOwnerLoopResult struct {
	m   authorityTimeMeasurement
	err error
}

type timeOwnerLoopFixture struct {
	t         *testing.T
	o         *authorityTimeOwner
	ticks     *atomic.Uint64
	calls     chan time.Time
	results   chan timeOwnerLoopResult
	completed time.Time
	base      authorityTimeMeasurement
}

func newTimeOwnerLoopFixture(t *testing.T) *timeOwnerLoopFixture {
	o, ticks := fakeAuthorityTimeOwner(t)
	base := *o.lastObservation
	o.anchor, o.lastObservation, o.sequence = nil, nil, 0
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel, o.done = cancel, make(chan struct{})
	f := &timeOwnerLoopFixture{t: t, o: o, ticks: ticks, calls: make(chan time.Time, 16), results: make(chan timeOwnerLoopResult), base: base}
	go func() {
		defer close(o.done)
		o.runLoopScheduled(ctx, func(ctx context.Context) (authorityTimeMeasurement, error) {
			f.calls <- time.Now()
			select {
			case r := <-f.results:
				return r.m, r.err
			case <-ctx.Done():
				return authorityTimeMeasurement{}, ctx.Err()
			}
		}, func() (uint64, error) { return 0, nil })
	}()
	synctest.Wait()
	f.takeCall()
	t.Cleanup(o.close)
	return f
}

func (f *timeOwnerLoopFixture) takeCall() time.Time {
	f.t.Helper()
	select {
	case at := <-f.calls:
		return at
	default:
		f.t.Fatal("expected exactly one measurement call")
		return time.Time{}
	}
}

func (f *timeOwnerLoopFixture) noCall() {
	f.t.Helper()
	select {
	case <-f.calls:
		f.t.Fatal("early or duplicate measurement")
	default:
	}
}

func (f *timeOwnerLoopFixture) complete(m authorityTimeMeasurement, err error) {
	f.t.Helper()
	f.results <- timeOwnerLoopResult{m, err}
	synctest.Wait()
	f.completed = time.Now()
}

func (f *timeOwnerLoopFixture) fresh() authorityTimeMeasurement {
	m := f.base
	f.o.mu.Lock()
	m.sequence = f.o.sequence + 1
	f.o.mu.Unlock()
	delta := f.ticks.Load() - f.base.received.nanos
	m.sent.nanos += delta
	m.received.nanos += delta
	ntpDelta := delta/1_000_000_000<<32 + delta%1_000_000_000*(1<<32)/1_000_000_000
	m.packet.reference += ntpDelta
	m.packet.receive += ntpDelta
	m.packet.transmit += ntpDelta
	return m
}

func (f *timeOwnerLoopFixture) healthy() time.Time {
	f.t.Helper()
	f.complete(f.fresh(), nil)
	time.Sleep(10*time.Minute - time.Nanosecond)
	synctest.Wait()
	f.noCall()
	var at time.Time
	select {
	case at = <-f.calls: // virtual time advances to the actual random timer
	case <-f.o.done:
		f.t.Fatal("qualified installation stopped owner")
	}
	if d := at.Sub(f.completed); d < 10*time.Minute || d > 41*time.Minute {
		f.t.Fatalf("healthy interval %v", d)
	}
	synctest.Wait()
	return at
}

func (f *timeOwnerLoopFixture) nextAfter(delay time.Duration) time.Time {
	f.t.Helper()
	target := f.completed.Add(delay)
	time.Sleep(time.Until(target) - time.Nanosecond)
	synctest.Wait()
	f.noCall()
	time.Sleep(time.Nanosecond)
	synctest.Wait()
	at := f.takeCall()
	if !at.Equal(target) {
		f.t.Fatalf("attempt at %v, expected %v", at, target)
	}
	f.noCall()
	return at
}

func TestAuthorityTimeDecisionTraceSameSample(t *testing.T) {
	capture := decisiontrace.Start(32)
	if capture == nil {
		t.Fatal("capture")
	}
	defer capture.Stop()
	clock, ticks := fakeAuthorityTimeOwner(t)
	original := clock.producer.sample
	calls := 0
	clock.producer.sample = func() (authorityTimeStamp, error) { calls++; return original() }
	first, e, err := clock.currentObserved()
	if err != nil || calls != 1 || e.Native != first.stamp.nanos {
		t.Fatal("not same sample", err)
	}
	ticks.Add(uint64(time.Hour))
	later, event, err := clock.currentObserved()
	if err != nil || calls != 2 || event.AgeUpper < uint64(time.Hour) || event.Width < uint64(7*time.Second) || event.Check == e.Check || later.sequence != first.sequence {
		t.Fatal("holdover trace changed evidence", err)
	}
	events, drops := capture.Drain()
	if calls != 2 || drops != 0 || len(events) != 1 || clock.anchor == nil || clock.failed {
		t.Fatal("trace changed state")
	}
}

func TestAuthorityTimeRecoveryLoopBudget(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		t.Run(map[bool]string{false: "bootstrap", true: "holdover"}[qualified], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTimeOwnerLoopFixture(t)
				waits := []int{32, 64, 128, 256, 512, 900, 900}
				if qualified {
					f.healthy()
					waits = []int{60, 120, 240, 480, 900, 900}
				}
				for _, seconds := range waits {
					f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
					f.nextAfter(time.Duration(seconds) * time.Second)
				}
				f.healthy()
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				f.nextAfter(time.Minute)
			})
		})
	}
}

func TestAuthorityTimeRecoveryLoopFaultPriority(t *testing.T) {
	for _, fault := range []error{errAuthorityTimeRate, errAuthorityTimeDenied, errAuthorityTimeSourceFault, errAuthorityTime} {
		t.Run(fault.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTimeOwnerLoopFixture(t)
				f.healthy()
				f.complete(authorityTimeMeasurement{}, fault)
				if (f.o.anchor == nil) != errors.Is(fault, errAuthorityTimeSourceFault) || f.o.sequence != 1 || f.o.failed {
					t.Fatal("fault changed evidence incorrectly")
				}
				if fault == errAuthorityTimeDenied {
					select {
					case <-f.o.done:
					default:
						t.Fatal("denied association kept querying")
					}
					time.Sleep(24 * time.Hour)
					synctest.Wait()
					f.noCall()
					if _, err := f.o.current(); err != nil {
						t.Fatal("denial erased sound holdover", err)
					}
					return
				}
				delay := time.Minute
				if fault == errAuthorityTimeRate {
					delay = 1024 * time.Second
				}
				f.nextAfter(delay)
			})
		})
	}
}

func TestAuthorityTimeRecoveryLoopExpiryAndOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTimeOwnerLoopFixture(t)
		f.healthy()
		// Timer passage cannot manufacture native elapsed evidence or parallel flights.
		time.Sleep(2 * time.Hour)
		f.ticks.Add(uint64(2 * time.Hour))
		synctest.Wait()
		f.noCall()
		for range 4 {
			if _, err := f.o.current(); err != nil || f.o.sequence != 1 {
				t.Fatal("sound holdover lost", err)
			}
		}
		f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
		f.nextAfter(time.Minute)
		f.o.close()
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		f.noCall()
	})
}

func TestAuthorityTimeRejectedCandidatePreservesAnchor(t *testing.T) {
	for _, fault := range []string{"bound", "source", "sequence", "native", "contradiction"} {
		t.Run(fault, func(t *testing.T) {
			o, ticks := fakeAuthorityTimeOwner(t)
			old := o.anchor
			m := *o.lastObservation
			m.sequence++
			switch fault {
			case "bound":
				m.packet.reportedRootDispersion = ^uint32(0)
			case "source":
				m.source.host = "foreign.invalid"
			case "sequence":
				m.sequence--
			case "native":
				m.received.boot[0]++
			case "contradiction":
				m.packet.transmit += 10 << 32
				m.packet.receive += 10 << 32
			}
			if err := o.installLocked(m); err == nil {
				t.Fatal("invalid candidate accepted")
			}
			terminal := fault == "native" || fault == "contradiction"
			if o.failed != terminal || (!terminal && o.anchor != old) || o.sequence != 1 {
				t.Fatal("candidate changed retained evidence")
			}
			if !terminal {
				ticks.Add(uint64(time.Hour))
				if _, err := o.current(); err != nil {
					t.Fatal("old anchor unavailable", err)
				}
			}
		})
	}
}

func TestAuthorityTimeRecoveryLoopNativeFaultStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTimeOwnerLoopFixture(t)
		f.healthy()
		f.complete(authorityTimeMeasurement{}, errAuthorityTimeNative)
		if !f.o.failed || f.o.anchor != nil {
			t.Fatal("native acquisition fault kept authority")
		}
		select {
		case <-f.o.done:
		default:
			t.Fatal("native fault kept acquisition running")
		}
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		f.noCall()
	})
}
