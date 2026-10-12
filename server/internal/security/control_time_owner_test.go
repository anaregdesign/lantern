package security

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestAuthorityTimeOwner(t *testing.T) {
	for _, mode := range []string{"success", "age equality", "delayed installation", "old sequence", "wrong source", "contradiction", "epoch change", "counter failure", "recovery after holdover"} {
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
			if mode == "delayed installation" {
				if err == nil || o.anchor != nil {
					t.Fatal("late installation refreshed source age")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			first, err := o.current()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "age equality":
				// Includes positive rate/read uncertainty, so exactly 60 counter
				// seconds must already have expired conservatively.
				ticks.Store(m.received.nanos + o.premises.maxAge)
				if _, err = o.current(); err == nil {
					t.Fatal("expired current sample")
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
				if _, err = o.current(); err == nil {
					t.Fatal("loss extended holdover")
				}
				m.sequence++
				m.sent.nanos = 60_900_000_000
				m.received.nanos = ticks.Load()
				m.packet.receive += 61 << 32
				m.packet.transmit += 61 << 32
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

func TestAuthorityTimeOwnerObservedSourceFault(t *testing.T) {
	for _, sourceErr := range []error{errAuthorityTimeSourceFault, errAuthorityTimeRate, errAuthorityTimeDenied} {
		t.Run(sourceErr.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			o := &authorityTimeOwner{producer: &authorityTimeProducer{}, anchor: &authorityTimeEstimate{}, cancel: cancel, done: make(chan struct{})}
			go o.run(ctx, func(context.Context) (authorityTimeMeasurement, error) { return authorityTimeMeasurement{}, sourceErr })
			defer o.close()
			deadline := time.Now().Add(time.Second)
			for {
				o.mu.Lock()
				cleared, failed := o.anchor == nil, o.failed
				o.mu.Unlock()
				if cleared {
					if (sourceErr == errAuthorityTimeDenied) != failed {
						t.Fatal("incorrect permanent failure state")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("reported source fault retained old authority")
				}
				time.Sleep(time.Millisecond)
			}
			if _, err := o.current(); err == nil {
				t.Fatal("reported source fault still authorizes")
			}
		})
	}
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
	first, event, err := clock.currentObserved()
	if err != nil || calls != 1 || event.Reason != "ok" || event.Check == 0 || event.Native != first.stamp.nanos {
		t.Fatal("not same sample", calls, event.Reason, err)
	}
	ticks.Add(clock.premises.maxAge)
	_, expired, err := clock.currentObserved()
	if err != errAuthorityTime || !errors.Is(err, errAuthorityTime) || calls != 2 || expired.Reason != "age" || expired.AgeUpper < clock.premises.maxAge || !expired.HasAnchor || expired.Check == event.Check {
		t.Fatal("age classification changed decision", calls, expired.Reason, err)
	}
	// Diagnostics cannot clear or revive an expired anchor, and reading them
	// never calls the sampler again.
	events, drops := capture.Drain()
	if calls != 2 || drops != 0 || len(events) != 2 || events[1].Check != expired.Check || clock.anchor == nil || clock.failed {
		t.Fatal("trace changed state or sampled again")
	}
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
	go o.run(ctx, func(ctx context.Context) (authorityTimeMeasurement, error) {
		f.calls <- time.Now()
		select {
		case r := <-f.results:
			return r.m, r.err
		case <-ctx.Done():
			return authorityTimeMeasurement{}, ctx.Err()
		}
	})
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
	time.Sleep(16*time.Second - time.Nanosecond)
	synctest.Wait()
	f.noCall()
	var at time.Time
	select {
	case at = <-f.calls: // virtual time advances to the actual random timer
	case <-f.o.done:
		f.t.Fatal("qualified installation stopped owner")
	}
	if d := at.Sub(f.completed); d < 16*time.Second || d >= 20*time.Second {
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

func TestAuthorityTimeRecoveryLoopBudget(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		t.Run(fmt.Sprintf("qualified=%t", qualified), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTimeOwnerLoopFixture(t)
				first := time.Now()
				waits := []int{32, 64, 128, 256, 512, 1024, 1024}
				starts := []int{0, 32, 96, 224, 480, 992, 2016, 3040}
				if qualified {
					first = f.healthy()
					waits = []int{32, 32, 32, 256, 512, 1024, 1024}
					starts = []int{0, 32, 64, 96, 352, 864, 1888, 2912}
				}
				for i, seconds := range waits {
					f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
					if !qualified && i == 1 {
						time.Sleep(13 * time.Second) // 45-second startup window: only two calls
						synctest.Wait()
						f.noCall()
					}
					at := f.nextAfter(time.Duration(seconds) * time.Second)
					if at.Sub(first) != time.Duration(starts[i+1])*time.Second {
						t.Fatal("cumulative failure budget exceeded")
					}
				}
				// Only a fresh installation resets an exhausted budget and backoff.
				f.healthy()
				for range 3 {
					f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
					f.nextAfter(32 * time.Second)
				}
			})
		})
	}
}

func TestAuthorityTimeRecoveryLoopFaultPriority(t *testing.T) {
	for _, used := range []bool{false, true} {
		t.Run(fmt.Sprintf("rate_after_credit=%t", used), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTimeOwnerLoopFixture(t)
				f.healthy()
				if used {
					for range 2 {
						f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
						f.nextAfter(32 * time.Second)
					}
				}
				// Even wrapping cannot make RATE eligible for acceleration.
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{errAuthorityTimeRate})
				if f.o.anchor != nil {
					t.Fatal("RATE retained anchor")
				}
				f.nextAfter(1024 * time.Second)
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				f.nextAfter(1024 * time.Second)
				f.healthy()
				for range 2 {
					f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
					f.nextAfter(32 * time.Second)
				}
			})
		})
	}
	for _, fault := range []error{errAuthorityTime, errAuthorityTimeSourceFault, &net.OpError{Op: "native", Err: context.DeadlineExceeded}} {
		t.Run(fault.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTimeOwnerLoopFixture(t)
				f.healthy()
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				f.nextAfter(32 * time.Second)
				f.complete(authorityTimeMeasurement{}, fault)
				if (f.o.anchor == nil) != errors.Is(fault, errAuthorityTimeSourceFault) || f.o.sequence != 1 {
					t.Fatal("retry classification changed anchor handling")
				}
				f.nextAfter(64 * time.Second)
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				f.nextAfter(128 * time.Second)
			})
		})
	}
}

