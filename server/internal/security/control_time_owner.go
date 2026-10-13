package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

// This fixed profile is conditional on honest conservative source reporting,
// an intact DNS/UDP time path and a host counter within the admitted envelope.
// It does not assert authenticated NTP or vendor-certified oscillator bounds.
const authorityTimeProfileDescription = "lantern/current-authority/time/v1;" + authorityTimePlatformProfile + ";rate=1000ppm;sample=1000ns;source-host=time.asia.apple.com;source=root-dispersion+nonnegative-root-delay/2+precision+10ms;source-max=1s;rtt-max-exclusive=1s;age-max-exclusive=60s;width-max-exclusive=4s;UTC=[2020,2080);trusted-source-and-path;no-NTP-authentication"

func authorityOperationalTimePremises() authorityTimePremises {
	return authorityTimePremises{
		ratePPB: 1_000_000, stampError: 1000, sourceAllowance: 10_000_000,
		maxSourceError: 1_000_000_000, maxRTT: 1_000_000_000,
		maxAge: 60_000_000_000, maxWidth: 4_000_000_000,
		validUTC: authorityTimeRange{1_577_836_800_000_000_000, 3_471_292_800_000_000_000},
	}
}

// Process-owned and non-serializable. A production caller cannot mint one from
// a wall time, a serialized transcript or an asserted qualification boolean.
type authorityCurrentTime struct {
	owner    *authorityTimeOwner
	stamp    authorityTimeStamp
	utc      authorityTimeRange
	profile  [32]byte
	sequence uint64
}

type authorityTimeOwner struct {
	mu              sync.Mutex
	traceID         uint64
	installReason   string
	traceState      decisiontrace.Transition
	producer        *authorityTimeProducer
	premises        authorityTimePremises
	profile         [32]byte
	anchor          *authorityTimeEstimate
	sequence        uint64
	failed, closed  bool
	lastObservation *authorityTimeMeasurement
	lastError       error // bounded latest source/qualification failure, for startup diagnosis
	cancel          context.CancelFunc
	done            chan struct{}
}

func startAuthorityTimeOwner(producer *authorityTimeProducer) *authorityTimeOwner {
	ctx, cancel := context.WithCancel(context.Background())
	o := &authorityTimeOwner{traceID: decisiontrace.NextID(), producer: producer, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription)), cancel: cancel, done: make(chan struct{})}
	go o.run(ctx, producer.measure)
	return o
}

// Only a qualified installation replenishes this process-local budget. The
// exponential state keeps advancing even when one of two waits is shortened.
// Timers/credits are scheduling inputs only, never evidence of current time.
type authorityTimeRetry struct {
	backoff time.Duration
	credits uint8
}

func (s *authorityTimeRetry) delay(err error, refresh time.Duration) time.Duration {
	if err == nil {
		s.backoff, s.credits = 0, 2
		return refresh
	}
	if s.backoff == 0 {
		s.backoff = 32 * time.Second
	} else {
		s.backoff = min(s.backoff*2, 1024*time.Second)
	}
	if errors.Is(err, errAuthorityTimeRate) {
		s.backoff, s.credits = 1024*time.Second, 0
		return s.backoff
	}
	var transport *authorityTimeTransportError
	if !errors.As(err, &transport) {
		s.credits = 0
	} else if s.credits > 0 && (s.backoff == 64*time.Second || s.backoff == 128*time.Second) {
		s.credits--
		return 32 * time.Second
	}
	return s.backoff
}

// Timers schedule work only. Authorization expiry uses native elapsed bounds.
// There is never a backlog or catch-up burst after a scheduler pause.
func (o *authorityTimeOwner) run(ctx context.Context, measure func(context.Context) (authorityTimeMeasurement, error)) {
	defer close(o.done)
	o.runLoop(ctx, measure)
}

