package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

var (
	errAuthorityTimeBusy   = errors.New("time measurement already in progress")
	errAuthorityTimeClosed = errors.New("time measurement producer closed")
)

// Only errors returned by DNS, dial, Write or Read get this private marker.
// It identifies a boundary, not a claim that the failure is transient. Native,
// packet, source and installation failures must never acquire retry credits.
type authorityTimeTransportError struct{ cause error }

func (e *authorityTimeTransportError) Error() string { return e.cause.Error() }
func (e *authorityTimeTransportError) Unwrap() error { return e.cause }

type authorityTimeSource struct {
	host          string
	configuration [32]byte
}

// Strictly supports the observed single-server configuration. Unknown options,
// additional servers and oversized input refuse instead of silently changing
// the source selection policy. The digest records bytes, not trust in DNS/NTP.
func parseAuthorityTimeSource(config []byte) (authorityTimeSource, error) {
	if len(config) == 0 || len(config) > 4096 {
		return authorityTimeSource{}, errAuthorityTime
	}
	var host string
	for line := range strings.SplitSeq(string(config), "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if host != "" || len(fields) != 2 || fields[0] != "server" {
			return authorityTimeSource{}, errAuthorityTime
		}
		host = fields[1]
	}
	if len(host) == 0 || len(host) > 253 {
		return authorityTimeSource{}, errAuthorityTime
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return authorityTimeSource{}, errAuthorityTime
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return authorityTimeSource{}, errAuthorityTime
			}
		}
	}
	return authorityTimeSource{host, sha256.Sum256(config)}, nil
}

type authorityTimeMeasurement struct {
	source         authorityTimeSource
	endpoint       string
	sent, received authorityTimeStamp
	packet         authorityNTPPacket
	request        [48]byte
	response       [48]byte
	sequence       uint64
}

// Private, unwired fact producer. One bounded datagram exchange at a time, no
// packet retries, no background goroutine, no cached success after loss, no OS writes.
// The time owner owns refresh/backoff and qualified invalidation.
type authorityTimeProducer struct {
	mu           sync.Mutex
	closed, busy bool
	cancel       context.CancelFunc
	workers      sync.WaitGroup
	sequence     uint64
	addressTurn  uint64
	attempts     uint64
	lastProbe    decisiontrace.Event
	lastAttempt  decisiontrace.StartupAttempt
	source       authorityTimeSource
	sample       func() (authorityTimeStamp, error)
}

func (p *authorityTimeProducer) measure(ctx context.Context) (authorityTimeMeasurement, error) {
	return p.exchange(ctx, "")
}

// The address seam permits local UDP fault tests. Production composition, when
// qualified, must use measure and the exact configured source, never this seam.
func (p *authorityTimeProducer) exchange(ctx context.Context, address string) (authorityTimeMeasurement, error) {
	return p.exchangeIO(ctx, address, net.DefaultResolver.LookupNetIP, (&net.Dialer{}).DialContext)
}

