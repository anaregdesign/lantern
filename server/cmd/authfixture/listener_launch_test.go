package main

import (
	"bytes"
	"context"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeListenerLaunchLifecycle(t *testing.T) {
	// Compile the actual production entry point once for this lifecycle matrix.
	// CI may supply its already-built exact-source binary on each native OS.
	binary := os.Getenv("LANTERN_TEST_LAUNCH_SERVER")
	if binary == "" {
		suffix := ""
		if runtime.GOOS == "windows" {
			suffix = ".exe"
		}
		binary = filepath.Join(t.TempDir(), "server"+suffix)
		command := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "..")
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build production child: %v\n%s", err, raw)
		}
	}
	failRestore := func(env map[string]string, dir string) {
		env["LANTERN_RECEIPT_WAL_MODE"] = "restart"
		env["LANTERN_RECEIPT_WAL_PATH"] = filepath.Join(dir, "missing-receipts.wal")
		env["LANTERN_RECEIPT_EPOCH"] = "42424242424242424242424242424242"
		env["LANTERN_RECEIPT_RETENTION"] = "1h"
		env["LANTERN_RECEIPT_MAX_ENTRIES"] = "32"
		env["LANTERN_RECEIPT_MAX_BYTES"] = "1048576"
		env["LANTERN_BACKUP_RESTORE_ON_START"] = "false"
	}
	assertReleased := func(t *testing.T, l *fixtureLaunch) {
		t.Helper()
		select {
		case <-l.process.done:
		default:
			t.Fatal("child was not reaped")
		}
		for _, p := range []int{l.public, l.peer} {
			if p == 0 {
				continue
			}
			listener, err := net.Listen("tcp", ":"+strconv.Itoa(p))
			if err != nil {
				t.Fatal("child retained socket after reaping", err)
			}
			_ = listener.Close()
		}
	}
	for _, phase := range []string{"before_configuration_eof", "certified_tls_then_eof", "failed_restore", "failed_tls_readiness", "foreign_nonce", "cancel_before_configuration"} {
		t.Run(phase, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "trust")
			launch, err := startFixtureLaunch(t.Context(), binary, dir, false, time.Now().Add(15*time.Second))
			if err != nil {
				t.Fatal("reserve", err)
			}
			defer launch.close()
			competing, err := net.Listen("tcp", ":"+strconv.Itoa(launch.public))
			if err == nil {
				_ = competing.Close()
				t.Fatal("competing bind entered pre-configuration gap")
			}
			if phase == "foreign_nonce" {
				if _, err := launch.exchange(t.Context(), listenerlaunch.Message{Phase: "configure", Nonce: strings.Repeat("ac", 32), Environment: []string{"LANTERN_PORT=1"}}, "adopted"); err == nil {
					t.Fatal("foreign child capability acknowledged")
				}
			} else if phase == "cancel_before_configuration" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := launch.configure(ctx, []string{"LANTERN_PORT=1"}); err == nil {
					t.Fatal("cancelled launch acknowledged")
				}
			} else if phase != "before_configuration_eof" {
				result, err := generate(dir, []int{launch.public}, nil, "off", "")
				if err != nil {
					t.Fatal(err)
				}
				result.Nodes[0].Environment["LANTERN_TLS_CLIENT_CA_FILE"] = result.CAFile
				if phase == "failed_restore" {
					failRestore(result.Nodes[0].Environment, dir)
				}
				env, err := fixtureEnvironment(result.Nodes[0], nil)
				if err != nil {
					t.Fatal(err)
				}
				err = launch.configure(t.Context(), env)
				if phase == "failed_restore" {
					if err == nil {
						t.Fatal("failed restore acknowledged adoption")
					}
				} else {
					if err != nil {
						t.Fatal("certified adoption", err)
					}
					if phase == "failed_tls_readiness" {
						foreign, err := generate(filepath.Join(t.TempDir(), "foreign"), []int{12345}, nil, "off", "")
						if err != nil {
							t.Fatal(err)
						}
						result.CAFile = foreign.CAFile
					}
					ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
					err = waitFixtureReady(ctx, result, []*fixtureProcess{launch.process})
					cancel()
					if phase == "failed_tls_readiness" && err == nil {
						t.Fatal("unverified TLS became ready")
					}
					if phase == "certified_tls_then_eof" && err != nil {
						t.Fatal("verified native mTLS readiness", err)
					}
				}
			}
			// Closing the supervisor's lifetime pipe must work on Windows too;
			// no unsupported Windows process interrupt is needed for graceful exit.
			_ = launch.input.Close()
			select {
			case <-launch.process.done:
			case <-time.After(3 * time.Second):
				t.Fatal("pipe EOF did not stop and reap child")
			}
			launch.close()
			assertReleased(t, launch)
		})
	}
	t.Run("partial_cohort_failure", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "trust")
		launches, public, peer, err := reserveFixtureCohort(t.Context(), binary, dir, 2, true, 15*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer closeFixtureLaunches(launches)
		result, err := generate(dir, public, peer, "off", "")
		if err != nil {
			t.Fatal(err)
		}
		result.launches = launches
		// The second node cannot certify; the already-adopted first node and all
		// preallocated private listeners must be reaped without fixture publication.
		failRestore(result.Nodes[1].Environment, dir)
		var output bytes.Buffer
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		err = serveFixture(ctx, result, binary, dir, "", strings.NewReader(""), &output, 15*time.Second)
		if err == nil || output.Len() != 0 {
			t.Fatal("partial cohort published success", err)
		}
		for _, launch := range launches {
			assertReleased(t, launch)
		}
	})
}

func TestFixtureReservedEndpointValidation(t *testing.T) {
	for _, tc := range []struct {
		address string
		private bool
	}{
		{"127.0.0.1:1234", false}, {"[::]:1234", true}, {"127.0.0.2:1234", true}, {"127.0.0.1:0", true}, {"localhost:1234", true}, {"[fe80::1%en0]:1234", false},
	} {
		if _, err := fixtureReservedPort(tc.address, tc.private); err == nil {
			t.Fatal("wrong role/family/scope", tc)
		}
	}
}
