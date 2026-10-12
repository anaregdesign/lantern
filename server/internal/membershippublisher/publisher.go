// Package membershippublisher implements a finite, external operator. Its
// signing keys never enter the serving runtime. Publication is not admission:
// each consumer still applies its own durable floor and native-time checks.
package membershippublisher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

var (
	ErrState       = errors.New("membership publisher: invalid, missing or uncertain operator state")
	ErrPublication = errors.New("membership publisher: incomplete publication; exact candidate retained")
	ErrBudget      = errors.New("membership publisher: declared publication budget ended")
)

const maxStateBytes = 1 << 20

// Durations use Go's JSON integer nanoseconds. End bounds every new signature;
// CleanupBy separately budgets orderly runtime shutdown before certificates end.
type Config struct {
	Version            uint32
	End, CleanupBy     time.Time
	Interval, Lifetime time.Duration
	Streams            []Stream
}

// Initial is the exact signed initial manifest (JSON []byte uses base64).
// Certificates must cover every pinned workload. KeyFile is private PKCS#8 PEM.
// Targets are existing private manifest inputs, not server checkpoint files.
type Stream struct {
	Kind                  string
	KeyFile               string
	RootsFile             string
	Initial               []byte
	Targets, Certificates []string
}

type candidate struct {
	Version           uint64
	Digest            [32]byte
	Current, Previous []byte
	Pending           bool
}
type journal struct {
	Version uint32
	Binding [32]byte
	Streams []candidate
}
type signer struct {
	key     ed25519.PrivateKey
	control peerauth.ControlManifest
	data    peerauth.Manifest
}

// Publisher holds stable process leases for the journal and every target. A
// different journal path cannot create a second publisher for the same inputs.
type Publisher struct {
	mu             sync.Mutex
	config         Config
	state          journal
	raw            []byte
	path           string
	leases         []*mutationlog.FileWALLease
	signers        []signer
	files          fileOps
	last           time.Time
	failed, closed bool
}

func decode(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return ErrState
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrState
	}
	return nil
}