// Private deterministic boundary seam; production always uses exchange above.
func (p *authorityTimeProducer) exchangeIO(ctx context.Context, address string,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (measurement authorityTimeMeasurement, resultErr error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return authorityTimeMeasurement{}, errAuthorityTimeClosed
	}
	if p.busy {
		p.mu.Unlock()
		return authorityTimeMeasurement{}, errAuthorityTimeBusy
	}
	if ctx == nil || p.sample == nil || p.source.host == "" || p.source.configuration == [32]byte{} || p.sequence == ^uint64(0) || (address == "" && p.addressTurn == ^uint64(0)) {
		p.mu.Unlock()
		return authorityTimeMeasurement{}, errAuthorityTime
	}
	incoming := ctx
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	p.cancel, p.busy = cancel, true
	sequence := p.sequence + 1
	addressTurn := p.addressTurn
	if address == "" {
		p.addressTurn++
	}
	p.attempts++
	probe := decisiontrace.Event{Attempt: p.attempts, Turn: addressTurn, Stage: "dns", Reason: "ok"}
	var readCount uint8
	var firstReadError, finalReadError string
	started := time.Now()
	p.workers.Add(1)
	p.mu.Unlock()
	defer func() {
		// Copy cancellation facts before our own cleanup cancel can change them.
		exchangeState, incomingState := authorityTimeContextState(ctx), authorityTimeContextState(incoming)
		cancel()
		p.mu.Lock()
		p.cancel, p.busy = nil, false
		probe.Elapsed = int64(time.Since(started))
		if resultErr != nil {
			probe.Reason = probe.Stage
			var ne net.Error
			if probe.Stage == "read" && (errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(resultErr, &ne) && ne.Timeout()) {
				probe.Reason = "read_timeout"
			}
			if errors.Is(resultErr, errAuthorityTimeRate) {
				probe.Reason = "rate"
			}
			if errors.Is(resultErr, errAuthorityTimeDenied) {
				probe.Reason = "deny"
			}
			if errors.Is(resultErr, errAuthorityTimeSourceFault) {
				probe.Reason = "source_fault"
			}
		}
		p.lastProbe = probe
		p.lastAttempt = decisiontrace.StartupAttempt{Attempt: probe.Attempt, Stage: probe.Stage, Reason: probe.Reason,
			HasReadBudget: probe.HasDeadline, ReadRemainingNS: probe.DeadlineLeft,
			ExchangeContext: exchangeState, IncomingContext: incomingState,
			ReadCount: readCount, FirstReadError: firstReadError, FinalReadError: finalReadError}
		p.mu.Unlock()
		p.workers.Done()
	}()
	addresses := []string{address}
	if address == "" {
		// Rotate the exact source's bounded answers. Only local route/family
		// failures before any packet may advance within this same deadline;
		// response loss still waits for the owner's next scheduled attempt.
		answers, err := lookup(ctx, "ip", p.source.host)
		if err != nil {
			return authorityTimeMeasurement{}, &authorityTimeTransportError{err}
		}
		if decisiontrace.Enabled() {
			normalized := make([]string, 0, len(answers))
			for _, a := range answers {
				normalized = append(normalized, a.Unmap().String())
			}
			slices.Sort(normalized)
			normalized = slices.Compact(normalized)
			probe.DNSCount = len(normalized)
			probe.DNSDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(normalized, "\n"))))
		}
		addresses, err = authorityTimeEndpoints(answers, addressTurn)
		if err != nil {
			return authorityTimeMeasurement{}, err
		}
	}

	probe.Stage = "random"
	var request [48]byte
	request[0] = 4<<3 | 3
	if _, err := rand.Read(request[40:48]); err != nil {
		return authorityTimeMeasurement{}, err
	}
	nonce := [8]byte(request[40:48])
	if nonce == [8]byte{} {
		return authorityTimeMeasurement{}, errAuthorityTime
	}
	// Connected UDP binds the response endpoint; the ephemeral local port is
	// selected by the OS (RFC 9109). Correlation is not authentication.
	probe.Stage = "dial"
	var conn net.Conn
	var err error
	for i, candidate := range addresses {
		probe.DialAttempts = i + 1
		if decisiontrace.Enabled() {
			probe.EndpointDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(candidate)))
			host, _, _ := net.SplitHostPort(candidate)
			ip, _ := netip.ParseAddr(host)
			probe.Family = "ipv6"
			if ip.Is4() {
				probe.Family = "ipv4"
			}
		}
		conn, err = dial(ctx, "udp", candidate)
		unreachable := errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.EAFNOSUPPORT)
		if err == nil || ctx.Err() != nil || !unreachable {
			break
		}
	}
	if err != nil {
		return authorityTimeMeasurement{}, &authorityTimeTransportError{err}
	}
	defer conn.Close()
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(callbackDone) })
	defer func() {
		if !stop() {
			<-callbackDone
		}
	}()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return authorityTimeMeasurement{}, err
	}
	probe.Stage = "native_send"
	sent, err := p.sample()
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
	probe.Native = sent.nanos
	probe.Stage = "send"
	n, err := conn.Write(request[:])
	if err != nil {
		return authorityTimeMeasurement{}, &authorityTimeTransportError{err}
	}
	if n != len(request) {
		return authorityTimeMeasurement{}, errAuthorityTime
	}
	// One extra byte makes oversized/truncated datagrams rejectable.
	var raw [49]byte
	probe.Stage = "read"
	probe.HasDeadline, probe.DeadlineLeft = true, int64(time.Until(deadline))
	n, err = conn.Read(raw[:])
	readCount, firstReadError = 1, authorityTimeReadError(err)
	finalReadError = firstReadError
	// Give a zero-byte Darwin receive abort one bounded chance on the same
	// socket. Never resend, renew the deadline or replace the original send
	// sample: any response still has to satisfy the original native RTT bound.
	if runtime.GOOS == "darwin" && n == 0 && errors.Is(err, syscall.ECONNABORTED) && ctx.Err() == nil && time.Until(deadline) > 0 {
		n, err = conn.Read(raw[:])
		readCount, finalReadError = 2, authorityTimeReadError(err)
	}
	if err != nil {
		return authorityTimeMeasurement{}, &authorityTimeTransportError{err}
	}
	probe.Stage = "native_receive"
	received, err := p.sample()
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
	probe.Native = received.nanos
	probe.Stage = "packet"
	packet, err := parseAuthorityNTP(raw[:n], nonce)
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
	probe.Stage = "native_binding"
	if sent.boot == [32]byte{} || sent.process == [16]byte{} || sent.boot != received.boot || sent.process != received.process || received.nanos < sent.nanos {
		return authorityTimeMeasurement{}, errAuthorityTime
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return authorityTimeMeasurement{}, errAuthorityTimeClosed
	}
	if err := ctx.Err(); err != nil {
		return authorityTimeMeasurement{}, err
	}
	p.sequence = sequence
	probe.Stage = "measured"
	return authorityTimeMeasurement{p.source, conn.RemoteAddr().String(), sent, received, packet, request, [48]byte(raw[:48]), sequence}, nil
}

