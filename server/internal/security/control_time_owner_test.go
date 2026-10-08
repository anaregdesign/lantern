package security

import (
	"context"
	"crypto/sha256"
	"sync/atomic"
	"testing"
	"time"
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
