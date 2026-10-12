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
	ErrCapacity    = errors.New("membership publisher: TLS chain capacity unavailable")
	ErrJournal     = errors.New("membership publisher: journal durability uncertain")
)

const maxStateBytes = 1 << 20

// Config durations use Go's JSON integer nanoseconds. End bounds every new signature;
// CleanupBy separately budgets orderly runtime shutdown before certificates end.
type Config struct {
	Version            uint32
	End, CleanupBy     time.Time
	Interval, Lifetime time.Duration
	Streams            []Stream
}

// Stream Initial is the exact signed initial manifest (JSON []byte uses base64).
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
	capacity       time.Time
	last           time.Time
	failed, closed bool
	retainedOnly   bool
	started        time.Time
	clock          func() time.Time
	targetStatus   [][]TargetStatus
	failure        string
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
func Open(configPath, statePath, mode string) (*Publisher, error) {
	if mode != "init" && mode != "resume" {
		return nil, ErrState
	}
	return openOwned(configPath, statePath, mode, time.Now().UTC(), nil)
}

// advance-window opens only authenticated retained state; it cannot issue.
// New-period capacity is validated separately before its binding is committed.
func openOwned(configPath, statePath, mode string, now time.Time, additionalInputs []string) (_ *Publisher, err error) {
	if !canonical(statePath) {
		return nil, ErrState
	}
	raw, err := readPrivate(configPath, maxStateBytes)
	if err != nil {
		return nil, ErrState
	}
	p := &Publisher{path: statePath, files: systemFiles(), retainedOnly: mode == "advance-window", started: now, clock: time.Now}
	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()
	if decode(raw, &p.config) != nil {
		return nil, ErrState
	}
	canonicalConfig, marshalErr := json.Marshal(p.config)
	if marshalErr != nil {
		return nil, ErrState
	}
	p.state = journal{Version: 1, Binding: sha256.Sum256(canonicalConfig)}
	if err = p.loadSigners(); err != nil {
		return nil, err
	}
	for _, stream := range p.config.Streams {
		p.targetStatus = append(p.targetStatus, make([]TargetStatus, len(stream.Targets)))
	}
	if mode != "advance-window" {
		if !now.Add(2 * peerauth.ClockMargin).Before(p.config.End) {
			return nil, errors.Join(ErrState, ErrBudget)
		}
		if p.capacity, err = p.validateCapacity(now, p.config.CleanupBy); err != nil {
			return nil, err
		}
	}
	var targets, inputs []string
	inputs = append(inputs, configPath)
	inputs = append(inputs, additionalInputs...)
	for _, stream := range p.config.Streams {
		targets = append(targets, stream.Targets...)
		inputs = append(inputs, stream.KeyFile, stream.RootsFile)
		inputs = append(inputs, stream.Certificates...)
	}
	if !safePaths(append([]string{configPath}, additionalInputs...), inputs, statePath, targets) {
		return nil, ErrState
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
		if mode == "init" && !errors.Is(statErr, os.ErrNotExist) || mode != "init" && statErr != nil {
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
				previous, _, previousEnd, e := p.verify(i, c.Previous)
				if e != nil || previous+1 != v || previous < initial ||
					previous == initial && !bytes.Equal(c.Previous, p.config.Streams[i].Initial) ||
					previous > initial && previousEnd.After(p.config.End) {
					return nil, ErrState
				}
			} else if len(c.Previous) != 0 {
				return nil, ErrState
			}
		}
	}
	// Intact resume permits only the current candidate or its immediate saved
	// predecessor during partial distribution. Unknown/advanced targets stop it.
	if err = p.checkTargets(); err != nil {
		return nil, err
	}
	if mode == "resume" && p.files.retained(statePath) != nil {
		return nil, ErrState
	}
	return p, nil
}

