//go:build darwin

package security

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Crosses native sampler, UDP producer, interval calculus and anchor owner.
// The independent parent pauses only its owned child; never the OS or another
// process. This demonstrates process-pause behavior, not an OS-suspend test.
func TestAuthorityTimeNativePauseGate(t *testing.T) {
	if os.Getenv("LANTERN_TEST_NATIVE_TIME_PAUSE") != "1" {
		t.Skip("explicit finite native campaign only")
	}
	for _, tc := range []struct {
		stage string
		pause time.Duration
	}{
		{"before-send", 1200 * time.Millisecond},
		{"after-receive", 1200 * time.Millisecond},
		{"before-use", 61 * time.Second},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				var request [48]byte
				n, peer, err := server.ReadFromUDP(request[:])
				if err != nil || n != 48 {
					return
				}
				raw := fakeAuthorityNTPResponse([8]byte(request[40:48]))
				now := time.Now()
				stamp := uint64(now.Unix()+2_208_988_800)<<32 | uint64(now.Nanosecond())*(1<<32)/1_000_000_000
				binary.BigEndian.PutUint64(raw[16:24], stamp-(1<<32))
				binary.BigEndian.PutUint64(raw[32:40], stamp)
				binary.BigEndian.PutUint64(raw[40:48], stamp)
				_, _ = server.WriteToUDP(raw[:], peer)
			}()
			defer func() { _ = server.Close(); <-done }()
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), tc.pause+10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthorityTimeNativePauseChild$")
			child.Env = append(os.Environ(), "LANTERN_TIME_CHILD_STAGE="+tc.stage, "LANTERN_TIME_CHILD_ADDRESS="+server.LocalAddr().String(), "LANTERN_TIME_CHILD_DIR="+dir)
			log, err := os.Create(filepath.Join(dir, "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			child.Stdout, child.Stderr = log, log
			if err = child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var status unix.WaitStatus
				pid, err := unix.Wait4(child.Process.Pid, &status, unix.WUNTRACED|unix.WNOHANG, nil)
				if err != nil {
					t.Fatal(err)
				}
				if pid != 0 {
					// Darwin's sys/wait.h WIFSTOPPED/WSTOPSIG encoding. The
					// pinned Go BSD helper labels SIGSTOP as Continued; this
					// wait requests WUNTRACED and never WCONTINUED.
					if uint32(status)&0xff != 0x7f || uint32(status)>>8&0xff != uint32(unix.SIGSTOP) {
						t.Fatalf("child exited before pause: %v", status)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child did not stop")
				}
				time.Sleep(5 * time.Millisecond)
			}
			observedStart := time.Now()
			timer := time.NewTimer(tc.pause)
			<-timer.C
			if err = child.Process.Signal(unix.SIGCONT); err != nil {
				t.Fatal(err)
			}
			if err = child.Wait(); err != nil {
				b, _ := os.ReadFile(filepath.Join(dir, "child.log"))
				t.Fatalf("child: %v\n%s", err, b)
			}
			receipt, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("independent parent observed pause=%s elapsed=%s receipt=%s", tc.pause, time.Since(observedStart), receipt)
		})
	}
}

func TestAuthorityTimeNativePauseChild(t *testing.T) {
	stage := os.Getenv("LANTERN_TIME_CHILD_STAGE")
	if stage == "" {
		t.Skip("owned child only")
	}
	sample, err := newDarwinAuthorityTimeSampler()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	wrapped := func() (authorityTimeStamp, error) {
		s, err := sample()
		count++
		if err == nil && ((stage == "before-send" && count == 1) || (stage == "after-receive" && count == 2)) {
			if err = unix.Kill(os.Getpid(), unix.SIGSTOP); err != nil {
				return authorityTimeStamp{}, err
			}
		}
		return s, err
	}
	source, _ := parseAuthorityTimeSource([]byte("server fixture.local"))
	p := &authorityTimeProducer{source: source, sample: wrapped}
	defer p.close()
	m, err := p.exchange(context.Background(), os.Getenv("LANTERN_TIME_CHILD_ADDRESS"))
	if err != nil {
		t.Fatal(err)
	}
	o := &authorityTimeOwner{producer: p, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription))}
	err = o.installLocked(m)
	var current authorityCurrentTime
	switch stage {
	case "before-send":
		if err == nil {
			t.Fatal("overlong send bracket admitted")
		}
	case "after-receive":
		if err != nil {
			t.Fatal(err)
		}
		current, err = o.current()
		if err != nil {
			t.Fatal(err)
		}
		if current.stamp.nanos-m.received.nanos < 1_000_000_000 {
			t.Fatal("delayed install reset age")
		}
	case "before-use":
		if err != nil {
			t.Fatal(err)
		}
		if err = unix.Kill(os.Getpid(), unix.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		current, err = o.current()
		if err == nil {
			t.Fatal("pause extended holdover")
		}
	default:
		t.Fatal("unknown stage")
	}
	now, sampleErr := sample()
	if sampleErr != nil {
		t.Fatal(sampleErr)
	}
	receipt := struct {
		Stage                   string
		Send, Receive, Observed uint64
		Refused                 bool
		Sequence                uint64
	}{stage, m.sent.nanos, m.received.nanos, now.nanos, err != nil, m.sequence}
	b, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(os.Getenv("LANTERN_TIME_CHILD_DIR"), "receipt.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
