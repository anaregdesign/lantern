//go:build linux || darwin

package replication

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"connectrpc.com/connect"
)

func TestGraphSnapshotSpoolConfigValidate(t *testing.T) {
	config := snapshotScratchTestConfig(t, 64<<10)
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(config.Directory); err != nil || len(entries) != 0 {
		t.Fatalf("Validate wrote to directory: entries=%v err=%v", entries, err)
	}
	missing := config
	missing.Directory = filepath.Join(config.Directory, "not-created")
	if err := missing.ValidateConfiguration(); err != nil {
		t.Fatal("pure bound validation required runtime directory", err)
	}
	if err := missing.Validate(); err == nil {
		t.Fatal("runtime validation stopped checking private storage")
	}
	for _, test := range []struct {
		name string
		edit func(*GraphSnapshotSpoolConfig)
	}{
		{"NoDirectory", func(c *GraphSnapshotSpoolConfig) { c.Directory = "" }},
		{"RelativeDirectory", func(c *GraphSnapshotSpoolConfig) { c.Directory = "scratch" }},
		{"MissingDirectory", func(c *GraphSnapshotSpoolConfig) { c.Directory = filepath.Join(c.Directory, "missing") }},
		{"ZeroBytes", func(c *GraphSnapshotSpoolConfig) { c.MaxDiskBytes = 0 }},
		{"OwnerExceedsAllowance", func(c *GraphSnapshotSpoolConfig) { c.MaxDiskBytes = snapshotScratchMetadataBytes - 1 }},
		{"ByteOverflow", func(c *GraphSnapshotSpoolConfig) { c.MaxDiskBytes = uint64(math.MaxInt64) + 1 }},
		{"ZeroFrame", func(c *GraphSnapshotSpoolConfig) { c.MaxFrameBytes = 0 }},
		{"NegativeFrame", func(c *GraphSnapshotSpoolConfig) { c.MaxFrameBytes = -1 }},
		{"FrameExceedsAllowance", func(c *GraphSnapshotSpoolConfig) { c.MaxFrameBytes = int(c.MaxDiskBytes) + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := config
			test.edit(&invalid)
			if err := invalid.Validate(); err == nil {
				t.Fatal("invalid spool configuration accepted")
			}
			if test.name != "MissingDirectory" && invalid.ValidateConfiguration() == nil {
				t.Fatal("pure bound validation accepted invalid path/capacity")
			}
		})
	}
}

func TestSnapshotScratchRoundTripAndRefund(t *testing.T) {
	config := snapshotScratchTestConfig(t, 4*snapshotScratchBlockBytes)
	scratch := snapshotScratchTestOwner(t, t.Context(), config)
	assertSnapshotScratchStats(t, scratch, 4096, 0)
	file, err := scratch.create()
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotScratchStats(t, scratch, 8192, 1)
	if n, err := file.Write([]byte("first")); err != nil || n != 5 {
		t.Fatalf("write=%d, %v", n, err)
	}
	if n, err := file.WriteAt([]byte("F"), 0); err != nil || n != 1 {
		t.Fatalf("overwrite=%d, %v", n, err)
	}
	if file.Size() != 5 {
		t.Fatalf("size=%d", file.Size())
	}
	assertSnapshotScratchStats(t, scratch, 12288, 1)
	assertSnapshotScratchOnlyOwner(t, config.Directory)
	if offset, err := file.Seek(-5, io.SeekCurrent); err != nil || offset != 0 {
		t.Fatalf("seek=%d, %v", offset, err)
	}
	got := make([]byte, 5)
	if n, err := io.ReadFull(file, got); err != nil || n != len(got) || string(got) != "First" {
		t.Fatalf("read=%d %q, %v", n, got, err)
	}
	if n, err := file.Read(got); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("EOF read=%d, %v", n, err)
	}
	if n, err := file.ReadAt(got[:1], 1); err != nil || n != 1 || got[0] != 'i' {
		t.Fatalf("ReadAt=%d %q, %v", n, got[:1], err)
	}
	if _, err := file.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek accepted")
	}
	if _, err := file.Seek(0, 999); err == nil {
		t.Fatal("unknown seek origin accepted")
	}
	slot := file.slot
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal("Close is not idempotent", err)
	}
	assertSnapshotScratchStats(t, scratch, 4096, 0)
	reused, err := scratch.create()
	if err != nil || reused.slot != slot {
		t.Fatalf("closed slot not reusable: file=%v err=%v", reused, err)
	}
	if err := scratch.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reused.Write([]byte("closed")); !errors.Is(err, errSnapshotScratchClosed) {
		t.Fatalf("closed owner accepted write: %v", err)
	}
	assertSnapshotScratchOnlyOwner(t, config.Directory)
}

