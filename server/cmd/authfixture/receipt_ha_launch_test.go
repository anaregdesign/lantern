package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReceiptHALaunchOwnsCohortThroughCertificationAndCleanup(t *testing.T) {
	binary := fixtureServerBinary(t)
	for _, phase := range []string{"unconfigured", "certified", "partial_failure", "cancelled"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			dir := filepath.Join(t.TempDir(), "trust")
			h, public, peer, err := reserveReceiptHALaunch(ctx, binary, dir, 20*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer h.close()
			addresses := []string{h.public.listener.Addr().String()}
			for _, listener := range h.peers {
				addresses = append(addresses, listener.Addr().String())
			}
			for _, launch := range h.launches {
				// Match each owned role's actual bind address: wildcard public,
				// IPv4 loopback data. Different wildcard/specific address binds
				// do not have identical exclusion rules on every native OS.
				addresses = append(addresses, ":"+strconv.Itoa(launch.public), net.JoinHostPort("127.0.0.1", strconv.Itoa(launch.peer)))
			}
			checkHeld := func() {
				t.Helper()
				for _, address := range addresses {
					if listener, e := net.Listen("tcp", address); e == nil {
						_ = listener.Close()
						t.Fatal("competing bind entered owned cohort")
					}
				}
			}
			checkHeld()
			if phase != "unconfigured" {
				f, err := generate(dir, public, peer, "off", "")
				if err != nil {
					t.Fatal(err)
				}
				if err = h.bindNodes(&f); err != nil {
					t.Fatal(err)
				}
				if phase == "partial_failure" {
					f.Nodes[3].Environment["LANTERN_PORT"] = "1"
				}
				if phase == "cancelled" {
					cancel()
				}
				err = h.configure(ctx, f)
				if phase == "certified" {
					if err != nil {
						t.Fatal(err)
					}
					if err = h.public.resume(h.launches[3]); err != nil {
						t.Fatal(err)
					}
					if err = waitFixtureReady(ctx, f, h.children()); err != nil {
						var diagnostic bytes.Buffer
						writeFixtureFailure(&diagnostic, &fixtureFailure{stage: "readiness", cause: err})
						t.Fatalf("certified readiness: %v; %s", err, diagnostic.String())
					}
					checkHeld()
				} else if err == nil {
					t.Fatal("failed cohort configured")
				}
			}
			h.close()
			h.close()
			for _, launch := range h.launches {
				select {
				case <-launch.process.done:
				default:
					t.Fatal("child was not reaped")
				}
			}
			for _, address := range addresses {
				listener, err := net.Listen("tcp", address)
				if err != nil {
					t.Fatal("owned socket leaked", err)
				}
				_ = listener.Close()
			}
		})
	}
}

func TestReceiptHAOrderlyStopRejectsFailedOrUnjoinedChild(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestReceiptHAOrderlyStopChild$")
			command.Env = append(os.Environ(), "LANTERN_FIXTURE_STOP_CHILD="+scenario)
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			log, err := os.CreateTemp(t.TempDir(), "child-")
			if err != nil {
				t.Fatal(err)
			}
			command.Stdout = log
			command.Stderr = log
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			process := &fixtureProcess{command: command, log: log, done: make(chan struct{})}
			go func() { _ = command.Wait(); close(process.done) }()
			launch := &fixtureLaunch{process: process, input: input, output: io.NopCloser(strings.NewReader(""))}
			defer launch.close()
			timeout := 5 * time.Second
			if scenario == "timeout" {
				timeout = 10 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			err = launch.orderlyStop(ctx)
			if (err == nil) != (scenario == "success") {
				t.Fatal("wrong orderly exit verdict", scenario, err)
			}
		})
	}
}

func TestReceiptHAOrderlyStopChild(t *testing.T) {
	scenario := os.Getenv("LANTERN_FIXTURE_STOP_CHILD")
	if scenario == "" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if scenario == "timeout" {
		time.Sleep(time.Second)
	}
	if scenario == "failure" {
		os.Exit(2)
	}
	os.Exit(0)
}