// Open takes an explicit init/resume choice. init requires never-used journal
// and publication leases plus exact initial inputs; resume requires all state.
// A missing journal or sidecar is never repaired by guessing a fresh revision.
func Open(configPath, statePath, mode string) (_ *Publisher, err error) {
	if mode != "init" && mode != "resume" || !canonical(statePath) {
		return nil, ErrState
	}
	raw, err := readPrivate(configPath, maxStateBytes)
	if err != nil {
		return nil, ErrState
	}
	p := &Publisher{path: statePath, files: systemFiles()}
	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()
	if decode(raw, &p.config) != nil {
		return nil, ErrState
	}
	canonicalConfig, _ := json.Marshal(p.config)
	p.state = journal{Version: 1, Binding: sha256.Sum256(canonicalConfig)}
	if err = p.validateConfig(time.Now().UTC()); err != nil {
		return nil, err
	}
	// Prevent target aliases of immutable inputs, journal or lock paths.
	protected := []string{configPath, statePath, statePath + ".lease"}
	var targets []string
	for _, s := range p.config.Streams {
		protected = append(protected, s.KeyFile, s.RootsFile)
		protected = append(protected, s.Certificates...)
		targets = append(targets, s.Targets...)
		for _, target := range s.Targets {
			protected = append(protected, target+".publisher.lease")
		}
	}
	for i, path := range targets {
		for _, other := range append(append([]string{}, protected...), targets[:i]...) {
			if path == other {
				return nil, ErrState
			}
			a, e1 := os.Stat(path)
			b, e2 := os.Stat(other)
			if e1 == nil && e2 == nil && os.SameFile(a, b) {
				return nil, ErrState
			}
		}
	}
	sort.Strings(targets)
	lockPaths := []string{statePath}
	for _, target := range targets {
		lockPaths = append(lockPaths, target+".publisher")
	}
	required := []string{statePath}
	for _, path := range lockPaths {
		required = append(required, path+".lease")
	}
	for _, path := range required {
		_, statErr := os.Lstat(path)
		if mode == "init" && !errors.Is(statErr, os.ErrNotExist) || mode == "resume" && statErr != nil {
			return nil, ErrState
		}
	}
	for _, path := range lockPaths {
		lease, e := mutationlog.AcquireFileWALLease(path)
		if e != nil {
			return nil, ErrState
		}
		p.leases = append(p.leases, lease)
	}
	if mode == "init" {
		for i, s := range p.config.Streams {
			v, _, _, e := p.verify(i, s.Initial)
			if e != nil {
				return nil, ErrState
			}
			for _, target := range s.Targets {
				b, e := readPrivate(target, peerauth.MaxManifestBytes)
				if e != nil || !bytes.Equal(b, s.Initial) {
					return nil, ErrState
				}
			}
			p.state.Streams = append(p.state.Streams, candidate{Version: v, Digest: sha256.Sum256(s.Initial), Current: s.Initial})
		}
		if err = p.save(); err != nil {
			return nil, err
		}
	} else {
		p.raw, err = readPrivate(statePath, maxStateBytes)
		if err != nil {
			return nil, ErrState
		}
		var saved journal
		if decode(p.raw, &saved) != nil || saved.Version != 1 || saved.Binding != p.state.Binding || len(saved.Streams) != len(p.signers) {
			return nil, ErrState
		}
		canonical, _ := json.Marshal(saved)
		if !bytes.Equal(canonical, p.raw) {
			return nil, ErrState
		}
		p.state = saved
		for i, c := range saved.Streams {
			v, _, end, e := p.verify(i, c.Current)
			if e != nil || v != c.Version || c.Digest != sha256.Sum256(c.Current) {
				return nil, ErrState
			}
			initial, _, _, _ := p.verify(i, p.config.Streams[i].Initial)
			if v < initial || v == initial && !bytes.Equal(c.Current, p.config.Streams[i].Initial) || v > initial && end.After(p.config.End) {
				return nil, ErrState
			}
			if c.Pending {
				previous, _, _, e := p.verify(i, c.Previous)
				if e != nil || previous+1 != v {
					return nil, ErrState
				}
			} else if len(c.Previous) != 0 {
				return nil, ErrState
			}
		}
		if err = syncRetained(statePath); err != nil {
			return nil, ErrState
		}
	}
	// Intact resume permits only the current candidate or its immediate saved
	// predecessor during partial distribution. Unknown/advanced targets stop it.
	if err = p.checkTargets(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Publisher) validateConfig(now time.Time) error {
	c := p.config
	if c.Version != 1 || len(c.Streams) != 2 || c.Interval < time.Second || c.Lifetime > peerauth.MaxMembershipLifetime || c.Lifetime <= 2*peerauth.ClockMargin || c.Interval >= (c.Lifetime-2*peerauth.ClockMargin)/2 || !c.End.After(now) || !c.CleanupBy.After(c.End) {
		return ErrState
	}
	kinds := map[string]bool{}
	for _, s := range c.Streams {
		if kinds[s.Kind] || s.Kind != "control" && s.Kind != "data" || len(s.Targets) == 0 || len(s.Targets) > 128 || len(s.Initial) > peerauth.MaxManifestBytes {
			return ErrState
		}
		kinds[s.Kind] = true
		keyPEM, e := readPrivate(s.KeyFile, 16<<10)
		if e != nil {
			return ErrState
		}
		block, rest := pem.Decode(keyPEM)
		clear(keyPEM)
		if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
			return ErrState
		}
		key, e := x509.ParsePKCS8PrivateKey(block.Bytes)
		clear(block.Bytes)
		k, ok := key.(ed25519.PrivateKey)
		if e != nil || !ok {
			return ErrState
		}
		p.signers = append(p.signers, signer{key: k})
		i := len(p.signers) - 1
		// Decode a hint solely to select the pinned domain/lineage, then verify
		// the exact signed envelope before retaining any of its configuration.
		var envelope struct {
			Payload   json.RawMessage
			Signature []byte
		}
		if decode(s.Initial, &envelope) != nil {
			return ErrState
		}
		if s.Kind == "control" {
			if decode(envelope.Payload, &p.signers[i].control) != nil {
				return ErrState
			}
		} else {
			if decode(envelope.Payload, &p.signers[i].data) != nil {
				return ErrState
			}
		}
		if _, _, _, e = p.verify(i, s.Initial); e != nil {
			return ErrState
		}
		members := p.signers[i].data.Members
		trust := p.signers[i].data.Domain.TrustDigest
		if s.Kind == "control" {
			trust = p.signers[i].control.Profile.TrustDigest
			for _, v := range p.signers[i].control.Profile.Voters {
				members = append(members, v.Workload)
			}
		}
		if len(s.Certificates) != len(members) {
			return ErrState
		}
		rootPEM, e := readPrivate(s.RootsFile, 1<<20)
		roots := x509.NewCertPool()
		if e != nil || sha256.Sum256(rootPEM) != trust || !roots.AppendCertsFromPEM(rootPEM) {
			return ErrState
		}
		covered := map[[32]byte]bool{}
		for _, path := range s.Certificates {
			raw, e := readPrivate(path, peerauth.MaxManifestBytes)
			if e != nil {
				return ErrState
			}
			block, rest := pem.Decode(raw)
			if block == nil || block.Type != "CERTIFICATE" {
				return ErrState
			}
			cert, e := x509.ParseCertificate(block.Bytes)
			if e != nil || cert.NotBefore.After(now) || !cert.NotAfter.After(c.CleanupBy.Add(peerauth.ClockMargin)) {
				return ErrState
			}
			intermediates := x509.NewCertPool()
			if len(bytes.TrimSpace(rest)) != 0 && !intermediates.AppendCertsFromPEM(rest) {
				return ErrState
			}
			for _, at := range []time.Time{now, c.CleanupBy.Add(peerauth.ClockMargin)} {
				for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
					if _, e := cert.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{usage}}); e != nil {
						return ErrState
					}
				}
			}
			digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			if covered[digest] || len(cert.URIs) != 1 {
				return ErrState
			}
			found := false
			for _, m := range members {
				if m.SPKI == digest && m.Identity == cert.URIs[0].String() {
					found = true
				}
			}
			if !found {
				return ErrState
			}
			covered[digest] = true
		}
		for _, path := range s.Targets {
			if !canonical(path) {
				return ErrState
			}
		}
	}
	return nil
}