func TestSnapshotScratchAggregateQuotaAndSlots(t *testing.T) {
	t.Run("AggregateRoundedExtents", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 5*snapshotScratchBlockBytes)
		scratch := snapshotScratchTestOwner(t, t.Context(), config)
		first, err := scratch.create()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := first.WriteAt([]byte{1}, 4095); err != nil {
			t.Fatal(err)
		}
		second, err := scratch.create()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := second.Write([]byte{2}); err != nil {
			t.Fatal(err)
		}
		assertSnapshotScratchStats(t, scratch, config.MaxDiskBytes, 2)
		if _, err := second.WriteAt([]byte{3}, 4096); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("aggregate overflow not ResourceExhausted: %v", err)
		}
		assertSnapshotScratchStats(t, scratch, 0, 0)
		if _, err := first.Read(make([]byte, 1)); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("quota failure did not close all files: %v", err)
		}
		assertSnapshotScratchOnlyOwner(t, config.Directory)
	})
	t.Run("FixedSlotLimit", func(t *testing.T) {
		scratch := snapshotScratchTestOwner(t, t.Context(), snapshotScratchTestConfig(t, (snapshotScratchSlots+2)*snapshotScratchMetadataBytes))
		for range snapshotScratchSlots {
			if _, err := scratch.create(); err != nil {
				t.Fatal(err)
			}
		}
		assertSnapshotScratchStats(t, scratch, (snapshotScratchSlots+1)*snapshotScratchMetadataBytes, snapshotScratchSlots)
		if _, err := scratch.create(); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("slot overflow not ResourceExhausted: %v", err)
		}
		assertSnapshotScratchStats(t, scratch, 0, 0)
	})
	t.Run("SparseGrowthCountsEntireLogicalExtent", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 128<<20)
		scratch := snapshotScratchTestOwner(t, t.Context(), config)
		file, err := scratch.create()
		if err != nil {
			t.Fatal(err)
		}
		const offset = 65 << 20
		if _, err := file.WriteAt([]byte("end"), offset); err != nil {
			t.Fatal("extent above former64MiB stage limit rejected", err)
		}
		assertSnapshotScratchStats(t, scratch, offset+3*snapshotScratchBlockBytes, 1)
		got := make([]byte, 3)
		if _, err := file.ReadAt(got, offset); err != nil || string(got) != "end" {
			t.Fatalf("large extent read=%q, %v", got, err)
		}
		if _, err := file.WriteAt([]byte{1}, int64(config.MaxDiskBytes)); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("sparse hole bypassed quota: %v", err)
		}
	})
	t.Run("OffsetOverflow", func(t *testing.T) {
		scratch := snapshotScratchTestOwner(t, t.Context(), snapshotScratchTestConfig(t, 64<<10))
		file, err := scratch.create()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteAt([]byte{1, 2}, math.MaxInt64); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("offset overflow accepted: %v", err)
		}
		assertSnapshotScratchStats(t, scratch, 0, 0)
	})
}

func TestSnapshotScratchIOFailureClosesOwner(t *testing.T) {
	for _, test := range []struct {
		name string
		want error
		hook func(*snapshotScratch)
	}{
		{"ENOSPCPartialWrite", syscall.ENOSPC, func(s *snapshotScratch) {
			s.writeAt = func(f *os.File, p []byte, offset int64) (int, error) {
				n, err := f.WriteAt(p[:len(p)/2], offset)
				return n, errors.Join(syscall.ENOSPC, err)
			}
		}},
		{"ShortWrite", io.ErrShortWrite, func(s *snapshotScratch) {
			s.writeAt = func(f *os.File, p []byte, offset int64) (int, error) { return f.WriteAt(p[:len(p)/2], offset) }
		}},
		{"InvalidWriteCount", io.ErrShortWrite, func(s *snapshotScratch) {
			s.writeAt = func(_ *os.File, p []byte, _ int64) (int, error) { return len(p) + 1, nil }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := snapshotScratchTestConfig(t, 64<<10)
			scratch := snapshotScratchTestOwner(t, t.Context(), config)
			file, err := scratch.create()
			if err != nil {
				t.Fatal(err)
			}
			other, err := scratch.create()
			if err != nil {
				t.Fatal(err)
			}
			test.hook(scratch)
			if _, err := file.Write([]byte("data")); !errors.Is(err, test.want) {
				t.Fatalf("storage failure lost: %v", err)
			}
			if other.file != nil || file.file != nil {
				t.Fatal("storage failure left an open scratch file")
			}
			assertSnapshotScratchStats(t, scratch, 0, 0)
			if err := scratch.Close(); !errors.Is(err, test.want) {
				t.Fatalf("owner lost sticky failure: %v", err)
			}
			assertSnapshotScratchOnlyOwner(t, config.Directory)
			reopened := snapshotScratchTestOwner(t, t.Context(), config)
			if err := reopened.Close(); err != nil {
				t.Fatal("failure retained lease", err)
			}
		})
	}
	t.Run("ReadError", func(t *testing.T) {
		scratch := snapshotScratchTestOwner(t, t.Context(), snapshotScratchTestConfig(t, 64<<10))
		file, err := scratch.create()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("data")); err != nil {
			t.Fatal(err)
		}
		scratch.readAt = func(*os.File, []byte, int64) (int, error) { return 0, io.ErrUnexpectedEOF }
		if _, err := file.ReadAt(make([]byte, 1), 0); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("read error lost: %v", err)
		}
		assertSnapshotScratchStats(t, scratch, 0, 0)
	})
}

