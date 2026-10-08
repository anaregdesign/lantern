package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

var (
	errAuthorityTimeBusy   = errors.New("time measurement already in progress")
	errAuthorityTimeClosed = errors.New("time measurement producer closed")
)

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
// retries, no background goroutine, no cached success after loss, no OS writes.
// A future background owner must own refresh/backoff and qualified invalidation.
type authorityTimeProducer struct {
	mu           sync.Mutex
	closed, busy bool
	cancel       context.CancelFunc
	workers      sync.WaitGroup
	sequence     uint64
	source       authorityTimeSource
	sample       func() (authorityTimeStamp, error)
}

func (p *authorityTimeProducer) measure(ctx context.Context) (authorityTimeMeasurement, error) {
	return p.exchange(ctx, net.JoinHostPort(p.source.host, "123"))
}

// The address seam permits local UDP fault tests. Production composition, when
// qualified, must use measure and the exact configured source, never this seam.
func (p *authorityTimeProducer) exchange(ctx context.Context, address string) (authorityTimeMeasurement, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return authorityTimeMeasurement{}, errAuthorityTimeClosed
	}
	if p.busy {
		p.mu.Unlock()
		return authorityTimeMeasurement{}, errAuthorityTimeBusy
	}
	if ctx == nil || p.sample == nil || p.source.host == "" || p.source.configuration == [32]byte{} || p.sequence == ^uint64(0) {
		p.mu.Unlock()
		return authorityTimeMeasurement{}, errAuthorityTime
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	p.cancel, p.busy = cancel, true
	sequence := p.sequence + 1
	p.workers.Add(1)
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		p.cancel, p.busy = nil, false
		p.mu.Unlock()
		p.workers.Done()
	}()

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
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if err != nil {
		return authorityTimeMeasurement{}, err
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
	sent, err := p.sample()
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
	n, err := conn.Write(request[:])
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
	if n != len(request) {
		return authorityTimeMeasurement{}, errAuthorityTime
	}
	// One extra byte makes oversized/truncated datagrams rejectable.
	var raw [49]byte
	n, err = conn.Read(raw[:])
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
	received, err := p.sample()
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
	packet, err := parseAuthorityNTP(raw[:n], nonce)
	if err != nil {
		return authorityTimeMeasurement{}, err
	}
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
	return authorityTimeMeasurement{p.source, conn.RemoteAddr().String(), sent, received, packet, request, [48]byte(raw[:48]), sequence}, nil
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