func (p *Publisher) verify(i int, raw []byte) (uint64, time.Time, time.Time, error) {
	s := p.signers[i]
	pub := s.key.Public().(ed25519.PublicKey)
	if p.config.Streams[i].Kind == "control" {
		m, e := peerauth.VerifyControlManifest(raw, pub, s.control.Profile.Lineage)
		if e != nil || m.Profile.Digest() != s.control.Profile.Digest() {
			return 0, time.Time{}, time.Time{}, ErrState
		}
		return m.Version, m.IssuedAt, m.ExpiresAt, nil
	}
	m, e := peerauth.VerifyManifest(raw, pub, s.data.Domain)
	if e != nil || !reflect.DeepEqual(m.Members, s.data.Members) {
		return 0, time.Time{}, time.Time{}, ErrState
	}
	return m.Version, m.IssuedAt, m.ExpiresAt, nil
}

func (p *Publisher) save() error {
	raw, err := json.Marshal(p.state)
	if err != nil || len(raw) > maxStateBytes {
		p.failed = true
		return ErrState
	}
	if err = p.files.replace(p.path, raw); err != nil {
		p.failed = true
		return ErrState
	}
	p.raw = raw
	return nil
}

func (p *Publisher) checkTargets() error {
	for i, s := range p.config.Streams {
		for _, path := range s.Targets {
			raw, err := readPrivate(path, peerauth.MaxManifestBytes)
			c := p.state.Streams[i]
			if err != nil || !bytes.Equal(raw, c.Current) && !(c.Pending && bytes.Equal(raw, c.Previous)) {
				return ErrState
			}
		}
	}
	return nil
}

