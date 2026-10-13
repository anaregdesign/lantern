//go:build lantern_test_time && (darwin || linux)

package security

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

// This file is excluded from normal builds. Even an explicitly tagged server
// binary refuses: only go test binaries may create synthetic UTC facts. The
// current-authority constructor's binding/custody/store/origin path is shared.
func newCurrentAuthorityTimeOwner() (*authorityTimeOwner, error) {
	if !testing.Testing() {
		return nil, errAuthorityTime
	}
	o, _, err := newFixtureAuthorityTimeOwner()
	return o, err
}

type fixtureAuthorityTimeSource struct {
	socket *net.UDPConn
	drop   atomic.Bool
	done   chan struct{}
}

func newFixtureAuthorityTimeOwner() (*authorityTimeOwner, *fixtureAuthorityTimeSource, error) {
	sample, err := newNativeAuthorityTimeSampler()
	if err != nil {
		return nil, nil, err
	}
	base, err := sample()
	if err != nil {
		return nil, nil, err
	}
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, nil, err
	}
	f := &fixtureAuthorityTimeSource{socket: socket, done: make(chan struct{})}
	// One synthetic input, never a fallback after attempting the real source.
	utc := time.Now().UTC()
	go func() {
		defer close(f.done)
		for {
			var request [49]byte
			n, peer, err := socket.ReadFromUDP(request[:])
			if err != nil {
				return
			}
			if n != 48 || request[0] != 4<<3|3 || f.drop.Load() {
				continue
			}
			stamp, err := sample()
			if err != nil {
				return
			}
			now := utc.Add(time.Duration(stamp.nanos - base.nanos))
			ntp := uint64(now.Unix()+2_208_988_800)<<32 | uint64(now.Nanosecond())*(1<<32)/1_000_000_000
			var response [48]byte
			response[0], response[1], response[3] = 4<<3|4, 1, 226
			copy(response[24:32], request[40:48])
			binary.BigEndian.PutUint64(response[16:24], ntp-(1<<32))
			binary.BigEndian.PutUint64(response[32:40], ntp)
			binary.BigEndian.PutUint64(response[40:48], ntp)
			_, _ = socket.WriteToUDP(response[:], peer)
		}
	}()
	source, _ := parseAuthorityTimeSource([]byte("server fixture.invalid\n"))
	p := &authorityTimeProducer{source: source, sample: sample}
	ctx, cancel := context.WithCancel(context.Background())
	o := &authorityTimeOwner{traceID: decisiontrace.NextID(), producer: p, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription)), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(o.done)
		defer func() { _ = socket.Close(); <-f.done }()
		o.runLoop(ctx, func(ctx context.Context) (authorityTimeMeasurement, error) {
			return p.exchange(ctx, socket.LocalAddr().String())
		})
	}()
	return o, f, nil
}
