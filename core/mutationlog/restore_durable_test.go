package mutationlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
)

// Both fixture variants leave a complete record whose last Sync acknowledgement
// was lost. Reopening the process-visible bytes is not itself a barrier.
func durableTipResumeFixture(t *testing.T, ahead bool) (string, *FileWALLease, *FileWALTipJournal) {
	t.Helper()
	path, lease, wal, tip, binding := fileWALTipFixture(t)
	path = lease.Path()
	if err := wal.Write(fileWALEntry(1, "first")); err != nil {
		t.Fatal(err)
	}
	if ahead {
		if err := wal.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		wal, err = ResumeFileWAL(path, fileWALStringEncode, fileWALStringDecode, func(Entry) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		wal.file = &lostSyncAckFile{File: wal.file.(*os.File)}
	} else {
		tip.file = &lostSyncAckFile{File: tip.file.(*os.File)}
	}
	if err := wal.Write(fileWALEntry(2, "second")); err == nil {
		t.Fatal("lost Sync acknowledgement unexpectedly succeeded")
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tip.Close(); err != nil {
		t.Fatal(err)
	}
	tip, err := ResumeFileWALTipJournal(path, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tip.Close() })
	return path, lease, tip
}

type durableResumeTrace struct {
	events []string
	fault  string
	panic  bool
	err    error
}

func (r *durableResumeTrace) record(event string) error {
	r.events = append(r.events, event)
	if event == r.fault {
		if r.panic {
			panic(r.err)
		}
		return r.err
	}
	return nil
}

type durableResumeFile struct {
	*os.File
	name   string
	trace  *durableResumeTrace
	syncs  int
	closed bool
}

func (f *durableResumeFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	if err != nil {
		return n, err
	}
	return n, f.trace.record(f.name + "-write")
}

func (f *durableResumeFile) Sync() error {
	f.syncs++
	if err := f.trace.record(fmt.Sprintf("%s-sync-%d", f.name, f.syncs)); err != nil {
		return err
	}
	return f.File.Sync()
}

func (f *durableResumeFile) Close() error {
	f.closed = true
	return f.File.Close()
}

func traceDurableResume(trace *durableResumeTrace, resumed **FileWAL, file **durableResumeFile) resumeFileWALFunc {
	return func(path string, encode func(MutationOp) ([]byte, error), decode func([]byte) (MutationOp, error), visit func(Entry) error) (*FileWAL, error) {
		wal, err := ResumeFileWAL(path, encode, decode, visit)
		if err != nil {
			return nil, err
		}
		*file = &durableResumeFile{File: wal.file.(*os.File), name: "wal", trace: trace}
		wal.file = *file
		*resumed = wal
		return wal, nil
	}
}

func TestResumeLogFromFileWALWithDurableTipBarriers(t *testing.T) {
	for _, ahead := range []bool{false, true} {
		t.Run(fmt.Sprintf("wal-ahead=%v", ahead), func(t *testing.T) {
			path, lease, tip := durableTipResumeFixture(t, ahead)
			before, err := os.Stat(path + ".tip")
			if err != nil {
				t.Fatal(err)
			}
			trace := &durableResumeTrace{}
			tipFile := &durableResumeFile{File: tip.file.(*os.File), name: "tip", trace: trace}
			tip.file = tipFile
			var wal *FileWAL
			var walFile *durableResumeFile
			var restored []Entry
			var log *Log
			var owner io.Closer
			checkNoBarrier := func() {
				if len(trace.events) != 0 {
					t.Fatalf("application callback after recovery barrier: %v", trace.events)
				}
			}
			err = lease.WithPath(func(canonical string) error {
				var resumeErr error
				log, owner, resumeErr = resumeLogFromFileWALWithDurableTip(canonical, Options{Capacity: 1}, fileWALStringEncode,
					func(p []byte) (MutationOp, error) { checkNoBarrier(); return fileWALStringDecode(p) },
					func(e Entry) error { checkNoBarrier(); return fileWALCutValidEntry(e) },
					func(e Entry) error { checkNoBarrier(); restored = append(restored, e); return nil }, tip,
					traceDurableResume(trace, &wal, &walFile), func(dir string) error {
						if dir != filepath.Dir(lease.Path()) || wal.tip != nil || tip.bound {
							t.Fatalf("directory barrier path/binding = %q, %p, %v", dir, wal.tip, tip.bound)
						}
						if err := trace.record("directory-sync"); err != nil {
							return err
						}
						return syncFileWALDirectory(dir)
					})
				return resumeErr
			})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			want := []string{"wal-sync-1", "tip-sync-1", "directory-sync"}
			if ahead {
				want = []string{"wal-sync-1", "tip-write", "tip-sync-1", "tip-sync-2", "directory-sync"}
			}
			if !reflect.DeepEqual(trace.events, want) {
				t.Fatalf("recovery order = %v, want %v", trace.events, want)
			}
			if log.wal != wal || wal.file != walFile || tip.file != tipFile || walFile.closed || tipFile.closed {
				t.Fatal("Log did not retain the exact recovered and synced file descriptors")
			}
			if len(restored) != 2 || log.Len() != 1 || log.RetainedEntries()[0].Seq != 2 {
				t.Fatalf("restored = %v, retained = %v", restored, log.RetainedEntries())
			}
			after, err := os.Stat(path + ".tip")
			if err != nil {
				t.Fatal(err)
			}
			growth := int64(0)
			if ahead {
				growth = fileWALTipRecordSize
			}
			if after.Size()-before.Size() != growth {
				t.Fatalf("tip growth = %d, want %d", after.Size()-before.Size(), growth)
			}
			cut, err := InspectFileWALCut(path, 2, fileWALStringDecode, fileWALCutValidEntry)
			if err != nil {
				t.Fatal(err)
			}
			witness, err := wal.TipWitness(2)
			if err != nil || witness.ChainSHA256 != cut.ObservedChainSHA256 {
				t.Fatalf("recovered witness = %+v, %v", witness, err)
			}
			entry, err := log.CommitWithPublication("third", hlc.Timestamp{}, func(Entry) {})
			if err != nil || entry.Seq != 3 {
				t.Fatalf("next append = %+v, %v", entry, err)
			}
			if wal.file != walFile || tip.file != tipFile || walFile.syncs != 2 {
				t.Fatal("append replaced the descriptor qualified during recovery")
			}
			if err := owner.Close(); err != nil || !walFile.closed || tipFile.closed {
				t.Fatalf("owner close did not preserve borrowed tip ownership: %v", err)
			}
		})
	}
}

func TestResumeLogFromFileWALWithDurableTipBarrierFailures(t *testing.T) {
	for _, ahead := range []bool{false, true} {
		events := []string{"wal-sync-1", "tip-sync-1", "directory-sync"}
		if ahead {
			events = []string{"wal-sync-1", "tip-write", "tip-sync-1", "tip-sync-2", "directory-sync"}
		}
		for index, fault := range events {
			for _, panics := range []bool{false, true} {
				t.Run(fmt.Sprintf("ahead=%v/%s/panic=%v", ahead, fault, panics), func(t *testing.T) {
					path, lease, tip := durableTipResumeFixture(t, ahead)
					failure := errors.New("injected recovery failure")
					trace := &durableResumeTrace{fault: fault, panic: panics, err: failure}
					tipFile := &durableResumeFile{File: tip.file.(*os.File), name: "tip", trace: trace}
					tip.file = tipFile
					var wal *FileWAL
					var walFile *durableResumeFile
					var log *Log
					var owner io.Closer
					var err error
					var caught any
					func() {
						defer func() { caught = recover() }()
						err = lease.WithPath(func(canonical string) error {
							log, owner, err = resumeLogFromFileWALWithDurableTip(canonical, Options{}, fileWALStringEncode,
								fileWALStringDecode, fileWALCutValidEntry, func(Entry) error { return nil }, tip,
								traceDurableResume(trace, &wal, &walFile), func(string) error { return trace.record("directory-sync") })
							return err
						})
					}()
					if panics && caught != failure || !panics && (!errors.Is(err, failure) || caught != nil) {
						t.Fatalf("failure = %v, panic = %v", err, caught)
					}
					if log != nil || owner != nil || wal == nil || !wal.closed || !walFile.closed || wal.tip != nil || tip.bound {
						t.Fatalf("failure exposed live/bound owner: log=%p owner=%v wal=%+v tip=%+v", log, owner, wal, tip)
					}
					if !reflect.DeepEqual(trace.events, events[:index+1]) {
						t.Fatalf("barriers after failure: %v, want %v", trace.events, events[:index+1])
					}
					if tipFile.closed {
						t.Fatal("bridge closed borrowed tip")
					}
					if contender, err := AcquireFileWALLease(path); !errors.Is(err, ErrFileWALLeaseBusy) {
						if contender != nil {
							_ = contender.Close()
						}
						t.Fatalf("bridge released caller's lease: %v", err)
					}
					if err := tip.Close(); err != nil {
						t.Fatal(err)
					}
					if err := lease.Close(); err != nil {
						t.Fatal(err)
					}
					contender, err := AcquireFileWALLease(path)
					if err != nil {
						t.Fatalf("cleanup left family busy: %v", err)
					}
					_ = contender.Close()
					for _, name := range []string{path, path + ".tip", path + ".lease"} {
						if _, err := os.Stat(name); err != nil {
							t.Fatalf("failure removed %s: %v", name, err)
						}
					}
				})
			}
		}
	}
}

func TestResumeLogFromFileWALWithDurableTipCallbacksBeforeBarriers(t *testing.T) {
	for _, phase := range []string{"decode", "validate", "restore"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%v", phase, panics), func(t *testing.T) {
				path, lease, tip := durableTipResumeFixture(t, true)
				before, err := os.ReadFile(path + ".tip")
				if err != nil {
					t.Fatal(err)
				}
				failure := errors.New("last record reconstruction failed")
				trace := &durableResumeTrace{}
				tipFile := &durableResumeFile{File: tip.file.(*os.File), name: "tip", trace: trace}
				tip.file = tipFile
				fail := func() error {
					if panics {
						panic(failure)
					}
					return failure
				}
				decode := func(p []byte) (MutationOp, error) {
					if phase == "decode" && string(p) == "second" {
						return nil, fail()
					}
					return fileWALStringDecode(p)
				}
				validate := func(e Entry) error {
					if phase == "validate" && e.Seq == 2 {
						return fail()
					}
					return nil
				}
				restore := func(e Entry) error {
					if phase == "restore" && e.Seq == 2 {
						return fail()
					}
					return nil
				}
				var log *Log
				var owner io.Closer
				var caught any
				func() {
					defer func() { caught = recover() }()
					err = lease.WithPath(func(canonical string) error {
						log, owner, err = ResumeLogFromFileWALWithDurableTip(canonical, Options{}, fileWALStringEncode, decode, validate, restore, tip)
						return err
					})
				}()
				if panics && caught != failure || !panics && (err == nil || caught != nil) || log != nil || owner != nil {
					t.Fatalf("failed reconstruction = %p, %v, %v, panic %v", log, owner, err, caught)
				}
				if len(trace.events) != 0 || tip.bound {
					t.Fatalf("reconstruction advanced tip: %v", trace.events)
				}
				after, err := os.ReadFile(path + ".tip")
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("failed reconstruction modified tip: %v", err)
				}
			})
		}
	}
}

