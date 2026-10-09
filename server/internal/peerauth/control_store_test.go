package peerauth

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func TestControlStoreUsesWholeTimeInterval(t *testing.T) {
	m, key, opts, now := controlFixture(t)
	low, high := *now, now.Add(time.Second)
	var sourceErr error
	opts.TimeBounds = func() (time.Time, time.Time, error) { return low, high, sourceErr }
	s, err := CreateControlStore(opts, signControlFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Check(t.Context()) != nil {
		t.Fatal("valid interval rejected")
	}
	low = m.IssuedAt.Add(-time.Nanosecond)
	if s.Check(t.Context()) == nil {
		t.Fatal("upper endpoint substituted for not-before")
	}
	low = m.IssuedAt
	high = high.Add(-time.Millisecond)
	if s.Check(t.Context()) != nil || s.FaultReason() != "none" {
		t.Fatal("tighter interval mislabeled as clock rollback")
	}
	sourceErr = errors.New("source unavailable")
	if s.Check(t.Context()) == nil || s.FaultReason() != "none" {
		t.Fatal("source loss ignored or membership permanently poisoned")
	}
	sourceErr = nil
	if s.Check(t.Context()) != nil {
		t.Fatal("fresh source recovery refused")
	}
	high = m.ExpiresAt.Add(-ClockMargin)
	if s.Check(t.Context()) == nil {
		t.Fatal("expiry equality accepted")
	}
	m.Version++
	m.IssuedAt = high.Add(-time.Second)
	m.ExpiresAt = high.Add(time.Minute)
	if s.Apply(signControlFixture(t, m, key)) == nil {
		t.Fatal("refresh ignored low endpoint")
	}
}

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

// Adapted from the independent checkpoint probe, including its real
// file-write/sync/rename cut. These are in-process panic cuts, not a simulated
// hardware power loss. Both a refresh and a terminal binding change must close
// admission, retain the observed disk cut, and require certified recovery.
func TestS3AIndependentControlCheckpointPanicMustClose(t *testing.T) {
	for _, binding := range []string{"same", "changed"} {
		t.Run(binding, func(t *testing.T) {
			for _, stage := range []string{"write", "sync", "rename", "directory_sync"} {
				t.Run(stage, func(t *testing.T) {
					m, key, opts, _ := controlFixture(t)
					original := signControlFixture(t, m, key)
					s, err := CreateControlStore(opts, original)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = s.Close() }()
					old, err := s.Floor()
					if err != nil || s.Check(t.Context()) != nil {
						t.Fatal("initial live membership", old, err)
					}
					m.Version++
					if binding == "changed" {
						m.Profile.ProtocolScope[0]++
					}
					next := signControlFixture(t, m, key)
					fault := errors.New("independent checkpoint panic")
					fired := false
					s.checkpointHook = func(at string) error {
						if at == stage {
							fired = true
							panic(fault)
						}
						return nil
					}
					var recovered any
					func() {
						defer func() { recovered = recover() }()
						_ = s.Apply(next)
					}()
					if !fired || recovered != fault {
						t.Fatal("checkpoint did not preserve the original panic", fired, recovered)
					}
					retained := original
					if stage == "directory_sync" {
						retained = next
					}
					disk, err := readCheckpoint(opts.Path)
					if err != nil || !bytes.Equal(disk, retained) {
						t.Fatal("checkpoint panic did not retain the expected native cut", err)
					}
					checkErr := s.Check(t.Context())
					_, _, memberErr := s.store.Member(opts.Self.Identity)
					floor, floorErr := s.Floor()
					t.Logf("stage=%s fault=%q Check=%v Member=%v Floor=(%+v,%v)",
						stage, s.FaultReason(), checkErr, memberErr, floor, floorErr)
					if checkErr == nil || memberErr == nil || s.FaultReason() == "none" {
						t.Error("uncertain checkpoint left workload membership eligible")
					}
					if floorErr == nil || floor != (ControlFloor{}) {
						t.Error("uncertain checkpoint returned an acknowledged M floor")
					}
					// Neither an exact replay nor a higher original-binding revision
					// may reopen the surviving process or replace a retained fence.
					s.checkpointHook = nil
					m.Version++
					m.Profile = opts.Profile
					if s.Apply(original) == nil || s.Apply(signControlFixture(t, m, key)) == nil {
						t.Error("uncertain checkpoint admitted another refresh")
					}
					if after, err := readCheckpoint(opts.Path); err != nil || !bytes.Equal(after, disk) {
						t.Fatal("failed refresh overwrote retained checkpoint", err)
					}
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					resumed, err := ResumeControlStore(opts, old)
					if stage == "directory_sync" && binding == "changed" {
						if !errors.Is(err, ErrMembership) {
							_ = resumed.Close()
							t.Fatal("resume did not reject visible terminal binding fence", err)
						}
						if resumed, err := ResumeControlStore(opts, ControlFloor{}); !errors.Is(err, ErrMembership) {
							_ = resumed.Close()
							t.Fatal("zero floor did not reject retained terminal binding fence", err)
						}
						lease, err := mutationlog.AcquireFileWALLease(opts.Path)
						if err != nil {
							t.Fatal("failed recovery stranded M lease", err)
						}
						if err := lease.Close(); err != nil {
							t.Fatal(err)
						}
					} else {
						if err != nil {
							t.Fatal("could not certify the retained same-binding cut", err)
						}
						defer func() { _ = resumed.Close() }()
						floor, err := resumed.Floor()
						want := old.Version
						if stage == "directory_sync" {
							want++
						}
						if err != nil || floor.Version != want || resumed.Check(t.Context()) != nil {
							t.Fatal("certified recovery did not publish the retained membership", floor, err)
						}
					}
					if after, err := readCheckpoint(opts.Path); err != nil || !bytes.Equal(after, disk) {
						t.Fatal("recovery changed the retained signed checkpoint", err)
					}
				})
			}
		})
	}
}