func TestSnapshotScratchCancellationAndCloseErrors(t *testing.T) {
	t.Run("CanceledConstructorWritesNothing", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 64<<10)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := newSnapshotScratch(ctx, config); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled constructor=%v", err)
		}
		if entries, err := os.ReadDir(config.Directory); err != nil || len(entries) != 0 {
			t.Fatalf("canceled constructor wrote artifacts: %v %v", entries, err)
		}
	})
	for _, afterIO := range []bool{false, true} {
		name := "BeforeIO"
		if afterIO {
			name = "AfterIO"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			scratch := snapshotScratchTestOwner(t, ctx, snapshotScratchTestConfig(t, 64<<10))
			file, err := scratch.create()
			if err != nil {
				t.Fatal(err)
			}
			called := false
			scratch.writeAt = func(f *os.File, p []byte, offset int64) (int, error) {
				called = true
				n, err := f.WriteAt(p, offset)
				cancel()
				return n, err
			}
			if !afterIO {
				cancel()
			}
			if _, err := file.Write([]byte("data")); !errors.Is(err, context.Canceled) || called != afterIO {
				t.Fatalf("cancel failed: called=%v err=%v", called, err)
			}
			assertSnapshotScratchStats(t, scratch, 0, 0)
		})
	}
	t.Run("FileCloseFailureStillClosesEveryDescriptor", func(t *testing.T) {
		scratch := snapshotScratchTestOwner(t, t.Context(), snapshotScratchTestConfig(t, 64<<10))
		file, err := scratch.create()
		if err != nil {
			t.Fatal(err)
		}
		other, err := scratch.create()
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("injected close failure")
		allClosed, retainedCharge, closeCount := true, true, 0
		scratch.afterClose = func(f *os.File) error {
			_, err := f.Stat()
			allClosed = allClosed && errors.Is(err, os.ErrClosed)
			retainedCharge = retainedCharge && scratch.usedBytes >= 2*snapshotScratchMetadataBytes
			closeCount++
			return injected
		}
		if err := file.Close(); !errors.Is(err, injected) {
			t.Fatalf("file close error lost: %v", err)
		}
		if !allClosed || !retainedCharge || closeCount != 2 || file.file != nil || other.file != nil {
			t.Fatalf("failed cleanup: closed=%v charged=%v closes=%d", allClosed, retainedCharge, closeCount)
		}
		assertSnapshotScratchStats(t, scratch, 0, 0)
		if err := scratch.Close(); !errors.Is(err, injected) || closeCount != 2 {
			t.Fatalf("failed owner cleanup not sticky/idempotent: closes=%d err=%v", closeCount, err)
		}
	})
	t.Run("OwnerCloseErrorAfterRealLeaseRelease", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 64<<10)
		scratch := snapshotScratchTestOwner(t, t.Context(), config)
		injected := errors.New("injected owner cleanup failure")
		var reopenError error
		calls := 0
		scratch.afterOwnerClose = func() error {
			calls++
			var reopened *snapshotScratch
			reopened, reopenError = newSnapshotScratch(t.Context(), config)
			if reopenError == nil {
				reopenError = reopened.Close()
			}
			return injected
		}
		if err := scratch.Close(); !errors.Is(err, injected) || reopenError != nil || calls != 1 {
			t.Fatalf("owner close hook preceded lease release: calls=%d reopen=%v close=%v", calls, reopenError, err)
		}
		if err := scratch.Close(); !errors.Is(err, injected) || calls != 1 {
			t.Fatalf("owner close error not sticky/idempotent: calls=%d close=%v", calls, err)
		}
		assertSnapshotScratchStats(t, scratch, 0, 0)
	})
}

func TestSnapshotScratchReadAtDoesNotSharePosition(t *testing.T) {
	scratch := snapshotScratchTestOwner(t, t.Context(), snapshotScratchTestConfig(t, 64<<10))
	file, err := scratch.create()
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("0123456789")
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(2, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 3)
	if _, err := file.ReadAt(got, 6); err != nil || !bytes.Equal(got, []byte("678")) {
		t.Fatalf("ReadAt=%q %v", got, err)
	}
	if _, err := file.Read(got); err != nil || !bytes.Equal(got, []byte("234")) {
		t.Fatalf("ReadAt moved sequential position: %q %v", got, err)
	}
}
