//go:build lantern_test_time && (linux || darwin)

package main

// Cross-cutting test-only owner-window composition; no ordinary serving path.
import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/provider"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func controlledNativeOwnerRead(path string, maximum int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("owner private input unavailable")
	}
	defer file.Close()
	return controlledNativeOwnerReadCaptured(file, maximum)
}

func controlledNativeOwnerFileInfo(file *os.File, maximum int64) (os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("owner bounded private input refused")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity.Uid != uint32(os.Getuid()) || identity.Nlink != 1 {
		return nil, errors.New("owner private input identity refused")
	}
	return info, nil
}

func controlledNativeOwnerReadCaptured(file *os.File, maximum int64) ([]byte, error) {
	info, err := controlledNativeOwnerFileInfo(file, maximum)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, errors.New("owner private input changed")
	}
	return raw, nil
}

// Only the two renewable signed membership inputs use this reader. An atomic
// replacement may unlink an already-open old inode; its captured bytes remain
// subject to signature, profile, native-time and renewal-continuity validation.
// Immutable operator inputs continue to require exactly one link above.
func controlledNativeLeaseRead(path string, maximum int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("owner signed lease input unavailable")
	}
	defer file.Close()
	return controlledNativeLeaseReadCaptured(file, maximum)
}

func controlledNativeLeaseFileIdentity(identity *syscall.Stat_t) bool {
	return identity != nil && identity.Uid == uint32(os.Getuid()) && (identity.Nlink == 0 || identity.Nlink == 1)
}

func controlledNativeLeaseReadCaptured(file *os.File, maximum int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("owner bounded signed lease input refused")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !controlledNativeLeaseFileIdentity(identity) {
		return nil, errors.New("owner signed lease input identity refused")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, errors.New("owner signed lease input changed")
	}
	return raw, nil
}

func controlledNativeOwnerDecode(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 64<<10 || oidc.ValidateJSON(raw) != nil || decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("owner exact JSON refused")
	}
	return nil
}

type controlledNativeLeaseAuthority struct {
	ControlPath string
	ControlKey  ed25519.PublicKey
	Profile     peerauth.ControlProfile
	DataPath    string
	DataKey     ed25519.PublicKey
	Domain      peerauth.Domain
	Members     []peerauth.Member
}

func controlledNativeLeaseInputs(config provider.SecurityConfig) (controlledNativeLeaseAuthority, error) {
	var raw struct {
		Membership struct {
			ManifestFile string                  `json:"manifest_file"`
			PublicKey    []byte                  `json:"public_key"`
			Profile      peerauth.ControlProfile `json:"profile"`
		} `json:"membership"`
	}
	if config.ProvisionedPeer == nil || json.Unmarshal(config.AuthorityJSON, &raw) != nil || len(raw.Membership.PublicKey) != ed25519.PublicKeySize {
		return controlledNativeLeaseAuthority{}, errors.New("owner native authority unavailable")
	}
	peer := config.ProvisionedPeer.Identity
	return controlledNativeLeaseAuthority{ControlPath: raw.Membership.ManifestFile, ControlKey: ed25519.PublicKey(raw.Membership.PublicKey),
		Profile: raw.Membership.Profile, DataPath: peer.ManifestFile, DataKey: ed25519.PublicKey(peer.ExpectedOperatorKey[:]),
		Domain: peer.ExpectedDomain, Members: slices.Clone(peer.FixedMembers)}, nil
}

type controlledNativeLeaseCut struct {
	ObservedAt           time.Time  `json:"observed_at"`
	ControlVersion       uint64     `json:"control_version"`
	ControlIssued        time.Time  `json:"control_issued_at"`
	ControlExpires       time.Time  `json:"control_expires_at"`
	ControlSHA256        string     `json:"control_sha256"`
	DataVersion          uint64     `json:"data_version"`
	DataIssued           time.Time  `json:"data_issued_at"`
	DataExpires          time.Time  `json:"data_expires_at"`
	DataSHA256           string     `json:"data_sha256"`
	ControlProfileSHA256 string     `json:"control_profile_sha256"`
	DataDomainSHA256     string     `json:"data_domain_sha256"`
	ControlManifest      []byte     `json:"control_manifest"`
	DataManifest         []byte     `json:"data_manifest"`
	NativeLow            *time.Time `json:"native_low,omitempty"`
	NativeHigh           *time.Time `json:"native_high,omitempty"`
}