// Step signs at most one new successor per stream. Failed targets do not block
// the other stream. An ambiguous journal write permanently stops this owner;
// only explicit intact resume may inspect its durable bytes. Old expiry alone
// does not prevent renewal. The operator's wall clock is never server authority.
func (p *Publisher) Step(now time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.failed {
		return ErrState
	}
	if !p.last.IsZero() && now.Before(p.last) {
		p.failed = true
		return ErrState
	}
	p.last = now
	if !now.Add(2 * peerauth.ClockMargin).Before(p.config.End) {
		return ErrBudget
	}
	raw, err := readPrivate(p.path, maxStateBytes)
	if err != nil || !bytes.Equal(raw, p.raw) {
		p.failed = true
		return ErrState
	}
	if p.checkTargets() != nil {
		return ErrState
	}
	incomplete := false
	for i, s := range p.config.Streams {
		c := &p.state.Streams[i]
		_, issued, _, _ := p.verify(i, c.Current)
		if !c.Pending && !now.Before(issued.Add(p.config.Interval)) {
			end := now.Add(p.config.Lifetime)
			if end.After(p.config.End) {
				end = p.config.End
			}
			var next []byte
			if s.Kind == "control" {
				m := p.signers[i].control
				m.Version = c.Version + 1
				m.IssuedAt = now.UTC()
				m.ExpiresAt = end.UTC()
				next, err = peerauth.SignControlManifest(m, p.signers[i].key)
			} else {
				m := p.signers[i].data
				m.Version = c.Version + 1
				m.IssuedAt = now.UTC()
				m.ExpiresAt = end.UTC()
				next, err = peerauth.SignManifest(m, p.signers[i].key)
			}
			if err != nil {
				p.failed = true
				return ErrState
			}
			*c = candidate{Version: c.Version + 1, Digest: sha256.Sum256(next), Current: next, Previous: c.Current, Pending: true}
			if err = p.save(); err != nil {
				return err
			} // Before the FIRST target.
		}
		if !c.Pending {
			continue
		}
		complete := true
		for _, path := range s.Targets {
			observed, e := readPrivate(path, peerauth.MaxManifestBytes)
			if e != nil || !bytes.Equal(observed, c.Current) && !bytes.Equal(observed, c.Previous) {
				complete = false
				continue
			}
			if bytes.Equal(observed, c.Current) {
				// Includes retry after a target rename succeeded but dir sync
				// failed. Readback alone must not count as durable delivery.
				e = syncRetained(path)
			} else {
				e = p.files.replace(path, c.Current)
			}
			if e != nil {
				complete = false
			}
		}
		if complete {
			c.Pending = false
			c.Previous = nil
			if err = p.save(); err != nil {
				return err
			}
		} else {
			incomplete = true
		}
	}
	if incomplete {
		return ErrPublication
	}
	return nil
}

// Run has a finite deadline and retries partial publication without resigning.
// Cancellation waits for any synchronous file operation; it never reports a
// partially flushed record as success or leaves detached publication goroutines.
func (p *Publisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := p.Step(time.Now().UTC())
		if errors.Is(err, ErrBudget) {
			return nil
		}
		if err != nil && !errors.Is(err, ErrPublication) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *Publisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var err error
	for i := len(p.leases) - 1; i >= 0; i-- {
		err = errors.Join(err, p.leases[i].Close())
	}
	for _, s := range p.signers {
		clear(s.key)
	}
	return err
}
