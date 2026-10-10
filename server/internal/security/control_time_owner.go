package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"time"
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
	o := &authorityTimeOwner{producer: producer, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription)), cancel: cancel, done: make(chan struct{})}
	go o.run(ctx, producer.measure)
	return o
}

// Timers schedule work only. Authorization expiry uses native elapsed bounds.
// There is never a backlog or catch-up burst after a scheduler pause.
func (o *authorityTimeOwner) run(ctx context.Context, measure func(context.Context) (authorityTimeMeasurement, error)) {
	defer close(o.done)
	backoff := time.Duration(0)
	for {
		m, err := measure(ctx)
		o.mu.Lock()
		if o.closed || o.failed || ctx.Err() != nil {
			o.mu.Unlock()
			return
		}
		if err == nil {
			err = o.installLocked(m)
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
		o.mu.Unlock()
		if failed {
			return
		}
		if err == nil {
			backoff = 0
		} else if backoff == 0 {
			backoff = 32 * time.Second
		} else {
			backoff = min(backoff*2, 1024*time.Second)
		}
		if errors.Is(err, errAuthorityTimeRate) {
			backoff = 1024 * time.Second
		}
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			o.fail()
			return
		}
		delay := 16*time.Second + time.Duration(binary.BigEndian.Uint64(random[:])%uint64(4*time.Second))
		if backoff > delay {
			delay = backoff
		}
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
	if o.closed || o.failed || m.source != o.producer.source || m.sequence <= o.sequence {
		return errAuthorityTime
	}
	a, err := calculateAuthorityTime(m, o.premises)
	if err != nil {
		o.anchor = nil
		return errAuthorityTimeSourceFault
	}
	now, err := o.producer.sample()
	if err != nil {
		o.failed = true
		o.anchor = nil
		return err
	}
	if _, err := a.atSample(now); err != nil {
		return err
	}
	if o.anchor != nil {
		old, err := o.anchor.atSample(m.received)
		if err == nil && (old.high < a.utc.low || a.utc.high < old.low) {
			o.failed = true
			o.anchor = nil
			return errAuthorityTime
		}
	}
	o.anchor = &a
	o.lastObservation = &m
	o.sequence = m.sequence
	return nil
}

func (o *authorityTimeOwner) current() (authorityCurrentTime, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.failed || o.anchor == nil {
		return authorityCurrentTime{}, errAuthorityTime
	}
	now, err := o.producer.sample()
	if err != nil {
		o.failed = true
		o.anchor = nil
		return authorityCurrentTime{}, err
	}
	r, err := o.anchor.atSample(now)
	if err != nil {
		return authorityCurrentTime{}, err
	}
	return authorityCurrentTime{o, now, r, o.profile, o.sequence}, nil
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