func controlledNativeLeaseCurrent(authority controlledNativeLeaseAuthority, now time.Time) (controlledNativeLeaseCut, error) {
	var cut controlledNativeLeaseCut
	controlRaw, err := controlledNativeLeaseRead(authority.ControlPath, peerauth.MaxManifestBytes)
	if err != nil {
		return cut, err
	}
	dataRaw, err := controlledNativeLeaseRead(authority.DataPath, peerauth.MaxManifestBytes)
	if err != nil {
		return cut, err
	}
	return controlledNativeLeaseVerify(authority, now, controlRaw, dataRaw)
}

func controlledNativeLeaseVerify(authority controlledNativeLeaseAuthority, now time.Time, controlRaw, dataRaw []byte) (controlledNativeLeaseCut, error) {
	cut, err := controlledNativeLeaseAuthenticate(authority, now, controlRaw, dataRaw)
	if err != nil {
		return cut, err
	}
	valid := func(issued, expires time.Time) bool {
		// Identical time predicate to native membership admission. In particular
		// a freshly published lease is not spuriously rejected for its first2s.
		return !now.Before(issued.Add(-peerauth.ClockMargin)) && now.Add(peerauth.ClockMargin).Before(expires)
	}
	if !valid(cut.ControlIssued, cut.ControlExpires) || !valid(cut.DataIssued, cut.DataExpires) {
		return controlledNativeLeaseCut{}, errors.New("owner current signed lease expired or future")
	}
	return cut, nil
}

// Authentication is separate from selection: a future candidate is still
// checked in full before it can be remembered or deferred by the monitor.
func controlledNativeLeaseAuthenticate(authority controlledNativeLeaseAuthority, now time.Time, controlRaw, dataRaw []byte) (controlledNativeLeaseCut, error) {
	var cut controlledNativeLeaseCut
	control, err := peerauth.VerifyControlManifest(controlRaw, authority.ControlKey, authority.Profile.Lineage)
	if err != nil || control.Profile.Digest() != authority.Profile.Digest() {
		return cut, errors.New("owner signed control profile refused")
	}
	data, err := peerauth.VerifyManifest(dataRaw, authority.DataKey, authority.Domain)
	if err != nil || !slices.Equal(data.Members, authority.Members) {
		return cut, errors.New("owner signed data membership refused")
	}
	profile, domain := control.Profile.Digest(), data.Domain.Digest()
	return controlledNativeLeaseCut{ObservedAt: now, ControlVersion: control.Version, ControlIssued: control.IssuedAt,
		ControlExpires: control.ExpiresAt, ControlSHA256: controlledGoogleDigest(controlRaw), DataVersion: data.Version,
		DataIssued: data.IssuedAt, DataExpires: data.ExpiresAt, DataSHA256: controlledGoogleDigest(dataRaw),
		ControlProfileSHA256: hex.EncodeToString(profile[:]), DataDomainSHA256: hex.EncodeToString(domain[:]),
		ControlManifest: slices.Clone(controlRaw), DataManifest: slices.Clone(dataRaw)}, nil
}

func controlledNativeLeaseAdvance(previous, next controlledNativeLeaseCut) error {
	if next.ObservedAt.Before(previous.ObservedAt) {
		return errors.New("owner lease observation rollback")
	}
	valid := func(oldVersion, version uint64, oldDigest, digest string, oldExpires, issued time.Time) bool {
		return version == oldVersion && digest == oldDigest || version > oldVersion && issued.Before(oldExpires)
	}
	if !valid(previous.ControlVersion, next.ControlVersion, previous.ControlSHA256, next.ControlSHA256, previous.ControlExpires, next.ControlIssued) ||
		!valid(previous.DataVersion, next.DataVersion, previous.DataSHA256, next.DataSHA256, previous.DataExpires, next.DataIssued) {
		return errors.New("owner signed lease rollback, equivocation or coverage gap")
	}
	return nil
}

func controlledNativeLeaseVerifyBounds(authority controlledNativeLeaseAuthority, observed, low, high time.Time, control, data []byte) (controlledNativeLeaseCut, error) {
	if low.IsZero() || high.Before(low) {
		return controlledNativeLeaseCut{}, errors.New("owner native time bounds unavailable")
	}
	cut, err := controlledNativeLeaseVerify(authority, low, control, data)
	if err != nil {
		return cut, err
	}
	if _, err := controlledNativeLeaseVerify(authority, high, control, data); err != nil {
		return controlledNativeLeaseCut{}, err
	}
	cut.ObservedAt, cut.NativeLow, cut.NativeHigh = observed, &low, &high
	return cut, nil
}

