//go:build lantern_test_time && (linux || darwin)

package main

// Cross-cutting test-only owner-window composition; no ordinary serving path.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// This public projection is evidence of the separately owned, already-opened
// publisher's finite certificate budget. It is NOT native authority: serving
// still admits only the genuine signed manifests below, with unchanged ten
// minute leases, profile bindings and durable production floors.
type controlledNativeRenewalCapacity struct {
	Schema                int       `json:"schema"`
	Kind                  string    `json:"kind"`
	Revision              string    `json:"revision"`
	Nonce                 string    `json:"nonce"`
	PlannedStop           time.Time `json:"planned_stop_at"`
	CleanupBy             time.Time `json:"cleanup_by"`
	PublisherEnd          time.Time `json:"publisher_end"`
	PublisherCleanupBy    time.Time `json:"publisher_cleanup_by"`
	TLSCapacityEnd        time.Time `json:"tls_capacity_end"`
	LeaseSeconds          int       `json:"lease_seconds"`
	RefreshSeconds        int       `json:"refresh_seconds"`
	TargetCount           int       `json:"target_count"`
	ControlProfileSHA256  string    `json:"control_profile_sha256"`
	DataDomainSHA256      string    `json:"data_domain_sha256"`
	ControlOperatorSHA256 string    `json:"control_operator_sha256"`
	DataOperatorSHA256    string    `json:"data_operator_sha256"`
}

func controlledNativeWholeCertificates(config provider.SecurityConfig, now, cleanup time.Time) error {
	var authority struct {
		Identity struct {
			Roots       string `json:"roots_pem"`
			Certificate string `json:"certificate_pem"`
		} `json:"identity"`
	}
	if config.ProvisionedPeer == nil || json.Unmarshal(config.AuthorityJSON, &authority) != nil {
		return errors.New("owner certificate authority unavailable")
	}
	certificates := []string{authority.Identity.Roots, authority.Identity.Certificate, config.TLS.CertPEM}
	for _, path := range []string{config.ProvisionedPeer.Identity.CAFile, config.ProvisionedPeer.Identity.CertFile} {
		raw, err := controlledNativeOwnerRead(path, 1<<20)
		if err != nil {
			return err
		}
		certificates = append(certificates, string(raw))
	}
	for _, raw := range certificates {
		count := 0
		for rest := []byte(raw); len(bytes.TrimSpace(rest)) != 0; {
			block, remaining := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return errors.New("owner certificate encoding refused")
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil || now.Add(-peerauth.ClockMargin).Before(cert.NotBefore) || !cleanup.Add(peerauth.ClockMargin).Before(cert.NotAfter) {
				return errors.New("owner whole certificate window insufficient")
			}
			count++
			rest = remaining
		}
		if count == 0 {
			return errors.New("owner complete certificate chain unavailable")
		}
	}
	return nil
}

func controlledNativeCertifiedBounds(config provider.SecurityConfig, observe func() (time.Time, time.Time, error)) (time.Time, time.Time, error) {
	low, high, err := observe()
	if err == nil {
		err = controlledNativeWholeCertificates(config, low, high)
	}
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return low, high, nil
}

func controlledNativeRenewalAdmission(input controlledNativeProviderInput, config provider.SecurityConfig, now time.Time) (controlledNativeLeaseAuthority, controlledNativeLeaseCut, error) {
	var cut controlledNativeLeaseCut
	authority, err := controlledNativeLeaseInputs(config)
	if err != nil {
		return authority, cut, err
	}
	if !controlledNativeProviderWindowShape(input) || input.OwnerWindow == nil || input.PlannedStop.Sub(now) < 15*time.Second || input.PlannedStop.Sub(now) > 30*time.Minute {
		return authority, cut, errors.New("owner finite window unavailable")
	}
	raw, err := controlledNativeOwnerRead(input.OwnerCapacity.Path, 16<<10)
	var capacity controlledNativeRenewalCapacity
	if err != nil || controlledGoogleDigest(raw) != input.OwnerCapacity.SHA256 || controlledNativeOwnerDecode(raw, &capacity) != nil {
		return authority, cut, errors.New("owner public renewal capacity unavailable")
	}
	if err := controlledNativeCapacityBinding(input, capacity, authority); err != nil {
		return authority, cut, err
	}
	if err := controlledNativeWholeCertificates(config, now, input.CleanupBy); err != nil {
		return authority, cut, err
	}
	cut, err = controlledNativeLeaseCurrent(authority, now)
	return authority, cut, err
}