// The original independent constructor probe uses the existing clock callback,
// after native lease acquisition. Extend it to Create's later liveness sample
// and to Resume so neither ownership transfer can leak an unreturned lease.
func TestS3AIndependentControlConstructionPanicMustReleaseLease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resume  bool
		panicAt int
	}{
		{name: "create_prepare", panicAt: 1},
		{name: "create_after_prepare", panicAt: 2},
		{name: "resume_prepare", resume: true, panicAt: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, key, opts, _ := controlFixture(t)
			raw := signControlFixture(t, m, key)
			now := opts.Now
			if tc.resume {
				s, err := CreateControlStore(opts, raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			fault := errors.New("independent construction clock panic")
			calls := 0
			opts.Now = func() time.Time {
				calls++
				if calls == tc.panicAt {
					panic(fault)
				}
				return now()
			}
			open := func() (*ControlStore, error) {
				if tc.resume {
					return ResumeControlStore(opts, ControlFloor{})
				}
				return CreateControlStore(opts, raw)
			}
			var recovered any
			var store *ControlStore
			func() {
				defer func() { recovered = recover() }()
				store, _ = open()
			}()
			if store != nil {
				_ = store.Close()
				t.Fatal("panicking constructor returned a store")
			}
			if calls != tc.panicAt || recovered != fault {
				t.Fatal("clock fault boundary did not preserve original panic", calls, recovered)
			}
			lease, err := mutationlog.AcquireFileWALLease(opts.Path)
			if err != nil {
				t.Fatal("constructor panic stranded the unreturned M lease", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			if tc.resume {
				if disk, err := readCheckpoint(opts.Path); err != nil || !bytes.Equal(disk, raw) {
					t.Fatal("constructor panic changed retained checkpoint", err)
				}
			} else if _, err := os.Stat(opts.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("clock panic unexpectedly created checkpoint", err)
			}
			opts.Now = now
			store, err = open()
			if err != nil {
				t.Fatal("valid construction after panic", err)
			}
			defer func() { _ = store.Close() }()
			if store.Check(t.Context()) != nil {
				t.Fatal("valid retry did not publish membership")
			}
			if lease, err := mutationlog.AcquireFileWALLease(opts.Path); !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
				if lease != nil {
					_ = lease.Close()
				}
				t.Fatal("successful construction did not retain lease ownership", err)
			}
		})
	}
}
