package security

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorityTimeSource(t *testing.T) {
	source, err := parseAuthorityTimeSource([]byte("# existing source\nserver time.asia.apple.com\n"))
	if err != nil || source.host != "time.asia.apple.com" || source.configuration == [32]byte{} {
		t.Fatalf("%+v %v", source, err)
	}
	for _, config := range []string{"", "server", "pool a", "server a iburst", "server a\nserver b", "server a:123", "server a/b", "server -a", "server a-", "server a..b", "server a\nunknown c", string(make([]byte, 4097))} {
		if _, err := parseAuthorityTimeSource([]byte(config)); err == nil {
			t.Fatalf("accepted %q", config)
		}
	}
}

func TestAuthorityTimeProducer(t *testing.T) {
	for _, mode := range []string{"success", "bad nonce", "oversize", "cancel", "close", "sample failure", "sample regression", "sample epoch"} {
		t.Run(mode, func(t *testing.T) {
			server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })
			entered, release, serverDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseServer := func() { releaseOnce.Do(func() { close(release) }) }
			go func() {
				defer close(serverDone)
				var request [48]byte
				n, addr, err := server.ReadFromUDP(request[:])
				close(entered)
				if err != nil {
					return
				}
				<-release
				if n != 48 || request[0] != 4<<3|3 {
					return
				}
				response := fakeAuthorityNTPResponse([8]byte(request[40:48]))
				if mode == "bad nonce" {
					response[24] ^= 1
				}
				data := response[:]
				if mode == "oversize" {
					data = append(append([]byte(nil), data...), 1)
				}
				_, _ = server.WriteToUDP(data, addr)
			}()
			t.Cleanup(func() { releaseServer(); _ = server.Close(); <-serverDone })
			source, err := parseAuthorityTimeSource([]byte("server time.asia.apple.com\n"))
			if err != nil {
				t.Fatal(err)
			}
			var samples atomic.Uint64
			p := &authorityTimeProducer{source: source, sample: func() (authorityTimeStamp, error) {
				count := samples.Add(1)
				if count == 2 && mode == "sample failure" {
					return authorityTimeStamp{}, errAuthorityTime
				}
				stamp := fakeAuthorityTimeStamp(count * 10_000_000)
				if count == 2 && mode == "sample regression" {
					stamp.nanos = 0
				}
				if count == 2 && mode == "sample epoch" {
					stamp.boot[0] ^= 1
				}
				return stamp, nil
			}}
			t.Cleanup(p.close)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type outcome struct {
				m   authorityTimeMeasurement
				err error
			}
			done := make(chan outcome, 1)
			go func() { m, err := p.exchange(ctx, server.LocalAddr().String()); done <- outcome{m, err} }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("request did not reach fixture")
			}
			if _, err := p.exchange(ctx, server.LocalAddr().String()); !errors.Is(err, errAuthorityTimeBusy) {
				t.Fatalf("parallel exchange: %v", err)
			}
			if mode == "cancel" {
				cancel()
			}
			if mode == "close" {
				p.close()
			}
			releaseServer()
			var result outcome
			select {
			case result = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("measurement did not join")
			}
			if mode == "success" {
				if result.err != nil {
					t.Fatal(result.err)
				}
				m := result.m
				if m.source != source || m.endpoint != server.LocalAddr().String() || m.sent.nanos != 10_000_000 || m.received.nanos != 20_000_000 || m.sequence != 1 || [8]byte(m.request[40:48]) != [8]byte(m.response[24:32]) {
					t.Fatalf("measurement binding: %+v", m)
				}
			} else if result.err == nil || result.m != (authorityTimeMeasurement{}) {
				t.Fatalf("failed measurement published facts: %+v", result)
			}
			p.close()
			if _, err := p.exchange(ctx, server.LocalAddr().String()); !errors.Is(err, errAuthorityTimeClosed) {
				t.Fatalf("post-close: %v", err)
			}
		})
	}
}

func TestAuthorityTimeProducerNoCachedSuccess(t *testing.T) {
	source, _ := parseAuthorityTimeSource([]byte("server time.asia.apple.com"))
	p := &authorityTimeProducer{source: source, sample: func() (authorityTimeStamp, error) { return fakeAuthorityTimeStamp(1), nil }}
	defer p.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m, err := p.exchange(ctx, "127.0.0.1:123"); err == nil || m != (authorityTimeMeasurement{}) {
		t.Fatal("cancelled request published measurement")
	}
	p.sequence = ^uint64(0)
	if _, err := p.exchange(context.Background(), "127.0.0.1:123"); err == nil {
		t.Fatal("sequence wrapped")
	}
}
