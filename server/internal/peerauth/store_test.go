package peerauth

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStorePersistedVersionFloorAndExpiredRecovery(t *testing.T) {
	m, key, options, now := membershipFixture(t)
	raw := signFixture(t, m, key)
	s, err := CreateStore(options, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResumeStore(options); err == nil {
		t.Fatal("path ownership not enforced")
	}
	m.Version = 2
	newer := signFixture(t, m, key)
	if s.Apply(newer) != nil || s.Apply(newer) != nil || s.Apply(raw) == nil {
		t.Fatal("monotonic/idempotent apply failed")
	}
	equivocation := m
	equivocation.ExpiresAt = equivocation.ExpiresAt.Add(time.Second)
	if s.Apply(signFixture(t, equivocation, key)) == nil {
		t.Fatal("same-version renewal accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	s, err = ResumeStore(options)
	if err != nil {
		t.Fatal("expired checkpoint lost version floor", err)
	}
	defer func() { _ = s.Close() }()
	if _, _, err := s.Member(m.Members[0].Identity); err == nil || s.Apply(raw) == nil {
		t.Fatal("expired/rollback membership admitted")
	}
	m.Version = 3
	m.IssuedAt, m.ExpiresAt = *now, now.Add(time.Minute)
	if s.Apply(signFixture(t, m, key)) != nil {
		t.Fatal("fresh signed update rejected")
	}
	if _, _, err := s.Member(m.Members[0].Identity); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(-time.Second)
	if _, _, err := s.Member(m.Members[0].Identity); err == nil {
		t.Fatal("backwards clock retained authority")
	}
}

func TestStoreRefusesLostDamagedOrPartialCheckpoint(t *testing.T) {
	m, key, options, _ := membershipFixture(t)
	raw := signFixture(t, m, key)
	s, err := CreateStore(options, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateStore(options, raw); err == nil {
		t.Fatal("fresh genesis overwrote previous state")
	}
	if err := os.WriteFile(options.Path, []byte(checkpointMagic), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResumeStore(options); err == nil {
		t.Fatal("damaged state accepted")
	}
	if err := os.Remove(options.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := ResumeStore(options); err == nil {
		t.Fatal("lost state created anonymous fallback")
	}
}

func TestStoreLocalRemovalStopsAllPeerAuthority(t *testing.T) {
	m, key, options, _ := membershipFixture(t)
	options.Self = m.Members[0]
	other := Member{ID: [16]byte{8}, Identity: "spiffe://lantern.test/node-b", SPKI: [32]byte{9}, Origin: "https://localhost:6382"}
	m.Members = append(m.Members, other)
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if origins, err := s.Origins(); err != nil || len(origins) != 1 || origins[0] != other.Origin {
		t.Fatal("self-filter failed", origins, err)
	}
	m.Version++
	m.Members = []Member{other}
	if err := s.Apply(signFixture(t, m, key)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Member(other.Identity); err == nil {
		t.Fatal("removed local member kept inbound authority")
	}
	if _, err := s.Origins(); err == nil {
		t.Fatal("removed local member kept outbound authority")
	}
}

func TestStorePrivateFaultReasonIsLatchedAndReadOnly(t *testing.T) {
	for _, cause := range []string{"clock_rollback", "checkpoint_failure", "closed"} {
		t.Run(cause, func(t *testing.T) {
			m, key, options, now := membershipFixture(t)
			calls := 0
			options.Now = func() time.Time { calls++; return *now }
			store, err := CreateStore(options, signFixture(t, m, key))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			before := calls
			if store.FaultReason() != "none" || calls != before {
				t.Fatal("healthy diagnostic sampled time")
			}
			switch cause {
			case "clock_rollback":
				*now = now.Add(-time.Second)
				if _, _, err := store.Member(m.Members[0].Identity); err == nil {
					t.Fatal("rollback did not fence authority")
				}
			case "checkpoint_failure":
				if err := os.Remove(options.Path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(options.Path, 0700); err != nil {
					t.Fatal(err)
				}
				m.Version++
				if store.Apply(signFixture(t, m, key)) == nil {
					t.Fatal("failed checkpoint retained authority")
				}
			case "closed":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before = calls
			wall, digest := store.lastWall, store.current.digest
			for range 10 {
				if store.FaultReason() != cause {
					t.Fatal("latched fault lost its private category")
				}
			}
			if calls != before || store.lastWall != wall || store.current.digest != digest {
				t.Fatal("inspection changed time/state")
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if store.FaultReason() != cause {
				t.Fatal("cleanup overwrote the original fault")
			}
		})
	}
}

func TestCurrentDataStoreUsesWholeTimeInterval(t *testing.T) {
	m, key, options, now := membershipFixture(t)
	m.Domain.AuthMode, m.Domain.SecurityGeneration = "oidc", [16]byte{1}
	m.Domain.CurrentProfile = "current-v2:" + strings.Repeat("ab", 32)
	options.Domain = m.Domain
	low, high := *now, now.Add(time.Second)
	var sourceErr error
	options.TimeBounds = func() (time.Time, time.Time, error) { return low, high, sourceErr }
	// This scalar port is for TLS/deadlines only; it must never seed rollback.
	options.Now = func() time.Time {
		if sourceErr != nil {
			return time.Unix(0, int64(^uint64(0)>>1)).UTC()
		}
		return high
	}
	raw := signFixture(t, m, key)
	for _, missing := range []string{"interval", "verification"} {
		candidate := options
		if missing == "interval" {
			candidate.TimeBounds = nil
		} else {
			candidate.Now = nil
		}
		if s, err := CreateStore(candidate, raw); err == nil {
			s.Close()
			t.Fatal("current clock fell back", missing)
		}
	}
	for _, invalid := range []string{"before_not_before", "expiry_equality", "zero_low", "zero_high", "inverted", "source_error"} {
		t.Run("fresh_"+invalid, func(t *testing.T) {
			low, high, sourceErr = *now, now.Add(time.Second), nil
			switch invalid {
			case "before_not_before":
				low = now.Add(-time.Nanosecond)
			case "expiry_equality":
				high = m.ExpiresAt.Add(-ClockMargin)
			case "zero_low":
				low = time.Time{}
			case "zero_high":
				high = time.Time{}
			case "inverted":
				low = high.Add(time.Nanosecond)
			case "source_error":
				sourceErr = errors.New("no qualified sample")
			}
			if s, err := CreateStore(options, raw); err == nil {
				s.Close()
				t.Fatal("invalid interval created membership")
			}
			if _, err := os.Stat(options.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("declined creation published a checkpoint", err)
			}
		})
	}
	low, high, sourceErr = *now, now.Add(time.Second), nil
	s, err := CreateStore(options, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := os.ReadFile(options.Path)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		_, _, memberErr := s.Member(m.Members[0].Identity)
		_, _, originErr := s.MemberAtOrigin(m.Members[0].Origin)
		_, originsErr := s.Origins()
		if (memberErr == nil) != want || (originErr == nil) != want || (originsErr == nil) != want {
			t.Fatal("interval admission", want, memberErr, originErr, originsErr)
		}
	}
	check(true)
	low, high = low.Add(time.Millisecond), high.Add(-time.Millisecond)
	check(true)
	sourceErr = errors.New("temporarily no qualified interval")
	check(false)
	if s.Apply(raw) == nil || s.FaultReason() != "none" {
		t.Fatal("source loss ignored or latched")
	}
	sourceErr = nil
	check(true)
	for _, invalid := range []string{"before_not_before", "expiry_equality", "source_error"} {
		low, high, sourceErr = *now, now.Add(time.Second), nil
		next := m
		next.Version++
		switch invalid {
		case "before_not_before":
			next.IssuedAt = low.Add(time.Nanosecond)
		case "expiry_equality":
			high = next.ExpiresAt.Add(-ClockMargin)
		case "source_error":
			sourceErr = errors.New("no qualified interval")
		}
		if s.Apply(signFixture(t, next, key)) == nil {
			t.Fatal("invalid Apply published", invalid)
		}
		after, err := os.ReadFile(options.Path)
		if err != nil || !bytes.Equal(before, after) || s.current.manifest.Version != m.Version || s.FaultReason() != "none" {
			t.Fatal("declined Apply changed checkpoint/floor", invalid, err)
		}
	}
	low, high, sourceErr = *now, now.Add(time.Second), nil
	check(true)
	m.Version++
	if s.Apply(signFixture(t, m, key)) != nil {
		t.Fatal("valid higher revision refused after interval recovery")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	low, high = m.ExpiresAt, m.ExpiresAt.Add(time.Second)
	resumed, err := ResumeStore(options)
	if err != nil {
		t.Fatal("expired checkpoint floor was lost", err)
	}
	defer resumed.Close()
	if _, _, err := resumed.Member(m.Members[0].Identity); err == nil || resumed.current.manifest.Version != m.Version {
		t.Fatal("expired resume authorized or lost its floor")
	}
}

func TestCurrentDataIntervalCannotRepairPermanentStoreFault(t *testing.T) {
	for _, fault := range []string{"checkpoint_failure", "closed"} {
		t.Run(fault, func(t *testing.T) {
			m, key, options, now := membershipFixture(t)
			m.Domain.AuthMode, m.Domain.SecurityGeneration = "oidc", [16]byte{1}
			m.Domain.CurrentProfile = "current-v2:" + strings.Repeat("ab", 32)
			options.Domain = m.Domain
			low, high := *now, now.Add(time.Second)
			options.TimeBounds = func() (time.Time, time.Time, error) { return low, high, nil }
			s, err := CreateStore(options, signFixture(t, m, key))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if fault == "closed" {
				s.Close()
			} else {
				if err := os.Remove(options.Path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(options.Path, 0700); err != nil {
					t.Fatal(err)
				}
				m.Version++
				if s.Apply(signFixture(t, m, key)) == nil {
					t.Fatal("failed checkpoint accepted")
				}
			}
			low, high = low.Add(time.Millisecond), high.Add(-time.Millisecond)
			if _, _, err := s.Member(m.Members[0].Identity); err == nil || s.FaultReason() != fault {
				t.Fatal("valid interval cleared permanent fault", fault, err)
			}
		})
	}
}