func TestAuthorityTimeRecoveryLoopInstallation(t *testing.T) {
	for _, mode := range []string{"stale", "source", "delayed", "qualification", "contradiction", "native", "deny"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTimeOwnerLoopFixture(t)
				f.healthy()
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				f.nextAfter(32 * time.Second)
				m := f.fresh()
				var err error
				switch mode {
				case "stale":
					m.sequence = 1
				case "source":
					m.source.host = "foreign.invalid"
				case "delayed":
					f.ticks.Add(61_000_000_000)
				case "qualification":
					m.packet.reportedRootDispersion = ^uint32(0)
				case "contradiction":
					m.packet.receive += 10 << 32
					m.packet.transmit += 10 << 32
				case "native":
					f.o.producer.sample = func() (authorityTimeStamp, error) { return authorityTimeStamp{}, errAuthorityTime }
				case "deny":
					err = errAuthorityTimeDenied
				}
				f.complete(m, err)
				if f.o.sequence != 1 {
					t.Fatal("unqualified measurement advanced generation")
				}
				terminal := mode == "contradiction" || mode == "native" || mode == "deny"
				if (f.o.anchor == nil) != (terminal || mode == "qualification") {
					t.Fatal("installation changed original anchor retention")
				}
				if terminal {
					select {
					case <-f.o.done:
					default:
						t.Fatal("terminal owner still running")
					}
					time.Sleep(24 * time.Hour)
					synctest.Wait()
					f.noCall()
					return
				}
				f.nextAfter(64 * time.Second)
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				f.nextAfter(128 * time.Second)
			})
		})
	}
}

func TestAuthorityTimeRecoveryLoopExpiryAndOwnership(t *testing.T) {
	t.Run("strict expiry independent of remaining credit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newTimeOwnerLoopFixture(t)
			f.healthy()
			for range 2 {
				f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				f.nextAfter(32 * time.Second)
			}
			// One acceleration credit remains. Native elapsed alone neither
			// schedules a flight nor refills it, even while a flight is blocked.
			f.ticks.Store(f.base.received.nanos + 59_939_997_999)
			if _, err := f.o.current(); err != nil {
				t.Fatal("one nanosecond before age equality refused", err)
			}
			f.ticks.Add(1)
			for range 4 {
				_, e, err := f.o.currentObserved()
				if err == nil || e.Reason != "age" || e.AgeUpper != 60_000_000_000 || f.o.sequence != 1 {
					t.Fatal("age equality accepted or changed generation")
				}
			}
			f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
			f.nextAfter(32 * time.Second)
			f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
			f.nextAfter(256 * time.Second) // expiry did not replenish credits
			f.healthy()
			if _, err := f.o.current(); err != nil || f.o.sequence != 2 {
				t.Fatal("fresh qualified measurement did not recover time")
			}
		})
	})
	t.Run("single flight and completion-relative wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newTimeOwnerLoopFixture(t)
			f.healthy()
			// The measurement barrier, not Sleep alone, prevents the owner
			// from making progress; no missed periodic ticks can accumulate.
			time.Sleep(2 * time.Hour)
			f.ticks.Add(uint64(2 * time.Hour))
			synctest.Wait()
			f.noCall()
			f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
			f.nextAfter(32 * time.Second)
		})
	})
	for _, phase := range []string{"flight", "accelerated wait"} {
		t.Run("cancel "+phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTimeOwnerLoopFixture(t)
				f.healthy()
				if phase == "accelerated wait" {
					f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
					f.nextAfter(32 * time.Second)
					f.complete(authorityTimeMeasurement{}, &authorityTimeTransportError{context.DeadlineExceeded})
				}
				f.o.close()
				time.Sleep(24 * time.Hour)
				synctest.Wait()
				f.noCall()
			})
		})
	}
}
