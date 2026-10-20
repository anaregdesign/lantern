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
	installations   uint64
	installedAt     authorityTimeStamp // scheduling only; never replaces anchor.at
	denied          bool
	failed, closed  bool
	lastObservation *authorityTimeMeasurement
	lastError       error // bounded latest source/qualification failure, for startup diagnosis
	startupHistory  decisiontrace.StartupSnapshot
	runContext      context.Context
	cancel          context.CancelFunc
	done            chan struct{}
}

func startAuthorityTimeOwner(producer *authorityTimeProducer) *authorityTimeOwner {
	ctx, cancel := context.WithCancel(context.Background())
	o := &authorityTimeOwner{traceID: decisiontrace.NextID(), producer: producer, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription)), runContext: ctx, cancel: cancel, done: make(chan struct{})}
	go o.run(ctx, producer.measure)
	return o
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
	o.runLoopScheduled(ctx, measure, func() (uint64, error) {
		var raw [8]byte
		_, err := rand.Read(raw[:])
		return binary.BigEndian.Uint64(raw[:]), err
	})
}

// Private dependency seam, never a mutable factory or production selector.
func (o *authorityTimeOwner) runLoopScheduled(ctx context.Context, measure func(context.Context) (authorityTimeMeasurement, error), random func() (uint64, error)) {
	var retry authorityTimeRetry
	for {
		m, err := measure(ctx)
		probe, attempt := o.producer.completedProbe()
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
		stopped := errors.Is(err, errAuthorityTimeDenied)
		o.denied = stopped
		if errors.Is(err, errAuthorityTimeSourceFault) {
			o.anchor = nil
		}
		if errors.Is(err, errAuthorityTimeNative) {
			o.failed, o.anchor = true, nil
		}
		failed := o.failed
		generation, hasAnchor := o.sequence, o.anchor != nil
		probe.Kind, probe.Object, probe.Anchor, probe.HasAnchor = "time_probe", o.traceID, generation, hasAnchor
		if err == nil {
			probe.Reason = "installed"
		} else if probe.Reason == "ok" {
			probe.Reason = "install_rejected"
		}
		attempt.Stage, attempt.Reason = probe.Stage, probe.Reason
		if failed {
			attempt.Reason = "native_terminal"
		}
		o.recordStartupLocked(attempt)
		anchor := o.anchor
		installedAt := o.installedAt
		o.mu.Unlock()
		if stopped {
			probe.Reason = "association_denied"
			decisiontrace.Emit(probe)
			return
		}
		if failed {
			probe.Reason = "native_terminal"
			decisiontrace.Emit(probe)
			return
		}
		entropy, randomErr := random()
		if randomErr != nil {
			o.fail()
			return
		}
		refresh := time.Minute
		if anchor != nil {
			refresh = authorityTimeRefreshDelay(*anchor, installedAt, entropy)
		}
		delay := retry.delay(err, refresh)
		if err != nil && retry.qualified && delay < 15*time.Minute {
			delay = min(15*time.Minute, delay+time.Duration(entropy%uint64(delay/10+1)))
		}
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
	if _, err := o.premises.elapsed(m.sent, m.received); err != nil {
		o.failed, o.anchor = true, nil
		return err
	}
	a, reason, err := calculateAuthorityTimeObserved(m, o.premises)
	o.installReason = reason
	if err != nil {
		return errAuthorityTime
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
		if _, err := o.premises.elapsed(o.anchor.at, m.received); err != nil {
			o.failed, o.anchor = true, nil
			return err
		}
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
	o.installations++
	o.installedAt = now
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
		if e.Reason == "elapsed" {
			o.failed, o.anchor = true, nil
		}
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

// Copied observations only; this never samples or acquires time.
func (o *authorityTimeOwner) acquisitionCounts() decisiontrace.AcquisitionCounts {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.producer.mu.Lock()
	defer o.producer.mu.Unlock()
	return decisiontrace.AcquisitionCounts{Attempts: o.producer.attempts, Writes: o.producer.writes, Installations: o.installations, InFlight: o.producer.busy, Failed: o.failed, Denied: o.denied}
}
