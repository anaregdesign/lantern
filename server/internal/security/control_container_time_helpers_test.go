//go:build linux && (amd64 || arm64)

package security

// This fixture supplies synthetic UTC facts only. It exercises real Linux
// sampling, UDP, owner.run and downstream quorum; it is never the production
// constructor or evidence about the configured upstream's UTC accuracy.
import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type containerTimeAttempt struct {
	Member            uint32
	Start, End        containerStamp
	Sequence          uint64
	Error             string
	Request, Response [48]byte
}
type containerTimePacket struct {
	Counter uint64
	Peer    string
	Dropped bool
	Nonce   [8]byte
}
type containerTimeFixture struct {
	mu            sync.Mutex
	socket        *net.UDPConn
	sample        func() (authorityTimeStamp, error)
	base          authorityTimeStamp
	utc           time.Time
	drop          bool
	attempts      []containerTimeAttempt
	packets       []containerTimePacket
	done          chan struct{}
	oldChallenges map[uint32][32]byte
	active        map[uint32]bool
}

func newContainerTimeFixture(t *testing.T) *containerTimeFixture {
	t.Helper()
	sample, err := newNativeAuthorityTimeSampler()
	containerMust(t, err)
	base, err := sample()
	containerMust(t, err)
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	containerMust(t, err)
	f := &containerTimeFixture{socket: socket, sample: sample, base: base, utc: time.Now().UTC(), done: make(chan struct{}), oldChallenges: map[uint32][32]byte{}, active: map[uint32]bool{}}
	go func() {
		defer close(f.done)
		for {
			var raw [49]byte
			n, peer, err := socket.ReadFromUDP(raw[:])
			if err != nil {
				return
			}
			stamp, err := sample()
			if err != nil {
				return
			}
			if n != 48 || raw[0] != 4<<3|3 {
				continue
			}
			nonce := [8]byte(raw[40:48])
			f.mu.Lock()
			f.packets = append(f.packets, containerTimePacket{stamp.nanos, peer.String(), f.drop, nonce})
			if !f.drop {
				// One wall reading supplies a fixed synthetic base. Subsequent timestamps
				// follow only this fixture's actual native elapsed counter.
				now := f.utc.Add(time.Duration(stamp.nanos - f.base.nanos))
				ntp := uint64(now.Unix()+2_208_988_800)<<32 | uint64(now.Nanosecond())*(1<<32)/1_000_000_000
				response := fakeAuthorityNTPResponse(nonce)
				binary.BigEndian.PutUint64(response[16:24], ntp-(1<<32))
				binary.BigEndian.PutUint64(response[32:40], ntp)
				binary.BigEndian.PutUint64(response[40:48], ntp)
				_, _ = socket.WriteToUDP(response[:], peer)
			}
			f.mu.Unlock()
		}
	}()
	t.Cleanup(func() { _ = socket.Close(); <-f.done })
	return f
}
func (f *containerTimeFixture) owner(t *testing.T, id uint32) *authorityTimeOwner {
	t.Helper()
	sample, err := newNativeAuthorityTimeSampler()
	containerMust(t, err)
	source, err := parseAuthorityTimeSource([]byte("server fixture.invalid\n"))
	containerMust(t, err)
	p := &authorityTimeProducer{source: source, sample: sample}
	ctx, cancel := context.WithCancel(context.Background())
	o := &authorityTimeOwner{producer: p, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription)), cancel: cancel, done: make(chan struct{})}
	go o.run(ctx, func(ctx context.Context) (authorityTimeMeasurement, error) {
		f.mu.Lock()
		f.active[id] = true
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.active[id] = false; f.mu.Unlock() }()
		start, err := sample()
		if err != nil {
			return authorityTimeMeasurement{}, err
		}
		m, err := p.exchange(ctx, f.socket.LocalAddr().String())
		end, endErr := sample()
		err = errors.Join(err, endErr)
		a := containerTimeAttempt{Member: id, Start: containerStamp{Boot: start.boot, Process: start.process, Counter: start.nanos}, End: containerStamp{Boot: end.boot, Process: end.process, Counter: end.nanos}, Sequence: m.sequence, Request: m.request, Response: m.response}
		if err != nil {
			a.Error = err.Error()
		}
		f.mu.Lock()
		f.attempts = append(f.attempts, a)
		f.mu.Unlock()
		return m, err
	})
	t.Cleanup(o.close)
	deadline := time.Now().Add(45 * time.Second)
	for {
		if _, err := o.current(); err == nil {
			return o
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture initial native anchor unavailable")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (g *ContainerAuthorityGate) stopTimeFixture() {
	f := g.timeFixture
	f.mu.Lock()
	f.drop = true
	f.mu.Unlock() // Ack: every later response is dropped.
	// Drain any flight sent before the phase switch and its owner installation.
	deadline := time.Now().Add(5 * time.Second)
	for {
		settled := true
		for id, origin := range g.owners {
			o := origin.network.timeOwner
			o.producer.mu.Lock()
			busy := o.producer.busy
			o.producer.mu.Unlock()
			o.mu.Lock()
			sequence := o.sequence
			o.mu.Unlock()
			f.mu.Lock()
			active := f.active[id]
			last := uint64(0)
			for _, a := range f.attempts {
				if a.Member == id && a.Error == "" {
					last = a.Sequence
				}
			}
			f.mu.Unlock()
			if busy || active || sequence != last {
				settled = false
			}
		}
		if settled {
			break
		}
		if time.Now().After(deadline) {
			g.t.Fatal("fixture phase did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for id, o := range g.owners {
		o.network.kernel.gate.Lock()
		f.oldChallenges[id] = o.network.receiver.active.certificate.request.statement.Challenge
		o.network.kernel.gate.Unlock()
	}
}

func (g *ContainerAuthorityGate) verifyTimeFixtureLoss() {
	t := g.t
	f := g.timeFixture
	f.mu.Lock()
	attempts := append([]containerTimeAttempt(nil), f.attempts...)
	packets := append([]containerTimePacket(nil), f.packets...)
	f.mu.Unlock()
	ages := map[uint32]struct{ Low, High uint64 }{}
	for id, origin := range g.owners {
		o := origin.network.timeOwner
		now, err := o.producer.sample()
		containerMust(t, err)
		old := g.anchors[id]
		age, err := o.premises.elapsed(old.at, now)
		containerMust(t, err)
		if age.low < o.premises.maxAge {
			t.Fatal("last successful anchor did not exceed holdover", id, age)
		}
		if _, err := old.atSample(now); err == nil {
			t.Fatal("retained anchor remained current")
		}
		o.mu.Lock()
		sequence := o.sequence
		o.mu.Unlock()
		if sequence != g.checkpoint.Anchors[id].Sequence {
			t.Fatal("failed flight refreshed successful sequence")
		}
		if _, err := o.current(); err == nil {
			t.Fatal("source loss retained qualification")
		}
		timeouts := 0
		for _, a := range attempts {
			if a.Member == id && a.Error != "" {
				timeouts++
			}
		}
		if timeouts < 2 {
			t.Fatal("two real UDP timeouts not observed", id, timeouts)
		}
		ages[id] = struct{ Low, High uint64 }{age.low, age.high}
	}
	containerWrite(t, filepath.Join(g.dir, "fixture-loss.json"), map[string]any{"source": "fixture.invalid", "actual_configured_source": false, "synthetic_UTC_not_accuracy_evidence": true, "last_anchor_ages": ages, "attempts": attempts, "packets": packets})
}

func (g *ContainerAuthorityGate) verifyTimeFixtureRecovery() {
	t := g.t
	f := g.timeFixture
	f.mu.Lock()
	attempts := append([]containerTimeAttempt(nil), f.attempts...)
	packets := append([]containerTimePacket(nil), f.packets...)
	f.mu.Unlock()
	for id, origin := range g.owners {
		o := origin.network.timeOwner
		now, err := o.current()
		containerMust(t, err)
		old := g.checkpoint.Stamps[id]
		if now.stamp.boot != old.Boot || now.stamp.process != old.Process {
			t.Fatal("source recovery changed native epoch")
		}
		if now.sequence <= g.checkpoint.Anchors[id].Sequence {
			t.Fatal("recovery reused anchor sequence")
		}
		origin.network.kernel.gate.Lock()
		challenge := origin.network.receiver.active.certificate.request.statement.Challenge
		origin.network.kernel.gate.Unlock()
		if challenge == f.oldChallenges[id] {
			t.Fatal("recovery reused renewal challenge")
		}
		var previous *containerTimeAttempt
		backoff := time.Duration(0)
		nonces := map[[8]byte]bool{}
		losses := 0
		for i := range attempts {
			a := &attempts[i]
			if a.Member != id {
				continue
			}
			if previous != nil {
				elapsed, err := o.premises.elapsed(authorityTimeStamp{previous.End.Boot, previous.End.Process, previous.End.Counter}, authorityTimeStamp{a.Start.Boot, a.Start.Process, a.Start.Counter})
				containerMust(t, err)
				minimum := 16 * time.Second
				if backoff > minimum {
					minimum = backoff
				}
				if elapsed.high < uint64(minimum) {
					t.Fatal("owner retry bypassed unchanged schedule", id, elapsed, minimum)
				}
			}
			if a.Error != "" {
				losses++
				if backoff == 0 {
					backoff = 32 * time.Second
				} else {
					backoff = min(backoff*2, 1024*time.Second)
				}
			} else {
				nonce := [8]byte(a.Request[40:48])
				if nonces[nonce] || nonce == [8]byte{} || nonce != [8]byte(a.Response[24:32]) {
					t.Fatal("successful measurement nonce replay/mismatch")
				}
				nonces[nonce] = true
				backoff = 0
			}
			previous = a
		}
		if losses < 2 || len(nonces) < 2 {
			t.Fatal("missing actual loss and fresh-response chain")
		}
	}
	containerWrite(t, filepath.Join(g.dir, "fixture-result.json"), map[string]any{"source": "fixture.invalid", "actual_configured_source": false, "synthetic_UTC_not_accuracy_evidence": true, "same_socket": f.socket.LocalAddr().String(), "same_native_epochs": true, "fresh_sequence_anchor_challenge": true, "unchanged_backoff_observed": true, "attempts": attempts, "packets": packets})
}

// Marker waiting is test control only; authorization uses native elapsed bounds.
func containerWaitMarker(t *testing.T, dir, name string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("controller marker unavailable", name)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
