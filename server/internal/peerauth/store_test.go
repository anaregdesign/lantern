package peerauth

import (
	"os"
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