func controlledNativeCapacityBinding(input controlledNativeProviderInput, capacity controlledNativeRenewalCapacity, authority controlledNativeLeaseAuthority) error {
	controlProfile, domain := authority.Profile.Digest(), authority.Domain.Digest()
	controlKey, dataKey := sha256.Sum256(authority.ControlKey), sha256.Sum256(authority.DataKey)
	if capacity.Schema != 1 || capacity.Kind != "controlled-native-provider-renewal-capacity" || capacity.Revision != input.Revision || capacity.Nonce != input.Nonce ||
		!capacity.PlannedStop.Equal(input.PlannedStop) || !capacity.CleanupBy.Equal(input.CleanupBy) || capacity.LeaseSeconds != 600 || capacity.RefreshSeconds != 60 || capacity.TargetCount != 3 ||
		!capacity.PublisherEnd.Equal(input.CleanupBy.Add(5*time.Second)) || !capacity.PublisherCleanupBy.Equal(input.CleanupBy.Add(10*time.Second)) ||
		!capacity.TLSCapacityEnd.After(capacity.PublisherCleanupBy.Add(peerauth.ClockMargin)) ||
		capacity.ControlProfileSHA256 != hex.EncodeToString(controlProfile[:]) || capacity.DataDomainSHA256 != hex.EncodeToString(domain[:]) ||
		capacity.ControlOperatorSHA256 != hex.EncodeToString(controlKey[:]) || capacity.DataOperatorSHA256 != hex.EncodeToString(dataKey[:]) {
		return errors.New("owner exact renewal capacity binding refused")
	}
	return nil
}

type controlledNativeLeaseMonitor struct {
	stop                    context.CancelFunc
	done                    chan struct{}
	file                    *os.File
	path                    string
	last                    controlledNativeLeaseCut
	observations            int
	err                     error
	closed                  bool
	first                   time.Time
	closedAt                time.Time
	authority               controlledNativeLeaseAuthority
	nativeEnd               chan chan error
	nativeOnce              sync.Once
	nativeErr               error
	nativeFrom, nativeUntil time.Time
	ownerStopSHA256         string
}

func controlledNativeMonitorLeases(ctx context.Context, cancelApp context.CancelFunc, input controlledNativeProviderInput, authority controlledNativeLeaseAuthority, first controlledNativeLeaseCut, native func() (time.Time, time.Time, error)) (*controlledNativeLeaseMonitor, error) {
	if native != nil {
		if err := controlledNativeOwnerControlDirectory(controlledNativeOwnerControl, false); err != nil {
			return nil, err
		}
	}
	path := "/state/controlled-native-provider-owner-leases-" + input.Mode + ".jsonl"
	return controlledNativeMonitorLeaseFile(ctx, cancelApp, path, authority, first, native)
}

func controlledNativeMonitorLeaseFile(ctx context.Context, cancelApp context.CancelFunc, path string, authority controlledNativeLeaseAuthority, first controlledNativeLeaseCut, native func() (time.Time, time.Time, error)) (*controlledNativeLeaseMonitor, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("owner create-only lease transcript unavailable")
	}
	// Continue checking leases through App teardown after its serving context is
	// canceled. The caller always stops and joins this monitor after workers join.
	monitorCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	m := &controlledNativeLeaseMonitor{stop: stop, done: make(chan struct{}), file: file, path: path, last: first,
		first: first.ObservedAt, authority: authority, nativeEnd: make(chan chan error)}
	appendCut := func(cut controlledNativeLeaseCut) error {
		if m.observations >= 128 || json.NewEncoder(file).Encode(cut) != nil || file.Sync() != nil {
			return errors.New("owner bounded lease transcript failed")
		}
		m.observations++
		return nil
	}
	if err := appendCut(first); err != nil {
		stop()
		file.Close()
		return nil, err
	}
	observe := func(final bool) error {
		now := time.Now().UTC()
		var next controlledNativeLeaseCut
		var err error
		if native != nil {
			low, high, err := native()
			if err != nil {
				return err
			}
			// Wall time is only the auxiliary event stamp during native serving.
			// Read/admit with the borrowed native bounds before any validity test.
			next, err = controlledNativeLeaseCurrent(authority, low)
			if err != nil {
				return err
			}
			next, err = controlledNativeLeaseVerifyBounds(authority, now, low, high, next.ControlManifest, next.DataManifest)
			if err != nil {
				return err
			}
		} else {
			next, err = controlledNativeLeaseCurrent(authority, now)
			if err != nil {
				return err
			}
		}
		if err := controlledNativeLeaseAdvance(m.last, next); err != nil {
			return err
		}
		if final || next.ControlSHA256 != m.last.ControlSHA256 || next.DataSHA256 != m.last.DataSHA256 {
			if err := appendCut(next); err != nil {
				return err
			}
		}
		m.last = next
		return nil
	}
	if native != nil {
		if err := observe(true); err != nil {
			stop()
			file.Close()
			return nil, err
		}
		m.nativeFrom = m.last.ObservedAt
	}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case response := <-m.nativeEnd:
				m.err = observe(true)
				if m.err == nil {
					m.nativeUntil = m.last.ObservedAt
					native = nil
				}
				response <- m.err
				if m.err != nil {
					cancelApp()
					return
				}
			case <-monitorCtx.Done():
				m.err = observe(true)
				return
			case <-ticker.C:
				if m.err = observe(false); m.err != nil {
					cancelApp()
					return
				}
			}
		}
	}()
	return m, nil
}

