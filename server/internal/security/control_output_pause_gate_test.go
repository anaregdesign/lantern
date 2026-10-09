//go:build darwin

package security

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The observer pauses only its owned test child. The child uses the actual
// configured-source time owner, actual credential provider and real TLS Connect
// unary response. Physical OS sleep/reboot is neither performed nor claimed.
func TestAuthorityOutputNativePauseGate(t *testing.T) {
	if os.Getenv("LANTERN_TEST_CURRENT_OUTPUT_PAUSE") != "1" {
		t.Skip("explicit finite native output campaign")
	}
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			if root := os.Getenv("LANTERN_CURRENT_EVIDENCE_DIR"); root != "" {
				dir = filepath.Join(root, "output-pause-"+phase)
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCurrentAuthorityGenuineProducerGate$", "-test.v")
			child.Env = append(os.Environ(), "LANTERN_TEST_CURRENT_NATIVE=1", "LANTERN_CURRENT_OUTPUT_CHILD="+phase, "LANTERN_CURRENT_OUTPUT_CHILD_DIR="+dir)
			log, err := os.Create(filepath.Join(dir, "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			child.Stdout, child.Stderr = log, log
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			deadline := time.Now().Add(150 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					raw, _ := os.ReadFile(filepath.Join(dir, "child.log"))
					t.Fatalf("child did not reach boundary: %s", raw)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := child.Process.Signal(unix.SIGSTOP); err != nil {
				t.Fatal(err)
			}
			for {
				var status unix.WaitStatus
				pid, err := unix.Wait4(child.Process.Pid, &status, unix.WUNTRACED|unix.WNOHANG, nil)
				if err != nil {
					t.Fatal(err)
				}
				if pid != 0 {
					if uint32(status)&0xff != 0x7f || uint32(status)>>8&0xff != uint32(unix.SIGSTOP) {
						t.Fatalf("not stopped: %v", status)
					}
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			sampler, err := newDarwinAuthorityTimeSampler()
			if err != nil {
				t.Fatal(err)
			}
			start, err := sampler()
			if err != nil {
				t.Fatal(err)
			}
			premises := authorityOperationalTimePremises()
			var elapsed authorityTimeRange
			for {
				now, err := sampler()
				if err != nil {
					t.Fatal(err)
				}
				elapsed, err = premises.elapsed(start, now)
				if err != nil {
					t.Fatal(err)
				}
				if elapsed.low >= uint64(16*time.Second) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := child.Process.Signal(unix.SIGCONT); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "continue"), []byte("independently observed expiry"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil {
				raw, _ := os.ReadFile(filepath.Join(dir, "child.log"))
				t.Fatalf("child: %v\n%s", err, raw)
			}
			receipt, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			authorization, _ := os.ReadFile(filepath.Join(dir, "authorization.json"))
			t.Logf("phase=%s independent_native_pause=[%d,%d] authorization=%s physical_receipt=%s artifact=%s; approved A has no send deadline", phase, elapsed.low, elapsed.high, authorization, receipt, dir)
		})
	}
}