func TestDurableTipRecoverySyncGuards(t *testing.T) {
	for _, state := range []string{"closed", "unusable", "bound", "wrong-frontier"} {
		t.Run("wal/"+state, func(t *testing.T) {
			path, _, tip := durableTipResumeFixture(t, false)
			cut, err := InspectFileWALCut(path, 2, fileWALStringDecode, fileWALCutValidEntry)
			if err != nil {
				t.Fatal(err)
			}
			wal, err := ResumeFileWAL(path, fileWALStringEncode, fileWALStringDecode, func(Entry) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			trace := &durableResumeTrace{}
			file := &durableResumeFile{File: wal.file.(*os.File), name: "wal", trace: trace}
			wal.file = file
			defer file.Close()
			switch state {
			case "closed":
				wal.closed = true
			case "unusable":
				wal.unusable = true
			case "bound":
				wal.tip = tip
			case "wrong-frontier":
				cut.ObservedChainSHA256[0] ^= 1
			}
			if err := wal.syncRecoveredPrefix(cut); err == nil || len(trace.events) != 0 {
				t.Fatalf("guarded WAL recovery = %v, events %v", err, trace.events)
			}
		})
	}
	for _, state := range []string{"closed", "unusable", "bound", "unverified"} {
		t.Run("tip/"+state, func(t *testing.T) {
			_, _, tip := durableTipResumeFixture(t, false)
			trace := &durableResumeTrace{}
			file := &durableResumeFile{File: tip.file.(*os.File), name: "tip", trace: trace}
			tip.file = file
			defer file.Close()
			tip.verified = true
			switch state {
			case "closed":
				tip.closed = true
			case "unusable":
				tip.unusable = true
			case "bound":
				tip.bound = true
			case "unverified":
				tip.verified = false
			}
			if err := tip.syncVerifiedPrefix(); err == nil || len(trace.events) != 0 {
				t.Fatalf("guarded tip recovery = %v, events %v", err, trace.events)
			}
		})
	}
}

func TestDurableTipRecoveryMixedFileWALFailures(t *testing.T) {
	for _, sentinel := range []error{ErrClosed, ErrSeqExhausted, context.Canceled, &DefiniteWALAbort{Cause: errors.New("abort-shaped cause")}} {
		for _, reverse := range []bool{false, true} {
			for _, phase := range []string{"wal-write", "wal-sync-1"} {
				t.Run(fmt.Sprintf("%s/reverse=%v/%s", sentinel, reverse, phase), func(t *testing.T) {
					_, lease, wal, tip, binding := fileWALTipFixture(t)
					path := lease.Path()
					mixed := fmt.Errorf("wrapped mixed failure: %w", errors.Join(ErrWALIndeterminate, sentinel))
					if reverse {
						mixed = errors.Join(sentinel, fmt.Errorf("wrapped uncertainty: %w", ErrWALIndeterminate))
					}
					trace := &durableResumeTrace{fault: phase, err: mixed}
					file := &durableResumeFile{File: wal.file.(*os.File), name: "wal", trace: trace}
					wal.file = file
					log := New(Options{WAL: wal})
					published := false
					_, err := log.CommitWithPublication("uncertain", fileWALEntry(1, "uncertain").HLC, func(Entry) { published = true })
					if !errors.Is(err, ErrWALIndeterminate) || !errors.Is(err, sentinel) || published || !wal.unusable {
						t.Fatalf("mixed I/O failure = %v, publication=%v, unusable=%v", err, published, wal.unusable)
					}
					iocount := len(trace.events)
					if _, err := log.CommitWithPublication("retry", hlc.Timestamp{}, nil); !errors.Is(err, ErrWALIndeterminate) {
						t.Fatalf("retry after uncertain frame = %v", err)
					}
					if len(trace.events) != iocount || log.Len() != 0 {
						t.Fatalf("uncertain attempt permitted I/O/publication: %v, ring %v", trace.events, log.RetainedEntries())
					}
					if _, err := wal.TipWitness(0); !errors.Is(err, ErrFileWALUnusable) {
						t.Fatalf("uncertain witness = %v", err)
					}
					if err := log.Close(); err != nil {
						t.Fatal(err)
					}
					if err := wal.Close(); err != nil {
						t.Fatal(err)
					}
					if err := tip.Close(); err != nil {
						t.Fatal(err)
					}
					tip, err = ResumeFileWALTipJournal(path, binding)
					if err != nil {
						t.Fatal(err)
					}
					defer tip.Close()
					var restored []Entry
					resumed, owner, err := ResumeLogFromFileWALWithDurableTip(path, Options{}, fileWALStringEncode, fileWALStringDecode,
						fileWALCutValidEntry, func(e Entry) error { restored = append(restored, e); return nil }, tip)
					if err != nil {
						t.Fatal(err)
					}
					defer owner.Close()
					if len(restored) != 1 || restored[0].Op != "uncertain" {
						t.Fatalf("recovery changed uncertain value: %v", restored)
					}
					if e, err := resumed.CommitWithPublication("next", hlc.Timestamp{}, nil); err != nil || e.Seq != 2 {
						t.Fatalf("recovery reused uncertain sequence: %+v, %v", e, err)
					}
				})
			}
		}
	}
}