// Both cuts must already have passed authentication. Selection never changes a
// signed time: only a strictly newer, not-yet-current stream can leave its
// previously selected bytes in use. The returned bytes must still cover the
// entire current native interval under the original verification predicate.
func controlledNativeLeaseSelectBounds(authority controlledNativeLeaseAuthority, observed, low, high time.Time, previous, candidate controlledNativeLeaseCut) (controlledNativeLeaseCut, error) {
	control, data := candidate.ControlManifest, candidate.DataManifest
	deferFuture := func(version, previousVersion uint64, issued, expires time.Time) bool {
		return version > previousVersion && low.Before(issued.Add(-peerauth.ClockMargin)) &&
			high.Add(peerauth.ClockMargin).Before(expires)
	}
	if deferFuture(candidate.ControlVersion, previous.ControlVersion, candidate.ControlIssued, candidate.ControlExpires) {
		control = previous.ControlManifest
	}
	if deferFuture(candidate.DataVersion, previous.DataVersion, candidate.DataIssued, candidate.DataExpires) {
		data = previous.DataManifest
	}
	return controlledNativeLeaseVerifyBounds(authority, observed, low, high, control, data)
}

type controlledNativeTranscriptLimits struct {
	Records  int
	RowBytes int
	Bytes    int64
}

func controlledNativeStaticTranscriptLimits() controlledNativeTranscriptLimits {
	return controlledNativeTranscriptLimits{Records: 128, RowBytes: 64 << 10, Bytes: 4 << 20}
}

func controlledNativeTranscriptLimitsFor(input controlledNativeProviderInput) (controlledNativeTranscriptLimits, error) {
	if input.OwnerWindow == nil || input.OwnerWindow.Mode != "unattended-maintenance-renewal" {
		return controlledNativeStaticTranscriptLimits(), nil
	}
	if !controlledNativeRenewalWindowShape(input.OwnerWindow, input.LifetimeSeconds) {
		return controlledNativeTranscriptLimits{}, errors.New("maintenance transcript horizon refused")
	}
	// Two independently observed signed streams per refresh; setup, final native
	// and cleanup boundaries remain included without an unbounded global quota.
	records := 2*((input.LifetimeSeconds+controlledNativeCleanupSeconds(input.OwnerWindow)+59)/60) + 16
	return controlledNativeTranscriptLimits{Records: records, RowBytes: 32 << 10, Bytes: int64(records) * (32 << 10)}, nil
}

func (l controlledNativeTranscriptLimits) valid() bool {
	return l.Records > 0 && l.RowBytes >= 4096 && l.RowBytes <= 64<<10 && l.Bytes > 0
}

func controlledNativeLeaseTranscript(raw []byte, authority controlledNativeLeaseAuthority, from, through time.Time) (int, error) {
	return controlledNativeLeaseTranscriptReader(bytes.NewReader(raw), authority, from, through, controlledNativeStaticTranscriptLimits())
}

func controlledNativeLeaseTranscriptReader(reader io.Reader, authority controlledNativeLeaseAuthority, from, through time.Time, limits controlledNativeTranscriptLimits) (int, error) {
	if !limits.valid() || through.Before(from) {
		return 0, errors.New("owner bounded lease interval transcript refused")
	}
	limited := &io.LimitedReader{R: reader, N: limits.Bytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), limits.RowBytes)
	count := 0
	var first, previous controlledNativeLeaseCut
	for scanner.Scan() {
		var cut controlledNativeLeaseCut
		if count == limits.Records || controlledNativeOwnerDecode(scanner.Bytes(), &cut) != nil {
			return count, errors.New("owner exact signed lease transcript refused")
		}
		var verified controlledNativeLeaseCut
		var err error
		if cut.NativeLow != nil || cut.NativeHigh != nil {
			if cut.NativeLow == nil || cut.NativeHigh == nil {
				return count, errors.New("owner partial native witness refused")
			}
			verified, err = controlledNativeLeaseVerifyBounds(authority, cut.ObservedAt, *cut.NativeLow, *cut.NativeHigh, cut.ControlManifest, cut.DataManifest)
		} else {
			verified, err = controlledNativeLeaseVerify(authority, cut.ObservedAt, cut.ControlManifest, cut.DataManifest)
		}
		expected, _ := json.Marshal(verified)
		actual, _ := json.Marshal(cut)
		if err != nil || !bytes.Equal(expected, actual) || count != 0 && controlledNativeLeaseAdvance(previous, cut) != nil {
			return count, errors.New("owner genuine signed lease transcript refused")
		}
		if count == 0 {
			first = cut
		}
		previous = cut
		count++
	}
	if scanner.Err() != nil || limited.N == 0 || count == 0 || first.ObservedAt.After(from) || previous.ObservedAt.Before(through) ||
		!through.Add(peerauth.ClockMargin).Before(previous.ControlExpires) || !through.Add(peerauth.ClockMargin).Before(previous.DataExpires) {
		return count, errors.New("owner whole actual lease interval not covered")
	}
	return count, nil
}