func (m *controlledNativeLeaseMonitor) finishNative() error {
	m.nativeOnce.Do(func() {
		response := make(chan error, 1)
		select {
		case m.nativeEnd <- response:
			m.nativeErr = <-response
		case <-m.done:
			m.nativeErr = m.err
		}
	})
	return m.nativeErr
}

// App still owns every worker and the original security owner. This test-only
// wrapper observes the last live native bounds before ALWAYS delegating the
// original lifecycle; its monitor then continues only auxiliary cleanup cuts.
type controlledNativeObservedLifecycle struct {
	securityLifecycle
	monitor *controlledNativeLeaseMonitor
	stop    func() (string, error)
}

func (o controlledNativeObservedLifecycle) finalWitness() error {
	var sha string
	var err error
	if o.stop == nil {
		err = errors.New("owner causal stop reader unavailable")
	} else {
		sha, err = o.stop()
	}
	if err == nil {
		o.monitor.ownerStopSHA256 = sha
	}
	// Even a missing/wrong record cannot skip monitor or original owner close.
	return errors.Join(err, o.monitor.finishNative())
}
func (o controlledNativeObservedLifecycle) Shutdown() error {
	return errors.Join(o.finalWitness(), o.securityLifecycle.Shutdown())
}
func (o controlledNativeObservedLifecycle) Close() error {
	return errors.Join(o.finalWitness(), o.securityLifecycle.Close())
}

func (m *controlledNativeLeaseMonitor) close() error {
	if m.closed {
		return m.err
	}
	m.closedAt = time.Now().UTC()
	m.stop()
	<-m.done
	m.err = errors.Join(m.err, m.file.Sync(), m.file.Close())
	m.closed = true
	return m.err
}

func (m *controlledNativeLeaseMonitor) fields(input controlledNativeProviderInput) (map[string]any, error) {
	if err := m.close(); err != nil {
		return nil, err
	}
	raw, err := controlledNativeOwnerRead(m.path, 4<<20)
	if err != nil {
		return nil, err
	}
	count, err := controlledNativeLeaseTranscript(raw, m.authority, m.first, m.closedAt)
	if err != nil || count != m.observations {
		return nil, errors.New("owner final signed interval transcript refused")
	}
	through := m.last.ControlExpires
	if m.last.DataExpires.Before(through) {
		through = m.last.DataExpires
	}
	through = through.Add(-peerauth.ClockMargin)
	if !time.Now().UTC().Before(through) {
		return nil, errors.New("owner terminal lease coverage expired")
	}
	fields := map[string]any{"owner_window": input.OwnerWindow, "owner_capacity_sha256": input.OwnerCapacity.SHA256,
		"owner_lease_transcript_sha256": controlledGoogleDigest(raw), "owner_lease_observations": m.observations,
		"owner_lease_through": through, "owner_lease_time_scope": "host-auxiliary", "owner_native_lease_observations": 0}
	if input.Mode == "resume" {
		if !controlledGoogleHex(m.ownerStopSHA256, 32) {
			return nil, errors.New("owner causal stop acknowledgement absent")
		}
		count, err := controlledNativeNativeInterval(raw, m.nativeFrom, m.nativeUntil)
		if err != nil {
			return nil, err
		}
		fields["owner_lease_time_scope"] = "native-serving-with-host-cleanup"
		fields["owner_native_lease_from"], fields["owner_native_lease_until"], fields["owner_native_lease_observations"] = m.nativeFrom, m.nativeUntil, count
		fields["owner_stop_sha256"] = m.ownerStopSHA256
	}
	return fields, nil
}

