//go:build lantern_test_time && (linux || darwin)

package main

// Cross-cutting test-only owner-window composition; no ordinary serving path.
import (
	"bufio"
	"bytes"
	"crypto/ed25519"
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
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("owner bounded private input refused")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity.Uid != uint32(os.Getuid()) || identity.Nlink != 1 {
		return nil, errors.New("owner private input identity refused")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, errors.New("owner private input changed")
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
	controlRaw, err := controlledNativeOwnerRead(authority.ControlPath, peerauth.MaxManifestBytes)
	if err != nil {
		return cut, err
	}
	dataRaw, err := controlledNativeOwnerRead(authority.DataPath, peerauth.MaxManifestBytes)
	if err != nil {
		return cut, err
	}
	return controlledNativeLeaseVerify(authority, now, controlRaw, dataRaw)
}

func controlledNativeLeaseVerify(authority controlledNativeLeaseAuthority, now time.Time, controlRaw, dataRaw []byte) (controlledNativeLeaseCut, error) {
	var cut controlledNativeLeaseCut
	control, err := peerauth.VerifyControlManifest(controlRaw, authority.ControlKey, authority.Profile.Lineage)
	if err != nil || control.Profile.Digest() != authority.Profile.Digest() {
		return cut, errors.New("owner signed control profile refused")
	}
	data, err := peerauth.VerifyManifest(dataRaw, authority.DataKey, authority.Domain)
	if err != nil || !slices.Equal(data.Members, authority.Members) {
		return cut, errors.New("owner signed data membership refused")
	}
	valid := func(issued, expires time.Time) bool {
		// Identical time predicate to native membership admission. In particular
		// a freshly published lease is not spuriously rejected for its first2s.
		return !now.Before(issued.Add(-peerauth.ClockMargin)) && now.Add(peerauth.ClockMargin).Before(expires)
	}
	if !valid(control.IssuedAt, control.ExpiresAt) || !valid(data.IssuedAt, data.ExpiresAt) {
		return cut, errors.New("owner current signed lease expired or future")
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

func controlledNativeLeaseTranscript(raw []byte, authority controlledNativeLeaseAuthority, from, through time.Time) (int, error) {
	if len(raw) == 0 || len(raw) > 4<<20 || through.Before(from) {
		return 0, errors.New("owner bounded lease interval transcript refused")
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	count := 0
	var first, previous controlledNativeLeaseCut
	for scanner.Scan() {
		var cut controlledNativeLeaseCut
		if count == 128 || controlledNativeOwnerDecode(scanner.Bytes(), &cut) != nil {
			return count, errors.New("owner exact signed lease transcript refused")
		}
		verified, err := controlledNativeLeaseVerify(authority, cut.ObservedAt, cut.ControlManifest, cut.DataManifest)
		if cut.NativeLow != nil || cut.NativeHigh != nil {
			if cut.NativeLow == nil || cut.NativeHigh == nil {
				return count, errors.New("owner partial native witness refused")
			}
			verified, err = controlledNativeLeaseVerifyBounds(authority, cut.ObservedAt, *cut.NativeLow, *cut.NativeHigh, cut.ControlManifest, cut.DataManifest)
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
	if scanner.Err() != nil || count == 0 || first.ObservedAt.After(from) || previous.ObservedAt.Before(through) ||
		!through.Add(peerauth.ClockMargin).Before(previous.ControlExpires) || !through.Add(peerauth.ClockMargin).Before(previous.DataExpires) {
		return count, errors.New("owner whole actual lease interval not covered")
	}
	return count, nil
}

func controlledNativeNativeInterval(raw []byte, from, until time.Time) (int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	count, ended := 0, false
	var first, last time.Time
	for scanner.Scan() {
		var cut controlledNativeLeaseCut
		if controlledNativeOwnerDecode(scanner.Bytes(), &cut) != nil {
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
	if scanner.Err() != nil || count < 2 || !first.Equal(from) || !last.Equal(until) {
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
}