// loadSigners checks immutable structure and signing authenticity, including
// for an expired old window. It does not validate TLS at an obsolete date.
func (p *Publisher) loadSigners() error {
	c := p.config
	if c.Version != 1 || len(c.Streams) != 2 || c.Interval < time.Second || c.Lifetime > peerauth.MaxMembershipLifetime || c.Lifetime <= 2*peerauth.ClockMargin || c.Interval >= (c.Lifetime-2*peerauth.ClockMargin)/2 || !validWindow(c) {
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
		p.failure = "journal_uncertain"
		return errors.Join(ErrState, ErrJournal)
	}
	if err = p.files.replace(p.path, raw); err != nil {
		p.failed = true
		p.failure = "journal_uncertain"
		return errors.Join(ErrState, ErrJournal)
	}
	p.raw = raw
	return nil
}

func (p *Publisher) checkTargets() error {
	for i, s := range p.config.Streams {
		for j, path := range s.Targets {
			raw, err := readPrivate(path, peerauth.MaxManifestBytes)
			c := p.state.Streams[i]
			if err != nil || !bytes.Equal(raw, c.Current) && (!c.Pending || !bytes.Equal(raw, c.Previous)) {
				p.observeTarget(i, j, nil, "operator_recovery_required")
				return ErrState
			}
			p.observeTarget(i, j, raw, "")
		}
	}
	return nil
}

// Step signs at most one new successor per stream. Failed targets do not block
// the other stream. An ambiguous journal write permanently stops this owner;
// only explicit intact resume may inspect its durable bytes. Old expiry alone
// does not prevent renewal. The operator's wall clock is never server authority.
func (p *Publisher) Step(now time.Time) (result error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() {
		if !p.failed {
			p.failure = FailureCategory(result)
		}
	}()
	if p.closed || p.failed || p.retainedOnly {
		return ErrState
	}
	if !p.last.IsZero() && now.Before(p.last) {
		p.failed = true
		p.failure = "clock_rollback"
		return ErrState
	}
	p.last = now
	if !now.Add(2 * peerauth.ClockMargin).Before(p.config.End) {
		return ErrBudget
	}
	raw, err := readPrivate(p.path, maxStateBytes)
	if err != nil || !bytes.Equal(raw, p.raw) {
		p.failed = true
		p.failure = "operator_recovery_required"
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
		for j, path := range s.Targets {
			observed, e := readPrivate(path, peerauth.MaxManifestBytes)
			if e != nil || !bytes.Equal(observed, c.Current) && !bytes.Equal(observed, c.Previous) {
				p.observeTarget(i, j, nil, "operator_recovery_required")
				complete = false
				continue
			}
			if bytes.Equal(observed, c.Current) {
				// Includes retry after a target rename succeeded but dir sync
				// failed. Readback alone must not count as durable delivery.
				e = p.files.retained(path)
			} else {
				e = p.files.replace(path, c.Current)
			}
			if e != nil {
				p.targetStatus[i][j].Failure = "delivery_retry"
				complete = false
			} else {
				p.observeTarget(i, j, c.Current, "")
				at := p.clock().UTC()
				p.targetStatus[i][j].LastDurableAt = &at
				p.targetStatus[i][j].LastDurableVersion = c.Version
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
	return p.RunWithStatus(ctx, nil, 0)
}

// RunWithStatus reports bounded, nonsecret observations every five seconds and
// on failure-category changes. The synchronous observer owns no authority.
// Its failure stops publication; it cannot silently disable expiry monitoring.
func (p *Publisher) RunWithStatus(ctx context.Context, observe func(Status) error, maintenanceLead time.Duration) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var reported time.Time
	previous := ""
	report := func(now time.Time, force bool) error {
		if observe == nil {
			return nil
		}
		snapshot := p.Status(now, maintenanceLead)
		if force || snapshot.Failure != previous || reported.IsZero() || now.Sub(reported) >= 5*time.Second {
			if err := observe(snapshot); err != nil {
				return ErrReporting
			}
			reported, previous = now, snapshot.Failure
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			p.mu.Lock()
			p.failure = "stopped"
			p.mu.Unlock()
			if e := report(p.clock().UTC(), true); e != nil {
				return e
			}
			return err
		}
		now := p.clock().UTC()
		err := p.Step(now)
		if e := report(p.clock().UTC(), false); e != nil {
			return e
		}
		if errors.Is(err, ErrBudget) {
			return nil
		}
		if err != nil && !errors.Is(err, ErrPublication) {
			return err
		}
		select {
		case <-ctx.Done():
			// Report termination at the top of the next iteration.
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