func TestControlledNativeProviderOwnerWindowContracts(t *testing.T) {
	input, _ := unitControlledNativeProvider()
	decode := func(value controlledNativeProviderInput) error {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		_, err = decodeControlledNativeProviderInput(raw)
		return err
	}
	for _, seconds := range []int{15, 540} {
		input.LifetimeSeconds = seconds
		if err := decode(input); err != nil {
			t.Fatal("static bound changed", err)
		}
	}
	input.LifetimeSeconds = 541
	if decode(input) == nil {
		t.Fatal("implicit extended window accepted")
	}
	input.LifetimeSeconds = controlledNativeOwnerSeconds
	input.OwnerWindow = &controlledNativeOwnerWindow{Mode: "membership-renewal", LeaseSeconds: 600, RefreshSeconds: 60}
	input.OwnerCapacity = &controlledNativeOwnerCapacityRef{Path: "/owner-capacity.json", SHA256: strings.Repeat("d", 64)}
	if err := decode(input); err != nil {
		t.Fatal("explicit fixed owner window refused", err)
	}
	for name, mutate := range map[string]func(*controlledNativeProviderInput){
		"intermediate lifetime": func(v *controlledNativeProviderInput) { v.LifetimeSeconds-- },
		"larger lease":          func(v *controlledNativeProviderInput) { v.OwnerWindow.LeaseSeconds++ },
		"different refresh":     func(v *controlledNativeProviderInput) { v.OwnerWindow.RefreshSeconds++ },
		"unknown mode":          func(v *controlledNativeProviderInput) { v.OwnerWindow.Mode = "fallback" },
		"missing capacity":      func(v *controlledNativeProviderInput) { v.OwnerCapacity = nil },
		"foreign capacity path": func(v *controlledNativeProviderInput) { v.OwnerCapacity.Path = "/operator/capacity.json" },
		"static with capacity":  func(v *controlledNativeProviderInput) { v.OwnerWindow = nil; v.LifetimeSeconds = 540 },
		"extended cleanup":      func(v *controlledNativeProviderInput) { v.CleanupBy = v.CleanupBy.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := input
			window, capacity := *input.OwnerWindow, *input.OwnerCapacity
			copy.OwnerWindow, copy.OwnerCapacity = &window, &capacity
			mutate(&copy)
			if decode(copy) == nil {
				t.Fatal("invalid owner authority shape accepted")
			}
		})
	}
}