func controlledNativeNativeInterval(raw []byte, from, until time.Time) (int, error) {
	return controlledNativeNativeIntervalReader(bytes.NewReader(raw), from, until, controlledNativeStaticTranscriptLimits())
}

func controlledNativeNativeIntervalReader(reader io.Reader, from, until time.Time, limits controlledNativeTranscriptLimits) (int, error) {
	if !limits.valid() {
		return 0, errors.New("owner native interval bounds refused")
	}
	limited := &io.LimitedReader{R: reader, N: limits.Bytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), limits.RowBytes)
	count, ended := 0, false
	var first, last time.Time
	for scanner.Scan() {
		var cut controlledNativeLeaseCut
		if count >= limits.Records || controlledNativeOwnerDecode(scanner.Bytes(), &cut) != nil {
			return count, errors.New("owner native interval decode refused")
		}
		if cut.NativeLow == nil && cut.NativeHigh == nil {
			if count != 0 {
				ended = true
			}
			continue
		}
		if cut.NativeLow == nil || cut.NativeHigh == nil || ended {
			return count, errors.New("owner disjoint native witness refused")
		}
		if count == 0 {
			first = cut.ObservedAt
		}
		last = cut.ObservedAt
		count++
	}
	if scanner.Err() != nil || limited.N == 0 || count < 2 || !first.Equal(from) || !last.Equal(until) {
		return count, errors.New("owner whole native serving interval absent")
	}
	return count, nil
}

// This fixture signs genuine bounded memberships at injected instants. It
// owns no provider, listener, external clock source or serving configuration.
func unitControlledNativeLeases(t *testing.T) (controlledNativeLeaseAuthority, func(uint64, time.Time, time.Duration) ([]byte, []byte)) {
	t.Helper()
	controlKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{71}, ed25519.SeedSize))
	dataKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{72}, ed25519.SeedSize))
	profile := peerauth.ControlProfile{Version: 1, Lineage: peerauth.ControlLineage{Deployment: [16]byte{1}, Instance: [16]byte{2}},
		NamespaceFormat: keyspace.Version, ProtocolScope: [32]byte{3}, TrustDigest: [32]byte{4}}
	var members []peerauth.Member
	for i := byte(1); i <= 3; i++ {
		member := peerauth.Member{ID: [16]byte{i}, Identity: fmt.Sprintf("spiffe://owner.test/node-%d", i),
			SPKI: [32]byte{i}, Origin: fmt.Sprintf("https://127.0.0.1:%d", 16380+int(i))}
		members = append(members, member)
		profile.Voters = append(profile.Voters, peerauth.ControlVoter{Workload: member, Voter: uint32(i), Key: [32]byte{i}, Proposer: true})
	}
	domain := peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: keyspace.Version, AuthMode: "oidc",
		SecurityGeneration: [16]byte{5}, TrustDigest: [32]byte{4}, CurrentProfile: "current-v2:" + strings.Repeat("06", 32)}
	authority := controlledNativeLeaseAuthority{ControlKey: controlKey.Public().(ed25519.PublicKey), Profile: profile,
		DataKey: dataKey.Public().(ed25519.PublicKey), Domain: domain, Members: members}
	sign := func(version uint64, issued time.Time, lifetime time.Duration) ([]byte, []byte) {
		t.Helper()
		control, err := peerauth.SignControlManifest(peerauth.ControlManifest{Version: version, Profile: profile, IssuedAt: issued, ExpiresAt: issued.Add(lifetime)}, controlKey)
		if err != nil {
			t.Fatal(err)
		}
		data, err := peerauth.SignManifest(peerauth.Manifest{Version: version, Domain: domain, IssuedAt: issued, ExpiresAt: issued.Add(lifetime), Members: members}, dataKey)
		if err != nil {
			t.Fatal(err)
		}
		return control, data
	}
	return authority, sign
}

