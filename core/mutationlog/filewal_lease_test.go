package mutationlog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFileWALLeaseCrossProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("FileWAL lease is unavailable on this platform")
	}
	if path := os.Getenv("LANTERN_TEST_FILEWAL_LEASE_PATH"); path != "" {
		lease, err := AcquireFileWALLease(path)
		if err != nil {
			fmt.Fprintln(os.Stdout, err)
			return
		}
		defer lease.Close()
		fmt.Fprintln(os.Stdout, "locked")
		time.Sleep(time.Hour) // parent kills the process to verify crash release
		return
	}

	path := filepath.Join(t.TempDir(), "receipt.wal")
	aliasPath := path
	if runtime.GOOS != "windows" {
		aliasDir := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(filepath.Dir(path), aliasDir); err != nil {
			t.Fatal(err)
		}
		aliasPath = filepath.Join(aliasDir, filepath.Base(path))
	}
	if _, err := AcquireFileWALLease(""); err == nil {
		t.Fatal("empty WAL path acquired a lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileWALLeaseCrossProcess$")
	child.Env = append(os.Environ(), "LANTERN_TEST_FILEWAL_LEASE_PATH="+path)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("child did not acquire the WAL path: %q, %v", line, err)
	}
	if lease, err := AcquireFileWALLease(aliasPath); lease != nil || !errors.Is(err, ErrFileWALLeaseBusy) {
		if lease != nil {
			_ = lease.Close()
		}
		t.Fatalf("concurrent WAL owner = %p, %v; want busy", lease, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("killed child exited successfully")
	}
	lease, err := AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("WAL path remained locked after process exit: %v", err)
	}
	canonicalDir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(canonicalDir, filepath.Base(path)); lease.Path() != want {
		t.Fatalf("canonical WAL path = %q, want %q", lease.Path(), want)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second lease close: %v", err)
	}
	if _, err := os.Stat(path + ".lease"); err != nil {
		t.Fatalf("stable lease sidecar disappeared: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lease unexpectedly created the WAL: %v", err)
	}
	if runtime.GOOS != "windows" {
		linkedWAL := filepath.Join(t.TempDir(), "linked.wal")
		if err := os.Symlink(path, linkedWAL); err != nil {
			t.Fatal(err)
		}
		if got, err := AcquireFileWALLease(linkedWAL); got != nil || err == nil {
			t.Fatalf("symlink WAL path = %p, %v; want rejection", got, err)
		}
		linkedLease := filepath.Join(t.TempDir(), "linked-lease.wal")
		if err := os.Symlink(path+".lease", linkedLease+".lease"); err != nil {
			t.Fatal(err)
		}
		if got, err := AcquireFileWALLease(linkedLease); got != nil || err == nil {
			t.Fatalf("symlink lease path = %p, %v; want rejection", got, err)
		}
	}
}

func TestFileWALLeaseKeepsOwnershipThroughCallback(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("FileWAL lease is unavailable on this platform")
	}
	lease, err := AcquireFileWALLease(filepath.Join(t.TempDir(), "receipt.wal"))
	if err != nil {
		t.Fatal(err)
	}
	wantPath := lease.Path()
	entered := make(chan struct{})
	release := make(chan struct{})
	callbackDone := make(chan error, 1)
	go func() {
		callbackDone <- lease.WithPath(func(path string) error {
			close(entered)
			if path != wantPath {
				return errors.New("callback received a different WAL path")
			}
			<-release
			return nil
		})
	}()
	<-entered
	closeStarted := make(chan struct{})
	closeDone := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeDone <- lease.Close()
	}()
	<-closeStarted
	select {
	case err := <-closeDone:
		t.Fatalf("lease closed during an active WAL pass: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-callbackDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	called := false
	if err := lease.WithPath(func(string) error { called = true; return nil }); !errors.Is(err, ErrFileWALLeaseClosed) || called {
		t.Fatalf("closed lease callback = %v, called %v", err, called)
	}
}
