package security

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func s2cJournalFixture(t *testing.T, policy s2cJournalPolicy) (string, [32]byte, *s2cJournal) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "protocol.wal")
	binding := [32]byte{1}
	j, err := createS2CJournal(path, binding, policy, []byte("g"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.close() })
	return path, binding, j
}

func s2cJournalReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestS2CJournalDurableRoundTrip(t *testing.T) {
	policy := s2cJournalPolicy{8192, 32}
	path, binding, j := s2cJournalFixture(t, policy)
	genesisFloor := j.floor
	first := []byte("original immutable evidence")
	if _, err := j.append(first, 0, 0); err != nil {
		t.Fatal(err)
	}
	first[0] = '!'
	last, err := j.append([]byte("another retained record"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := []string{"g", "original immutable evidence", "another retained record"}
	if !reflect.DeepEqual(j.records, wantRecords) {
		t.Fatalf("caller modified retained history: %q", j.records)
	}
	if _, err := mutationlog.AcquireFileWALLease(path); !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
		t.Fatalf("live journal did not retain lease: %v", err)
	}
	wantUsed := j.used
	if err := j.close(); err != nil {
		t.Fatal(err)
	}
	var indices []uint64
	restored, err := resumeS2CJournal(path, binding, policy, genesisFloor, func(index uint64, raw []byte) error {
		indices = append(indices, index)
		if string(raw) != wantRecords[index-1] {
			t.Fatalf("record %d = %q", index, raw)
		}
		raw[0] = '?' // The semantic callback cannot mutate the retained bytes.
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close()
	if !reflect.DeepEqual(indices, []uint64{1, 2, 3}) || !reflect.DeepEqual(restored.records, wantRecords) ||
		restored.floor != last || restored.used != wantUsed || restored.count != 3 {
		t.Fatalf("restore differs: indices=%v records=%q floor=%+v used=%d", indices, restored.records, restored.floor, restored.used)
	}
	next, err := restored.append([]byte("next"), 0, 0)
	if err != nil || next.Index != 4 {
		t.Fatalf("next append = %+v, %v", next, err)
	}
	used, count, tips, err := s2cJournalFiles(path, policy)
	if err != nil || used != restored.used || count != 4 || tips != 4 {
		t.Fatalf("actual accounting = %d/%d/%d, %v", used, count, tips, err)
	}
}

func TestS2CJournalCompletionCredit(t *testing.T) {
	for _, short := range []uint64{0, 1} {
		t.Run(map[uint64]string{0: "exact", 1: "one byte short"}[short], func(t *testing.T) {
			policy := s2cJournalPolicy{85 + 4*89 - short, 4}
			_, _, j := s2cJournalFixture(t, policy)
			before := j.floor
			for _, protected := range [][2]uint64{{math.MaxUint64, 0}, {0, math.MaxUint64}, {178, 3}} {
				if _, err := j.append([]byte("a"), protected[0], protected[1]); !errors.Is(err, errS2CJournalCapacity) {
					t.Fatalf("overflowing protection accepted: %v", err)
				}
			}
			if j.floor != before || j.guard() != nil {
				t.Fatal("preflight refusal changed or poisoned journal")
			}
			if _, err := j.append([]byte("a"), 178-short, 2); err != nil {
				t.Fatal(err)
			}
			if _, err := j.append([]byte("b"), 178-short, 2); !errors.Is(err, errS2CJournalCapacity) {
				t.Fatalf("ordinary traffic stole completion credit: %v", err)
			}
			if _, err := j.append([]byte("c"), 89-short, 1); err != nil {
				t.Fatal(err)
			}
			_, err := j.append([]byte("d"), 0, 0)
			if short == 0 && err != nil || short != 0 && !errors.Is(err, errS2CJournalCapacity) {
				t.Fatalf("exact final charge: %v", err)
			}
			if _, err := j.append([]byte("e"), 0, 0); !errors.Is(err, errS2CJournalCapacity) {
				t.Fatalf("budget refilled: %v", err)
			}
		})
	}
	t.Run("one record short", func(t *testing.T) {
		_, _, j := s2cJournalFixture(t, s2cJournalPolicy{4096, 2})
		if _, err := j.append([]byte("a"), 0, 1); !errors.Is(err, errS2CJournalCapacity) {
			t.Fatalf("record credit stolen: %v", err)
		}
		if _, err := j.append([]byte("a"), 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := j.append([]byte("b"), 0, 0); !errors.Is(err, errS2CJournalCapacity) {
			t.Fatalf("record limit bypassed: %v", err)
		}
	})
}

func TestS2CJournalMissingTipCapacity(t *testing.T) {
	for _, short := range []uint64{0, 1} {
		t.Run(map[uint64]string{0: "exact", 1: "one byte short"}[short], func(t *testing.T) {
			policy := s2cJournalPolicy{85 + 2*89, 2}
			path, binding, j := s2cJournalFixture(t, policy)
			floor, err := j.append([]byte("a"), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.close(); err != nil {
				t.Fatal(err)
			}
			tip := s2cJournalReadFile(t, path+".tip")
			if err := os.Truncate(path+".tip", int64(len(tip)-44)); err != nil {
				t.Fatal(err)
			}
			policy.Bytes -= short
			visits := 0
			restored, err := resumeS2CJournal(path, binding, policy, floor, func(uint64, []byte) error { visits++; return nil })
			if short != 0 {
				if restored != nil || !errors.Is(err, errS2CJournalCapacity) || visits != 0 {
					t.Fatalf("unfunded catch-up = %v, %v, visits=%d", restored, err, visits)
				}
				if got := s2cJournalReadFile(t, path+".tip"); !bytes.Equal(got, tip[:len(tip)-44]) {
					t.Fatal("failed recovery modified tip")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer restored.close()
			if restored.floor != floor || restored.used != policy.Bytes || visits != 2 || !bytes.Equal(s2cJournalReadFile(t, path+".tip"), tip) {
				t.Fatal("catch-up did not restore exact durable budget and tip")
			}
			if _, err := restored.append([]byte("b"), 0, 0); !errors.Is(err, errS2CJournalCapacity) {
				t.Fatalf("recovery refunded used bytes: %v", err)
			}
		})
	}
}

func TestS2CJournalRollbackAndFullSuffixValidation(t *testing.T) {
	policy := s2cJournalPolicy{8192, 32}
	for _, damage := range []string{"rollback", "wrong floor", "wrong binding", "torn WAL", "torn tip", "missing WAL", "missing tip", "semantic suffix", "record bound"} {
		t.Run(damage, func(t *testing.T) {
			path, binding, j := s2cJournalFixture(t, policy)
			oldFloor := j.floor
			oldWAL, oldTip := s2cJournalReadFile(t, path), s2cJournalReadFile(t, path+".tip")
			latest, err := j.append([]byte("later obligation"), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.close(); err != nil {
				t.Fatal(err)
			}
			required, actualPolicy := oldFloor, policy
			switch damage {
			case "rollback":
				required = latest
				if err := os.WriteFile(path, oldWAL, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+".tip", oldTip, 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong floor":
				required.Chain[0] ^= 1
			case "wrong binding":
				binding[0] ^= 1
				required = s2cJournalFloor{}
			case "torn WAL", "torn tip":
				name := path
				if damage == "torn tip" {
					name += ".tip"
				}
				if err := os.Truncate(name, int64(len(s2cJournalReadFile(t, name))-1)); err != nil {
					t.Fatal(err)
				}
			case "missing WAL", "missing tip":
				name := path
				if damage == "missing tip" {
					name += ".tip"
				}
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			case "record bound":
				actualPolicy.Records = 1
			}
			visits := 0
			restored, err := resumeS2CJournal(path, binding, actualPolicy, required, func(index uint64, _ []byte) error {
				visits++
				if damage == "semantic suffix" && index == 2 {
					return errS2CJournalRecord
				}
				return nil
			})
			if restored != nil || err == nil || damage != "semantic suffix" && visits != 0 {
				t.Fatalf("invalid history accepted: owner=%v err=%v visits=%d", restored, err, visits)
			}
			lease, err := mutationlog.AcquireFileWALLease(path)
			if err != nil {
				t.Fatalf("failed recovery leaked lease: %v", err)
			}
			_ = lease.Close()
		})
	}
}

func TestS2CJournalUncertaintyAndPanic(t *testing.T) {
	for _, point := range []string{"before append panic", "after log panic", "closed tip"} {
		t.Run(point, func(t *testing.T) {
			policy := s2cJournalPolicy{8192, 32}
			path, binding, j := s2cJournalFixture(t, policy)
			before := j.floor
			mixed := errors.Join(mutationlog.ErrWALIndeterminate, &mutationlog.DefiniteWALAbort{Cause: errors.New("lost reply")})
			switch point {
			case "before append panic":
				j.hooks = &s2cJournalHooks{beforeAppend: func() { panic(mixed) }}
			case "after log panic":
				j.hooks = &s2cJournalHooks{afterLog: func() { panic(mixed) }}
			case "closed tip":
				if err := j.tip.Close(); err != nil {
					t.Fatal(err)
				}
			}
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				if _, err := j.append([]byte("recover exact bytes"), 0, 0); !errors.Is(err, errS2CJournalUnknown) {
					t.Fatalf("owned error was not uncertain: %v", err)
				}
			}()
			if panicked != (point != "closed tip") || !errors.Is(j.guard(), errS2CJournalUnknown) {
				t.Fatalf("journal survived uncertainty: panic=%v guard=%v", panicked, j.guard())
			}
			if _, err := j.append([]byte("retry"), 0, 0); !errors.Is(err, errS2CJournalUnknown) {
				t.Fatalf("uncertain sequence reused: %v", err)
			}
			if err := j.close(); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(j.guard(), errS2CJournalUnknown) {
				t.Fatal("close hid uncertainty behind definite closed state")
			}
			restored, err := resumeS2CJournal(path, binding, policy, before, func(uint64, []byte) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer restored.close()
			wantCount := uint64(1)
			if point == "after log panic" {
				wantCount = 2
			}
			if restored.count != wantCount || wantCount == 2 && restored.records[1] != "recover exact bytes" {
				t.Fatalf("recovered incorrect uncertain suffix: %q", restored.records)
			}
		})
	}
}

func TestS2CJournalConstructionAndCloseOwnership(t *testing.T) {
	policy := s2cJournalPolicy{8192, 32}
	t.Run("partial family", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "p.wal")
		if err := os.WriteFile(path+".tip", []byte("retained partial bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		if j, err := createS2CJournal(path, [32]byte{1}, policy, []byte("g")); j != nil || !errors.Is(err, os.ErrExist) {
			t.Fatalf("adopted partial family: %v, %v", j, err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("creation touched missing WAL: %v", err)
		}
	})
	t.Run("replay panic cleanup", func(t *testing.T) {
		path, binding, j := s2cJournalFixture(t, policy)
		floor := j.floor
		_ = j.close()
		panicked := false
		func() {
			defer func() { panicked = recover() != nil }()
			_, _ = resumeS2CJournal(path, binding, policy, floor, func(uint64, []byte) error { panic("replay checkpoint") })
		}()
		if !panicked {
			t.Fatal("replay checkpoint did not run")
		}
		lease, err := mutationlog.AcquireFileWALLease(path)
		if err != nil {
			t.Fatalf("panic leaked lease: %v", err)
		}
		_ = lease.Close()
	})
	t.Run("close drains append", func(t *testing.T) {
		path, _, j := s2cJournalFixture(t, policy)
		entered, release := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		j.hooks = &s2cJournalHooks{beforeAppend: func() { close(entered); <-release }}
		appendDone, closeDone := make(chan error, 1), make(chan error, 1)
		go func() { _, err := j.append([]byte("active"), 0, 0); appendDone <- err }()
		<-entered
		go func() { closeDone <- j.close() }()
		if lease, err := mutationlog.AcquireFileWALLease(path); !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
			if lease != nil {
				_ = lease.Close()
			}
			t.Fatalf("close released active ownership: %v", err)
		}
		select {
		case err := <-closeDone:
			t.Fatalf("close returned before append drained: %v", err)
		default:
		}
		close(release)
		if err := <-appendDone; err != nil {
			t.Fatal(err)
		}
		if err := <-closeDone; err != nil {
			t.Fatal(err)
		}
		if !errors.Is(j.guard(), errS2CJournalClosed) {
			t.Fatal("closed journal remained readable")
		}
		lease, err := mutationlog.AcquireFileWALLease(path)
		if err != nil {
			t.Fatal(err)
		}
		_ = lease.Close()
	})
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "close error cleanup", true: "close panic cleanup"}[panics], func(t *testing.T) {
			path, _, j := s2cJournalFixture(t, policy)
			cause := errors.Join(mutationlog.ErrWALIndeterminate, &mutationlog.DefiniteWALAbort{Cause: errors.New("close acknowledgement lost")})
			j.walOwner = s2cJournalCloseFault{j.walOwner, cause, panics}
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				if err := j.close(); !errors.Is(err, cause) {
					t.Fatalf("close lost error: %v", err)
				}
			}()
			if panicked != panics || !errors.Is(j.guard(), errS2CJournalUnknown) || !errors.Is(j.guard(), mutationlog.ErrWALIndeterminate) {
				t.Fatalf("close hid uncertainty: panic=%v guard=%v", panicked, j.guard())
			}
			lease, err := mutationlog.AcquireFileWALLease(path)
			if err != nil {
				t.Fatalf("close failure stranded lease: %v", err)
			}
			_ = lease.Close()
		})
	}
}

type s2cJournalCloseFault struct {
	io.Closer
	cause  error
	panics bool
}

func (c s2cJournalCloseFault) Close() error {
	err := c.Closer.Close()
	if c.panics {
		panic(c.cause)
	}
	return errors.Join(err, c.cause)
}
