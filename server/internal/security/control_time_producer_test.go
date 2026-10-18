package security

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestAuthorityTimeProducerStartupContextObservation(t *testing.T) {
	for _, mode := range []string{"own deadline", "upstream cancel", "upstream deadline", "immediate read error", "depleted pre-read budget"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				incoming, cancel := context.WithCancel(context.Background())
				if mode == "upstream deadline" {
					cancel()
					incoming, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				}
				defer cancel()
				source, _ := parseAuthorityTimeSource([]byte("server fixture.invalid"))
				var samples int
				p := &authorityTimeProducer{source: source, sample: func() (authorityTimeStamp, error) {
					samples++
					return fakeAuthorityTimeStamp(uint64(samples) * 10_000_000), nil
				}}
				defer p.close()
				var flight context.Context
				conn := &timeProducerConn{write: func(b []byte) (int, error) { return len(b), nil }, read: func([]byte) (int, error) {
					if mode == "immediate read error" {
						return 0, net.ErrClosed
					}
					if mode == "upstream cancel" {
						cancel()
					}
					<-flight.Done()
					return 0, net.ErrClosed
				}}
				_, err := p.exchangeIO(incoming, "", func(context.Context, string, string) ([]netip.Addr, error) {
					if mode == "depleted pre-read budget" {
						time.Sleep(2900 * time.Millisecond)
					}
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}, func(ctx context.Context, _, _ string) (net.Conn, error) { flight = ctx; return conn, nil })
				if err == nil || samples != 1 {
					t.Fatal("diagnostic changed the failed exchange or native sampling count")
				}
				_, observation := p.completedProbe()
				if observation.Attempt != 1 || observation.Stage != "read" || !observation.HasReadBudget {
					t.Fatal("observation does not describe the actual failed Read")
				}
				wantIncoming, wantExchange := decisiontrace.ContextLive, decisiontrace.ContextDeadlineExceeded
				wantBudget := int64(3 * time.Second)
				switch mode {
				case "upstream cancel":
					wantIncoming, wantExchange = decisiontrace.ContextCanceled, decisiontrace.ContextCanceled
				case "upstream deadline":
					wantIncoming, wantBudget = decisiontrace.ContextDeadlineExceeded, int64(100*time.Millisecond)
				case "immediate read error":
					wantExchange = decisiontrace.ContextLive
				case "depleted pre-read budget":
					wantBudget = int64(100 * time.Millisecond)
				}
				if observation.IncomingContext != wantIncoming || observation.ExchangeContext != wantExchange || observation.ReadRemainingNS != wantBudget {
					t.Fatalf("pre-cleanup context/budget differs: %+v", observation)
				}
				if flight.Err() == nil {
					t.Fatal("exchange cleanup did not cancel its child context")
				}
			})
		})
	}
}

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
	capture := decisiontrace.Start(64)
	defer capture.Stop()
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
			probe := p.probeObservation()
			if probe.Attempt != 1 || probe.EndpointDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(server.LocalAddr().String()))) || probe.Family != "ipv4" || probe.Elapsed <= 0 {
				t.Fatal("probe diagnostic is not the completed attempt")
			}
			if mode == "bad nonce" && probe.Stage != "packet" {
				t.Fatal("packet refusal classification")
			}
			if mode == "sample failure" && probe.Stage != "native_receive" {
				t.Fatal("sample failure classification")
			}
			if mode == "success" {
				if probe.Reason != "ok" || probe.Native != 20_000_000 || samples.Load() != 2 {
					t.Fatal("diagnostic sampled again or altered success")
				}
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

func TestAuthorityTimeEndpointUsesBoundedConfiguredAnswers(t *testing.T) {
	answers := []netip.Addr{netip.MustParseAddr("2001:db8::2"), netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.1")}
	a, err := authorityTimeEndpoints(answers, 0)
	if err != nil || !slices.Equal(a, []string{"192.0.2.1:123", "[2001:db8::2]:123"}) {
		t.Fatal(a, err)
	}
	b, err := authorityTimeEndpoints(answers, 1)
	if err != nil || !slices.Equal(b, []string{"[2001:db8::2]:123", "192.0.2.1:123"}) {
		t.Fatal(b, err)
	}
	c, err := authorityTimeEndpoints(answers, 2)
	if err != nil || !slices.Equal(c, a) {
		t.Fatal("no scheduled rotation", c, err)
	}
	if answers[0].String() != "2001:db8::2" {
		t.Fatal("mutated resolver result")
	}
	for _, bad := range [][]netip.Addr{nil, make([]netip.Addr, 17), {netip.Addr{}}, {netip.MustParseAddr("::")}, {netip.MustParseAddr("ff02::1")}} {
		if _, err := authorityTimeEndpoints(bad, 0); err == nil {
			t.Fatal("bad DNS answer scope")
		}
	}
}

type timeProducerConn struct {
	write       func([]byte) (int, error)
	read        func([]byte) (int, error)
	deadlineErr error
}

func (c *timeProducerConn) Write(p []byte) (int, error) { return c.write(p) }
func (c *timeProducerConn) Read(p []byte) (int, error)  { return c.read(p) }
func (*timeProducerConn) Close() error                  { return nil }
func (*timeProducerConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 123}
}
func (c *timeProducerConn) RemoteAddr() net.Addr           { return c.LocalAddr() }
func (c *timeProducerConn) SetDeadline(time.Time) error    { return c.deadlineErr }
func (*timeProducerConn) SetReadDeadline(time.Time) error  { return nil }
func (*timeProducerConn) SetWriteDeadline(time.Time) error { return nil }

func TestAuthorityTimeTransportClassification(t *testing.T) {
	for _, mode := range []string{"dns", "dial", "write", "read", "deadline", "short write", "native send", "native receive", "dns answers", "packet", "source fault", "RATE", "DENY", "RSTR"} {
		t.Run(mode, func(t *testing.T) {
			cause := &net.OpError{Op: "boundary", Net: "udp", Err: context.DeadlineExceeded}
			source, _ := parseAuthorityTimeSource([]byte("server fixture.invalid"))
			samples, writes, reads, dials := 0, 0, 0, 0
			p := &authorityTimeProducer{source: source, sample: func() (authorityTimeStamp, error) {
				samples++
				if mode == "native send" || mode == "native receive" && samples == 2 {
					return authorityTimeStamp{}, cause // net.Error is NOT sufficient classification
				}
				return fakeAuthorityTimeStamp(uint64(samples) * 10_000_000), nil
			}}
			defer p.close()
			var nonce [8]byte
			conn := &timeProducerConn{
				write: func(b []byte) (int, error) {
					writes++
					copy(nonce[:], b[40:48])
					if mode == "write" {
						return 0, cause
					}
					if mode == "short write" {
						return len(b) - 1, nil
					}
					return len(b), nil
				},
				read: func(b []byte) (int, error) {
					reads++
					if mode == "read" {
						return 0, cause
					}
					response := fakeAuthorityNTPResponse(nonce)
					switch mode {
					case "packet":
						response[24] ^= 1
					case "source fault":
						response[0] |= 1 << 6
					case "RATE", "DENY", "RSTR":
						response[1] = 0
						copy(response[12:16], mode)
					}
					return copy(b, response[:]), nil
				},
			}
			if mode == "deadline" {
				conn.deadlineErr = cause
			}
			lookup := func(ctx context.Context, network, host string) ([]netip.Addr, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second || network != "ip" || host != source.host {
					t.Fatal("changed source or measurement budget")
				}
				if mode == "dns" {
					return nil, cause
				}
				if mode == "dns answers" {
					return nil, nil
				}
				return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2"), netip.MustParseAddr("2001:db8::2")}, nil
			}
			dial := func(context.Context, string, string) (net.Conn, error) {
				dials++
				if mode == "dial" {
					return nil, cause
				}
				return conn, nil
			}
			m, err := p.exchangeIO(t.Context(), "", lookup, dial)
			var marked *authorityTimeTransportError
			want := mode == "dns" || mode == "dial" || mode == "write" || mode == "read"
			if err == nil || errors.As(err, &marked) != want || m != (authorityTimeMeasurement{}) {
				t.Fatal("incorrect transport classification or published failed measurement")
			}
			if want || mode == "deadline" || mode == "native send" || mode == "native receive" {
				var original *net.OpError
				if !errors.Is(err, cause) || !errors.As(err, &original) || original != cause || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("lost original error identity")
				}
			}
			if mode == "RATE" && !errors.Is(err, errAuthorityTimeRate) || (mode == "DENY" || mode == "RSTR") && !errors.Is(err, errAuthorityTimeDenied) {
				t.Fatal("lost source response priority")
			}
			if writes > 1 || reads > 1 || dials > 1 {
				t.Fatal("retried within one attempt")
			}
		})
	}
}