// The test-build composition owns additional UDP responder workers and joins
// them before closing done. Both compositions execute this unchanged loop.
func (o *authorityTimeOwner) runLoop(ctx context.Context, measure func(context.Context) (authorityTimeMeasurement, error)) {
	var retry authorityTimeRetry
	for {
		m, err := measure(ctx)
		probe := o.producer.probeObservation()
		o.mu.Lock()
		if o.closed || o.failed || ctx.Err() != nil {
			o.mu.Unlock()
			return
		}
		if err == nil {
			err = o.installLocked(m)
			probe.Stage = o.installReason
		}
		o.lastError = err
		if errors.Is(err, errAuthorityTimeDenied) {
			o.failed = true
			o.anchor = nil
		}
		if errors.Is(err, errAuthorityTimeSourceFault) || errors.Is(err, errAuthorityTimeRate) {
			o.anchor = nil
		}
		failed := o.failed
		generation, hasAnchor := o.sequence, o.anchor != nil
		probe.Kind, probe.Object, probe.Anchor, probe.HasAnchor = "time_probe", o.traceID, generation, hasAnchor
		if err == nil {
			probe.Reason = "installed"
		} else if probe.Reason == "ok" {
			probe.Reason = "install_rejected"
		}
		o.mu.Unlock()
		if failed {
			probe.Reason = "native_terminal"
			decisiontrace.Emit(probe)
			return
		}
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			o.fail()
			return
		}
		refresh := 16*time.Second + time.Duration(binary.BigEndian.Uint64(random[:])%uint64(4*time.Second))
		delay := retry.delay(err, refresh)
		probe.Delay = int64(delay)
		decisiontrace.Emit(probe)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Called only under the owner gate. Sequence, source and sample bindings are
// verified before publication. A new interval cannot silently recenter a live
// contradictory anchor. Expired anchors never supply authority during repair.
func (o *authorityTimeOwner) installLocked(m authorityTimeMeasurement) error {
	o.installReason = "binding"
	if o.closed || o.failed || m.source != o.producer.source || m.sequence <= o.sequence {
		return errAuthorityTime
	}
	a, reason, err := calculateAuthorityTimeObserved(m, o.premises)
	o.installReason = reason
	if err != nil {
		o.anchor = nil
		return errAuthorityTimeSourceFault
	}
	o.installReason = "native_install"
	now, err := o.producer.sample()
	if err != nil {
		o.failed = true
		o.anchor = nil
		return err
	}
	if _, check, err := a.atSampleObserved(now); err != nil {
		o.installReason = check.Reason
		return err
	}
	if o.anchor != nil {
		old, err := o.anchor.atSample(m.received)
		if err == nil && (old.high < a.utc.low || a.utc.high < old.low) {
			o.failed = true
			o.anchor = nil
			o.installReason = "contradiction"
			return errAuthorityTime
		}
	}
	o.anchor = &a
	o.lastObservation = &m
	o.sequence = m.sequence
	o.installReason = "installed"
	return nil
}

func (o *authorityTimeOwner) current() (authorityCurrentTime, error) {
	now, _, err := o.currentObserved()
	return now, err
}
func (o *authorityTimeOwner) currentObserved() (result authorityCurrentTime, event decisiontrace.Event, resultErr error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	event = decisiontrace.Event{Kind: "time_current", Object: o.traceID, Check: decisiontrace.NextID(), Anchor: o.sequence, HasAnchor: o.anchor != nil}
	defer func() {
		event.Kind, event.Object, event.Anchor = "time_current", o.traceID, o.sequence
		event.NativeReason = event.Reason
		if decisiontrace.Enabled() {
			o.traceState.Emit(fmt.Sprintf("%s/%d", event.Reason, event.Anchor), event)
		}
	}()
	if o.closed {
		event.Reason = "closed"
		return result, event, errAuthorityTime
	}
	if o.failed {
		event.Reason = "native_terminal"
		return result, event, errAuthorityTime
	}
	if o.anchor == nil {
		event.Reason = "no_anchor"
		return result, event, errAuthorityTime
	}
	now, err := o.producer.sample()
	if err != nil {
		o.failed = true
		o.anchor = nil
		event.Reason = "native_sample"
		return result, event, err
	}
	r, e, err := o.anchor.atSampleObserved(now)
	e.Check = event.Check
	event = e
	if err != nil {
		return result, event, err
	}
	return authorityCurrentTime{o, now, r, o.profile, o.sequence}, event, nil
}

func (o *authorityTimeOwner) elapsed(a, b authorityCurrentTime) (authorityTimeRange, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failed || o.closed || a.owner != o || b.owner != o || a.profile != o.profile || b.profile != o.profile {
		return authorityTimeRange{}, errAuthorityTime
	}
	return o.premises.elapsed(a.stamp, b.stamp)
}

func (o *authorityTimeOwner) fail() {
	o.mu.Lock()
	o.failed = true
	o.anchor = nil
	o.mu.Unlock()
	o.cancel()
}

func (o *authorityTimeOwner) close() {
	o.mu.Lock()
	o.closed = true
	o.anchor = nil
	o.mu.Unlock()
	o.cancel()
	o.producer.close()
	<-o.done
}