// Only copied classifications survive a Read; never expose net.OpError text,
// addresses or syscall strings. These facts do not control the receive path.
func authorityTimeReadError(err error) string {
	var timed net.Error
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, syscall.ECONNABORTED):
		return "aborted"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timed) && timed.Timeout():
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, net.ErrClosed):
		return "closed"
	default:
		return "other"
	}
}

func authorityTimeEndpoints(answers []netip.Addr, turn uint64) ([]string, error) {
	if len(answers) == 0 || len(answers) > 16 {
		return nil, errAuthorityTime
	}
	answers = slices.Clone(answers)
	for i, a := range answers {
		a = a.Unmap()
		if !a.IsValid() || a.IsUnspecified() || a.IsMulticast() || a.Zone() != "" {
			return nil, errAuthorityTime
		}
		answers[i] = a
	}
	slices.SortFunc(answers, func(a, b netip.Addr) int { return a.Compare(b) })
	answers = slices.Compact(answers)
	start := int(turn % uint64(len(answers)))
	result := make([]string, len(answers))
	for i := range answers {
		result[i] = net.JoinHostPort(answers[(start+i)%len(answers)].String(), "123")
	}
	return result, nil
}

func (p *authorityTimeProducer) close() {
	p.mu.Lock()
	p.closed = true
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Unlock()
	p.workers.Wait()
}

// This is only the completed attempt's copied diagnostic, never a time input.
func (p *authorityTimeProducer) probeObservation() decisiontrace.Event {
	probe, _ := p.completedProbe()
	return probe
}

func (p *authorityTimeProducer) completedProbe() (decisiontrace.Event, decisiontrace.StartupAttempt) {
	if p == nil {
		return decisiontrace.Event{}, decisiontrace.StartupAttempt{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastProbe, p.lastAttempt
}