func TestControlledNativeProviderWholeOwnerCapacity(t *testing.T) {
	authority, _ := unitControlledNativeLeases(t)
	input, _ := unitControlledNativeProvider()
	profile, domain := authority.Profile.Digest(), authority.Domain.Digest()
	controlKey, dataKey := sha256.Sum256(authority.ControlKey), sha256.Sum256(authority.DataKey)
	capacity := controlledNativeRenewalCapacity{Schema: 1, Kind: "controlled-native-provider-renewal-capacity", Revision: input.Revision, Nonce: input.Nonce,
		PlannedStop: input.PlannedStop, CleanupBy: input.CleanupBy, PublisherEnd: input.CleanupBy.Add(5 * time.Second), PublisherCleanupBy: input.CleanupBy.Add(10 * time.Second),
		TLSCapacityEnd: input.CleanupBy.Add(time.Hour), LeaseSeconds: 600, RefreshSeconds: 60, TargetCount: 3,
		ControlProfileSHA256: hex.EncodeToString(profile[:]), DataDomainSHA256: hex.EncodeToString(domain[:]),
		ControlOperatorSHA256: hex.EncodeToString(controlKey[:]), DataOperatorSHA256: hex.EncodeToString(dataKey[:])}
	if err := controlledNativeCapacityBinding(input, capacity, authority); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*controlledNativeRenewalCapacity){
		"short operator horizon": func(v *controlledNativeRenewalCapacity) { v.PublisherEnd = input.PlannedStop },
		"short certificate horizon": func(v *controlledNativeRenewalCapacity) {
			v.TLSCapacityEnd = v.PublisherCleanupBy.Add(peerauth.ClockMargin)
		},
		"foreign profile":  func(v *controlledNativeRenewalCapacity) { v.ControlProfileSHA256 = strings.Repeat("0", 64) },
		"foreign operator": func(v *controlledNativeRenewalCapacity) { v.DataOperatorSHA256 = strings.Repeat("0", 64) },
		"foreign cohort":   func(v *controlledNativeRenewalCapacity) { v.Nonce = strings.Repeat("0", 64) },
		"extended lease":   func(v *controlledNativeRenewalCapacity) { v.LeaseSeconds++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := capacity
			mutate(&bad)
			if controlledNativeCapacityBinding(input, bad, authority) == nil {
				t.Fatal("invalid capacity binding accepted")
			}
		})
	}
	now := input.PlannedStop.Add(-30 * time.Minute)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{73}, ed25519.SeedSize))
	certificate := func(end time.Time) string {
		t.Helper()
		template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: end, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	long := certificate(input.CleanupBy.Add(time.Hour))
	dir := t.TempDir()
	rootPath, leafPath := filepath.Join(dir, "root.pem"), filepath.Join(dir, "leaf.pem")
	for _, path := range []string{rootPath, leafPath} {
		if err := os.WriteFile(path, []byte(long), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := provider.SecurityConfig{TLS: provider.SecurityTLSConfig{CertPEM: long}, ProvisionedPeer: &provider.PeerPlaneConfig{}}
	config.ProvisionedPeer.Identity.CAFile, config.ProvisionedPeer.Identity.CertFile = rootPath, leafPath
	config.AuthorityJSON, _ = json.Marshal(map[string]any{"identity": map[string]string{"roots_pem": long, "certificate_pem": long}})
	if err := controlledNativeWholeCertificates(config, now, input.CleanupBy); err != nil {
		t.Fatal("whole original certificate interval refused", err)
	}
	if low, high, err := controlledNativeCertifiedBounds(config, func() (time.Time, time.Time, error) { return now, now.Add(time.Millisecond), nil }); err != nil || !low.Equal(now) || !high.Equal(now.Add(time.Millisecond)) {
		t.Fatal("native callback certificate interval refused", low, high, err)
	}
	if _, _, err := controlledNativeCertifiedBounds(config, func() (time.Time, time.Time, error) { return now, input.CleanupBy.Add(time.Hour), nil }); err == nil {
		t.Fatal("native callback accepted endpoint outside immutable certificate coverage")
	}
	config.TLS.CertPEM = certificate(input.CleanupBy.Add(peerauth.ClockMargin))
	if controlledNativeWholeCertificates(config, now, input.CleanupBy) == nil {
		t.Fatal("short ordinary TLS certificate accepted for whole owner interval")
	}
}

type controlledNativeUnitLifecycle struct {
	calls int
	close func()
}

func (o *controlledNativeUnitLifecycle) Close() error    { o.calls++; o.close(); return nil }
func (o *controlledNativeUnitLifecycle) Shutdown() error { return o.Close() }

func TestControlledNativeProviderNativeLeaseMonitor(t *testing.T) {
	for _, scenario := range []string{"normal", "native-failure", "missing-stop", "wrong-stop"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				time.Sleep(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Sub(time.Now().UTC()))
				authority, sign := unitControlledNativeLeases(t)
				dir := t.TempDir()
				controlDir := filepath.Join(dir, "owner-control")
				if err := os.Mkdir(controlDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := controlledNativeOwnerControlDirectory(controlDir, false); err != nil {
					t.Fatal(err)
				}
				input, _ := unitControlledNativeProvider()
				input.PlannedStop = time.Now().UTC().Add(30 * time.Minute)
				input.CleanupBy = input.PlannedStop.Add(20 * time.Second)
				input.OwnerWindow = &controlledNativeOwnerWindow{Mode: "membership-renewal", LeaseSeconds: 600, RefreshSeconds: 60}
				input.OwnerCapacity = &controlledNativeOwnerCapacityRef{SHA256: strings.Repeat("a", 64)}
				request := controlledNativeOwnerStop{Schema: 1, Kind: "controlled-native-provider-owner-stop", Revision: input.Revision, Nonce: input.Nonce, PlannedStop: input.PlannedStop, CleanupBy: input.CleanupBy}
				writeStop := func(value controlledNativeOwnerStop) {
					t.Helper()
					raw, _ := json.Marshal(value)
					if err := os.WriteFile(filepath.Join(controlDir, "stop.json"), raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				authority.ControlPath = filepath.Join(dir, "control.json")
				authority.DataPath = filepath.Join(dir, "data.json")
				publish := func(version uint64) {
					t.Helper()
					control, data := sign(version, time.Now().UTC(), peerauth.MaxMembershipLifetime)
					for path, raw := range map[string][]byte{authority.ControlPath: control, authority.DataPath: data} {
						if err := os.WriteFile(path+".next", raw, 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(path+".next", path); err != nil {
							t.Fatal(err)
						}
					}
				}
				publish(1)
				first, err := controlledNativeLeaseCurrent(authority, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				var closed, unavailable atomic.Bool
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				monitor, err := controlledNativeMonitorLeaseFile(ctx, cancel, filepath.Join(dir, "transcript.jsonl"), authority, first, func() (time.Time, time.Time, error) {
					if closed.Load() {
						t.Error("native clock sampled after original owner closed")
						return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
					}
					if unavailable.Load() {
						return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
					}
					now := time.Now().UTC()
					return now, now.Add(time.Millisecond), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				defer monitor.close()
				original := &controlledNativeUnitLifecycle{close: func() { closed.Store(true) }}
				wrapped := controlledNativeObservedLifecycle{securityLifecycle: original, monitor: monitor, stop: func() (string, error) {
					return controlledNativeOwnerStopAcknowledgement(controlDir, input.Revision, input.Nonce, input.PlannedStop, input.CleanupBy)
				}}
				if scenario != "normal" {
					if scenario == "native-failure" {
						writeStop(request)
						unavailable.Store(true)
						time.Sleep(2 * time.Second)
						if ctx.Err() == nil {
							t.Fatal("native witness failure did not cancel App")
						}
					} else if scenario == "wrong-stop" {
						bad := request
						bad.Nonce = strings.Repeat("0", 64)
						writeStop(bad)
					}
					if wrapped.Shutdown() == nil || original.calls != 1 {
						t.Fatal("failed native witness/causal stop skipped original shutdown", original.calls)
					}
					return
				}
				for minute := 1; minute <= 30; minute++ {
					time.Sleep(time.Minute)
					publish(uint64(minute + 1))
				}
				writeStop(request)
				if err := wrapped.Shutdown(); err != nil || original.calls != 1 {
					t.Fatal("original owned shutdown", original.calls, err)
				}
				// Cleanup continues without sampling a closed native owner.
				time.Sleep(2 * time.Second)
				fields, err := monitor.fields(input)
				stopRaw, _ := os.ReadFile(filepath.Join(controlDir, "stop.json"))
				if err != nil || fields["owner_stop_sha256"] != controlledGoogleDigest(stopRaw) || fields["owner_lease_time_scope"] != "native-serving-with-host-cleanup" || fields["owner_native_lease_observations"].(int) < 2 {
					t.Fatal("native serving/auxiliary cleanup proof", fields, err)
				}
			})
		})
	}
}

func TestControlledNativeProviderMonitorUsesOwnedNativeBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Sub(time.Now().UTC()))
		hostStart := time.Now()
		nativeStart := hostStart.UTC().Add(-time.Hour)
		authority, sign := unitControlledNativeLeases(t)
		control, data := sign(1, nativeStart, peerauth.MaxMembershipLifetime)
		dir := t.TempDir()
		authority.ControlPath = filepath.Join(dir, "control.json")
		authority.DataPath = filepath.Join(dir, "data.json")
		for path, raw := range map[string][]byte{authority.ControlPath: control, authority.DataPath: data} {
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		first, err := controlledNativeLeaseVerify(authority, nativeStart, control, data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := controlledNativeLeaseCurrent(authority, hostStart.UTC()); err == nil {
			t.Fatal("fixture wall point unexpectedly inside native lease")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var calls atomic.Int64
		monitor, err := controlledNativeMonitorLeaseFile(ctx, cancel, filepath.Join(dir, "transcript.jsonl"), authority, first, func() (time.Time, time.Time, error) {
			calls.Add(1)
			low := nativeStart.Add(time.Since(hostStart))
			return low, low.Add(time.Millisecond), nil
		})
		if err != nil {
			t.Fatal("native-valid monitor rejected auxiliary wall point", err)
		}
		defer monitor.close()
		time.Sleep(2 * time.Second)
		if err := monitor.close(); err != nil || ctx.Err() != nil || calls.Load() < 2 || monitor.last.NativeLow == nil {
			t.Fatal("monitor used auxiliary wall time for native admission", calls.Load(), err, ctx.Err())
		}
	})
}