func TestControlledNativeProviderSignedRenewalTranscript(t *testing.T) {
	authority, sign := unitControlledNativeLeases(t)
	from := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, duration := range []struct {
		name    string
		seconds int
	}{{"maintenance streaming two hours", 7200}, {"maintenance maximum seven days replay", 604800}} {
		t.Run(duration.name, func(t *testing.T) {
			input, _ := unitControlledNativeProvider()
			input.OwnerWindow = &controlledNativeOwnerWindow{Mode: "unattended-maintenance-renewal", LeaseSeconds: 600, RefreshSeconds: 60}
			input.LifetimeSeconds = duration.seconds
			input.Mode = "resume"
			limits, err := controlledNativeTranscriptLimitsFor(input)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "closed-leases.jsonl")
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.New()
			count := 0
			var last controlledNativeLeaseCut
			var lastRaw []byte
			var lastOffset int64
			var size int64
			appendCut := func(control, data []byte, at time.Time) {
				t.Helper()
				cut, err := controlledNativeLeaseVerifyBounds(authority, at, at, at.Add(time.Millisecond), control, data)
				if err != nil {
					t.Fatal("genuine advancing native cut", err)
				}
				raw, err := json.Marshal(cut)
				raw = append(raw, '\n')
				if err != nil || len(raw) > limits.RowBytes {
					t.Fatal("three-member canonical row exceeds admitted bound", len(raw), err)
				}
				if n, err := file.Write(raw); err != nil || n != len(raw) {
					t.Fatal("write fixture", err)
				}
				hash.Write(raw)
				count++
				last, lastRaw, lastOffset = cut, raw, size
				size += int64(len(raw))
			}
			control, previousData := sign(1, from, peerauth.MaxMembershipLifetime)
			appendCut(control, previousData, from)
			for minute := 1; minute <= duration.seconds/60; minute++ {
				at := from.Add(time.Duration(minute) * time.Minute)
				control, data := sign(uint64(minute+1), at, peerauth.MaxMembershipLifetime)
				// Independent atomic per-stream publication may expose one new stream
				// before the other. Both genuine cuts must remain admitted.
				appendCut(control, previousData, at)
				appendCut(control, data, at)
				previousData = data
			}
			if err := errors.Join(file.Sync(), file.Close()); err != nil {
				t.Fatal(err)
			}
			through := from.Add(time.Duration(duration.seconds) * time.Second)
			m := controlledNativeLeaseMonitor{path: path, authority: authority, first: from, closedAt: through, nativeFrom: from, nativeUntil: through, last: last}
			start := time.Now()
			digest, observed, native, err := m.replay(input)
			elapsed := time.Since(start)
			if err != nil || digest != hex.EncodeToString(hash.Sum(nil)) || observed != count || native != count || count <= 128 {
				t.Fatal("bounded captured-FD streaming replay", observed, native, count, err)
			}
			t.Logf("maintenance replay seconds=%d rows=%d bytes=%d elapsed=%s", duration.seconds, count, size, elapsed)
			if duration.seconds != 7200 {
				return
			}
			open := func() *os.File {
				t.Helper()
				f, e := os.Open(path)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { f.Close() })
				return f
			}
			if _, err := controlledNativeLeaseTranscriptReader(open(), authority, from, through, controlledNativeStaticTranscriptLimits()); err == nil {
				t.Fatal("legacy128-row profile consumed maintenance evidence")
			}
			short := limits
			short.Bytes = size - 1
			if _, err := controlledNativeLeaseTranscriptReader(open(), authority, from, through, short); err == nil {
				t.Fatal("maintenance byte cap ignored")
			}
			if _, err := controlledNativeNativeIntervalReader(open(), from, through.Add(time.Second), limits); err == nil {
				t.Fatal("missing native interval end admitted")
			}
			aux := last
			aux.NativeLow = nil
			aux.NativeHigh = nil
			auxRaw, _ := json.Marshal(aux)
			if _, err := controlledNativeNativeIntervalReader(bytes.NewReader(append(auxRaw, '\n')), from, through, limits); err == nil {
				t.Fatal("absent native block admitted")
			}
			oversized := strings.Repeat(" ", limits.RowBytes) + string(lastRaw)
			if _, err := controlledNativeLeaseTranscriptReader(strings.NewReader(oversized), authority, from, through, limits); err == nil {
				t.Fatal("maintenance line cap ignored")
			}
			bad := last
			bad.DataSHA256 = strings.Repeat("0", 64)
			badRaw, _ := json.Marshal(bad)
			badRaw = append(badRaw, '\n')
			if len(badRaw) != len(lastRaw) {
				t.Fatal("late corruption fixture changed size")
			}
			changed, err := os.OpenFile(path, os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = changed.Seek(lastOffset, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			if _, err = changed.Write(badRaw); err != nil {
				t.Fatal(err)
			}
			if err = changed.Close(); err != nil {
				t.Fatal(err)
			}
			if processed, err := controlledNativeLeaseTranscriptReader(open(), authority, from, through, limits); err == nil || processed != count-1 {
				t.Fatal("late signed metadata corruption not refused at its actual cut", processed, err)
			}
		})
	}

	t.Run("captured atomically replaced signed inputs", func(t *testing.T) {
		oldControl, oldData := sign(1, from, peerauth.MaxMembershipLifetime)
		newControl, newData := sign(2, from.Add(time.Minute), peerauth.MaxMembershipLifetime)
		current := authority
		current.ControlPath = filepath.Join(t.TempDir(), "control.json")
		current.DataPath = filepath.Join(t.TempDir(), "data.json")
		var captured [][]byte
		for _, input := range []struct {
			path     string
			previous []byte
			renewed  []byte
		}{{current.ControlPath, oldControl, newControl}, {current.DataPath, oldData, newData}} {
			if err := os.WriteFile(input.path, input.previous, 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(input.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { file.Close() })
			if err := os.WriteFile(input.path+".next", input.renewed, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(input.path+".next", input.path); err != nil {
				t.Fatal(err)
			}
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if info.Sys().(*syscall.Stat_t).Nlink != 0 {
				t.Fatal("atomic replacement did not unlink captured old inode")
			}
			if _, err := controlledNativeOwnerReadCaptured(file, peerauth.MaxManifestBytes); err == nil {
				t.Fatal("immutable input accepted an unlinked descriptor")
			}
			raw, err := controlledNativeLeaseReadCaptured(file, peerauth.MaxManifestBytes)
			if err != nil || !bytes.Equal(raw, input.previous) {
				t.Fatal("captured signed generation lost during replacement", err)
			}
			captured = append(captured, raw)
		}
		before, err := controlledNativeLeaseVerify(authority, from.Add(time.Minute), captured[0], captured[1])
		if err != nil || before.ControlVersion != 1 || before.DataVersion != 1 {
			t.Fatal("captured generation did not retain genuine authority", err)
		}
		after, err := controlledNativeLeaseCurrent(current, from.Add(time.Minute))
		if err != nil || after.ControlVersion != 2 || after.DataVersion != 2 || controlledNativeLeaseAdvance(before, after) != nil {
			t.Fatal("renewed paths did not verify with exact continuity", err)
		}
	})
	t.Run("unsafe renewable input refused", func(t *testing.T) {
		control, _ := sign(1, from, peerauth.MaxMembershipLifetime)
		for _, scenario := range []string{"hardlink", "mode", "symlink", "size", "empty"} {
			t.Run(scenario, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "control.json")
				if err := os.WriteFile(path, control, 0600); err != nil {
					t.Fatal(err)
				}
				maximum := int64(peerauth.MaxManifestBytes)
				switch scenario {
				case "hardlink":
					if err := os.Link(path, path+".alias"); err != nil {
						t.Fatal(err)
					}
				case "mode":
					if err := os.Chmod(path, 0644); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(path, path+".alias"); err != nil {
						t.Fatal(err)
					}
					path += ".alias"
				case "size":
					maximum = int64(len(control) - 1)
				case "empty":
					if err := os.Truncate(path, 0); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := controlledNativeLeaseRead(path, maximum); err == nil {
					t.Fatal("unsafe renewed membership input accepted")
				}
			})
		}
		// A different UID requires privilege to create on disk. Exercise the same
		// descriptor-identity predicate with a copied real Stat_t, without chown.
		path := filepath.Join(t.TempDir(), "identity.json")
		if err := os.WriteFile(path, control, 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		identity := *info.Sys().(*syscall.Stat_t)
		identity.Uid++
		if controlledNativeLeaseFileIdentity(&identity) || controlledNativeLeaseFileIdentity(nil) {
			t.Fatal("foreign or unavailable descriptor identity accepted")
		}
	})
	var transcript bytes.Buffer
	var cuts []controlledNativeLeaseCut
	for minute := 0; minute <= 30; minute++ {
		now := from.Add(time.Duration(minute) * time.Minute)
		control, data := sign(uint64(minute+1), now, peerauth.MaxMembershipLifetime)
		cut, err := controlledNativeLeaseVerify(authority, now, control, data)
		if err != nil {
			t.Fatal("genuine renewed lease refused", err)
		}
		cuts = append(cuts, cut)
		if err := json.NewEncoder(&transcript).Encode(cut); err != nil {
			t.Fatal(err)
		}
	}
	through := from.Add(30 * time.Minute)
	if count, err := controlledNativeLeaseTranscript(transcript.Bytes(), authority, from, through); err != nil || count != 31 {
		t.Fatal("thirty minutes of overlapping genuine ten-minute leases refused", count, err)
	}
	// The same last lease cannot stand in for the missing original interval.
	last, _ := json.Marshal(cuts[len(cuts)-1])
	if _, err := controlledNativeLeaseTranscript(last, authority, from, through); err == nil {
		t.Fatal("missing initial coverage accepted")
	}
	if _, err := controlledNativeLeaseTranscript(transcript.Bytes(), authority, from, through.Add(time.Second)); err == nil {
		t.Fatal("unobserved terminal interval accepted")
	}
	for name, mutate := range map[string]func(*controlledNativeLeaseCut){
		"forged metadata":          func(v *controlledNativeLeaseCut) { v.ControlVersion++ },
		"forged digest":            func(v *controlledNativeLeaseCut) { v.DataSHA256 = strings.Repeat("0", 64) },
		"tampered signed envelope": func(v *controlledNativeLeaseCut) { v.ControlManifest = append(slices.Clone(v.ControlManifest), ' ') },
	} {
		t.Run(name, func(t *testing.T) {
			var raw bytes.Buffer
			for i, original := range cuts {
				if i == 15 {
					mutate(&original)
				}
				if err := json.NewEncoder(&raw).Encode(original); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := controlledNativeLeaseTranscript(raw.Bytes(), authority, from, through); err == nil {
				t.Fatal("nongenuine transcript accepted")
			}
		})
	}
	for name, bad := range map[string]controlledNativeLeaseAuthority{
		"foreign profile":    func() controlledNativeLeaseAuthority { v := authority; v.Profile.ProtocolScope[0]++; return v }(),
		"foreign generation": func() controlledNativeLeaseAuthority { v := authority; v.Domain.SecurityGeneration[0]++; return v }(),
		"foreign members": func() controlledNativeLeaseAuthority {
			v := authority
			v.Members = slices.Clone(v.Members)
			v.Members[0].ID[0]++
			return v
		}(),
		"foreign signer": func() controlledNativeLeaseAuthority {
			v := authority
			v.ControlKey = ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
			return v
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := controlledNativeLeaseTranscript(transcript.Bytes(), bad, from, through); err == nil {
				t.Fatal("foreign authority accepted")
			}
		})
	}
	control, data := sign(1, from, peerauth.MaxMembershipLifetime)
	for _, now := range []time.Time{from.Add(-peerauth.ClockMargin - time.Nanosecond), from.Add(peerauth.MaxMembershipLifetime - peerauth.ClockMargin)} {
		if _, err := controlledNativeLeaseVerify(authority, now, control, data); err == nil {
			t.Fatal("noncurrent signed lease accepted")
		}
	}
	for name, next := range map[string]controlledNativeLeaseCut{
		"rollback":     cuts[0],
		"equivocation": func() controlledNativeLeaseCut { v := cuts[1]; v.DataSHA256 = cuts[0].DataSHA256; return v }(),
		"coverage gap": func() controlledNativeLeaseCut { v := cuts[2]; v.ControlIssued = cuts[1].ControlExpires; return v }(),
	} {
		t.Run(name, func(t *testing.T) {
			if controlledNativeLeaseAdvance(cuts[1], next) == nil {
				t.Fatal("invalid lease transition accepted")
			}
		})
	}
}

func TestControlledNativeProviderNativeBoundsLeaseVerification(t *testing.T) {
	authority, sign := unitControlledNativeLeases(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	control, data := sign(1, now, peerauth.MaxMembershipLifetime)
	// observed_at is deliberately outside the lease: it is auxiliary host
	// metadata and cannot replace the actual owned native bounds for admission.
	cut, err := controlledNativeLeaseVerifyBounds(authority, now.Add(time.Hour), now, now.Add(time.Millisecond), control, data)
	if err != nil || cut.NativeLow == nil || !cut.NativeLow.Equal(now) {
		t.Fatal("native witness replaced with host time", err)
	}
	// A genuine native refresh may refine an overlapping uncertainty interval
	// and LOWER its high endpoint. The owned native clock rejects contradictory
	// anchors; replay must not invent endpoint-monotonicity as another premise.
	previous, err := controlledNativeLeaseVerifyBounds(authority, now, now, now.Add(50*time.Millisecond), control, data)
	if err != nil {
		t.Fatal(err)
	}
	narrowed, err := controlledNativeLeaseVerifyBounds(authority, now.Add(5*time.Millisecond), now.Add(10*time.Millisecond), now.Add(40*time.Millisecond), control, data)
	if err != nil || controlledNativeLeaseAdvance(previous, narrowed) != nil {
		t.Fatal("valid native uncertainty refinement refused", err)
	}
	var transcript bytes.Buffer
	for _, cut := range []controlledNativeLeaseCut{previous, narrowed} {
		if err := json.NewEncoder(&transcript).Encode(cut); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := controlledNativeLeaseTranscript(transcript.Bytes(), authority, now, narrowed.ObservedAt); err != nil || count != 2 {
		t.Fatal("genuine narrowed native interval transcript refused", count, err)
	}
	for _, bounds := range [][2]time.Time{{now.Add(-peerauth.ClockMargin - time.Nanosecond), now}, {now, now.Add(peerauth.MaxMembershipLifetime - peerauth.ClockMargin)}, {now.Add(time.Millisecond), now}, {time.Time{}, now}} {
		if _, err := controlledNativeLeaseVerifyBounds(authority, now, bounds[0], bounds[1], control, data); err == nil {
			t.Fatal("invalid native whole bounds accepted")
		}
	}
	t.Run("future renewal retains only whole-interval-valid selected bytes", func(t *testing.T) {
		oldControl, oldData := sign(1, now.Add(-time.Minute), peerauth.MaxMembershipLifetime)
		newControl, newData := sign(2, now, peerauth.MaxMembershipLifetime)
		low, high := now.Add(-3*time.Second), now.Add(3*time.Second)
		previous, err := controlledNativeLeaseVerifyBounds(authority, now, low, high, oldControl, oldData)
		if err != nil {
			t.Fatal("previous genuine lease must cover the whole native interval", err)
		}
		if _, err := controlledNativeLeaseVerifyBounds(authority, now, low, high, newControl, newData); err == nil {
			t.Fatal("three-second lower-bound lag must refuse the newly issued lease")
		}
		candidate, err := controlledNativeLeaseAuthenticate(authority, now, newControl, newData)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := controlledNativeLeaseSelectBounds(authority, now, low, high, previous, candidate)
		if err != nil || selected.ControlVersion != 1 || selected.DataVersion != 1 ||
			!bytes.Equal(selected.ControlManifest, oldControl) || !bytes.Equal(selected.DataManifest, oldData) {
			t.Fatal("future candidate replaced current signed bytes", err)
		}
		adopted, err := controlledNativeLeaseSelectBounds(authority, now.Add(time.Second), low.Add(time.Second), high.Add(time.Second), selected, candidate)
		if err != nil || adopted.ControlVersion != 2 || adopted.DataVersion != 2 {
			t.Fatal("eligible genuine candidate did not replace the previous bytes", err)
		}
		// A future candidate cannot extend an expired selected lease or excuse
		// an invalid native interval. No generic time-error retry is allowed.
		shortControl, shortData := sign(1, now.Add(-time.Minute), 64*time.Second)
		short, err := controlledNativeLeaseAuthenticate(authority, now, shortControl, shortData)
		if err != nil {
			t.Fatal(err)
		}
		for name, previous := range map[string]controlledNativeLeaseCut{"expired previous": short, "future previous": candidate} {
			t.Run(name, func(t *testing.T) {
				if _, err := controlledNativeLeaseSelectBounds(authority, now, low, high, previous, candidate); err == nil {
					t.Fatal("noncurrent previous lease excused a future candidate")
				}
			})
		}
		for _, bounds := range [][2]time.Time{{{}, high}, {high, low}, {low, now.Add(peerauth.MaxMembershipLifetime)}} {
			if _, err := controlledNativeLeaseSelectBounds(authority, now, bounds[0], bounds[1], previous, candidate); err == nil {
				t.Fatal("future candidate excused invalid bounds or expiry")
			}
		}
	})
}
