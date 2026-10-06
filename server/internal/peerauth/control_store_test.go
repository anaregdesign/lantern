package peerauth

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestControlStoreRefreshFenceAndIndependentFloor(t *testing.T) {
	m, key, opts, _ := controlFixture(t)
	raw := signControlFixture(t, m, key)
	s, err := CreateControlStore(opts, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	old, _ := s.Floor()
	foreign := m
	foreign.Version++
	foreign.Profile.Lineage.Instance[0]++
	if s.Apply(signControlFixture(t, foreign, key)) == nil || s.Check(t.Context()) != nil {
		t.Fatal("foreign manifest fenced owner")
	}
	m.Version++
	m.ExpiresAt = m.ExpiresAt.Add(time.Second)
	if err := s.Apply(signControlFixture(t, m, key)); err != nil {
		t.Fatal(err)
	}
	floor, err := s.Floor()
	if err != nil || floor.Version != 2 {
		t.Fatal(floor, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.Path, append([]byte(checkpointMagic), raw...), 0600); err != nil {
		t.Fatal(err)
	}
	if resumed, err := ResumeControlStore(opts, floor); err == nil {
		_ = resumed.Close()
		t.Fatal("known membership rollback")
	}
	s, err = ResumeControlStore(opts, old)
	if err != nil {
		t.Fatal(err)
	}
	m.Profile.ProtocolScope[0]++
	if s.Apply(signControlFixture(t, m, key)) == nil || s.FaultReason() != "control_binding_changed" {
		t.Fatal("changed binding not fenced")
	}
	fence, err := s.Floor()
	if err != nil || fence.Version != 2 {
		t.Fatal("durable fence receipt", fence, err)
	}
	_ = s.Close()
	if resumed, err := ResumeControlStore(opts, ControlFloor{}); err == nil {
		_ = resumed.Close()
		t.Fatal("old binding reopened after fence")
	}
}

func TestControlStoreUncertainFenceAndTimeBoundaries(t *testing.T) {
	for _, stage := range []string{"write", "sync", "rename", "directory_sync"} {
		t.Run(stage, func(t *testing.T) {
			m, key, opts, _ := controlFixture(t)
			s, err := CreateControlStore(opts, signControlFixture(t, m, key))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			s.checkpointHook = func(at string) error {
				if at == stage {
					return errors.New("injected native checkpoint failure")
				}
				return nil
			}
			m.Version++
			m.Profile.ProtocolScope[0]++
			if s.Apply(signControlFixture(t, m, key)) == nil || s.Check(t.Context()) == nil {
				t.Fatal("uncertain store continued")
			}
			if _, err := s.Floor(); err == nil {
				t.Fatal("uncertain fence acknowledged")
			}
			if stage == "directory_sync" {
				_ = s.Close()
				if next, err := ResumeControlStore(opts, ControlFloor{}); err == nil {
					_ = next.Close()
					t.Fatal("visible terminal fence ignored")
				}
			}
		})
	}
	t.Run("time", func(t *testing.T) {
		m, key, opts, now := controlFixture(t)
		*now = m.IssuedAt.Add(-ClockMargin - time.Nanosecond)
		if s, err := CreateControlStore(opts, signControlFixture(t, m, key)); err == nil {
			_ = s.Close()
			t.Fatal("before not-before")
		}
		*now = m.IssuedAt.Add(-ClockMargin)
		s, err := CreateControlStore(opts, signControlFixture(t, m, key))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		*now = m.ExpiresAt.Add(-ClockMargin - time.Nanosecond)
		if s.Check(t.Context()) != nil {
			t.Fatal("strictly before expiry")
		}
		*now = m.ExpiresAt.Add(-ClockMargin)
		if s.Check(t.Context()) == nil {
			t.Fatal("exact expiry admitted")
		}
	})
}