func TestAuthorityTimeProducerSkipsOnlyUnreachableBeforeSend(t *testing.T) {
	capture := decisiontrace.Start(64)
	if capture == nil {
		t.Fatal("capture unavailable")
	}
	defer capture.Stop()
	for _, mode := range []string{"network", "host", "family", "all unreachable", "limit", "permission", "other", "canceled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			source, _ := parseAuthorityTimeSource([]byte("server fixture.invalid"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer deadlineCancel()
			}
			var deadline time.Time
			lookups, samples, writes, reads := 0, 0, 0, 0
			var dialed []string
			p := &authorityTimeProducer{source: source, addressTurn: 1, sample: func() (authorityTimeStamp, error) {
				samples++
				return fakeAuthorityTimeStamp(uint64(samples) * 10_000_000), nil
			}}
			defer p.close()
			lookup := func(ctx context.Context, network, host string) ([]netip.Addr, error) {
				lookups++
				var ok bool
				deadline, ok = ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second || network != "ip" || host != source.host {
					t.Fatal("source or original deadline changed")
				}
				if mode == "limit" {
					answers := make([]netip.Addr, 16)
					for i := range answers {
						answers[i] = netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)})
					}
					return answers, nil
				}
				return []netip.Addr{netip.MustParseAddr("2001:db8::2"), netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2")}, nil
			}
			var nonce [8]byte
			conn := &timeProducerConn{
				write: func(b []byte) (int, error) { writes++; copy(nonce[:], b[40:48]); return len(b), nil },
				read: func(b []byte) (int, error) {
					reads++
					reply := fakeAuthorityNTPResponse(nonce)
					return copy(b, reply[:]), nil
				},
			}
			cause := &net.OpError{Op: "dial", Net: "udp", Err: syscall.ENETUNREACH}
			switch mode {
			case "host":
				cause.Err = syscall.EHOSTUNREACH
			case "family":
				cause.Err = syscall.EAFNOSUPPORT
			case "permission":
				cause.Err = syscall.EACCES
			case "other":
				cause.Err = context.DeadlineExceeded
			}
			dial := func(ctx context.Context, network, address string) (net.Conn, error) {
				actual, _ := ctx.Deadline()
				if network != "udp" || actual != deadline {
					t.Fatal("fallback changed transport or renewed deadline")
				}
				dialed = append(dialed, address)
				if mode == "canceled" {
					cancel()
				}
				if mode == "deadline" {
					<-ctx.Done()
				}
				if address != "127.0.0.1:123" || mode == "all unreachable" {
					return nil, cause
				}
				return conn, nil
			}
			m, err := p.exchangeIO(ctx, "", lookup, dial)
			success := mode == "network" || mode == "host" || mode == "family"
			if success {
				if err != nil || samples != 2 || writes != 1 || reads != 1 || m.sequence != 1 || m.source != source || m.endpoint != "127.0.0.1:123" {
					t.Fatal("fallback changed the single bound measurement", err, samples, writes, reads)
				}
				if !slices.Equal(dialed, []string{"[2001:db8::1]:123", "[2001:db8::2]:123", "127.0.0.1:123"}) {
					t.Fatal("configured rotation or no-repeat bound changed", dialed)
				}
			} else {
				var transport *authorityTimeTransportError
				if !errors.As(err, &transport) || m != (authorityTimeMeasurement{}) || samples != 0 || writes != 0 || reads != 0 {
					t.Fatal("failed connection created time authority or sent a datagram", err)
				}
				want := 1
				if mode == "all unreachable" {
					want = 3
				}
				if mode == "limit" {
					want = 16
					unique := slices.Clone(dialed)
					slices.Sort(unique)
					if len(slices.Compact(unique)) != want {
						t.Fatal("repeated configured address")
					}
				}
				if len(dialed) != want {
					t.Fatal("unexpected connection retry", dialed)
				}
				if !errors.Is(err, cause) {
					t.Fatal("lost original connection error", err)
				}
			}
			probe := p.probeObservation()
			if lookups != 1 || p.attempts != 1 || p.addressTurn != 2 || probe.DialAttempts != len(dialed) {
				t.Fatal("retried DNS/scheduled attempt or lost diagnostic")
			}
		})
	}
}
