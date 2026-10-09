//go:build linux && (amd64 || arm64)

package security

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestAuthorityTimeLinuxIdentity(t *testing.T) {
	boot := []byte("79a4d6d3-f6a7-4815-906e-8cecf4d96c24\n")
	namespace := "time:[4026531834]"
	offsets := []byte("monotonic           0         0\nboottime            0         0\n")
	for _, name := range []string{"valid", "short UUID", "zero UUID", "bad UUID", "extra dash", "uppercase UUID", "wrong namespace", "zero namespace", "bad inode", "oversize namespace", "negative offset", "nonzero offset", "duplicate clock", "extra clock", "missing offsets"} {
		t.Run(name, func(t *testing.T) {
			b, n, o := append([]byte(nil), boot...), namespace, append([]byte(nil), offsets...)
			switch name {
			case "short UUID":
				b = b[:36]
			case "zero UUID":
				b = []byte("00000000-0000-0000-0000-000000000000\n")
			case "bad UUID":
				b[0] = 'z'
			case "extra dash":
				b[0] = '-'
			case "uppercase UUID":
				b[2] = 'A'
			case "wrong namespace":
				n = "pid:[123]"
			case "zero namespace":
				n = "time:[0]"
			case "bad inode":
				n = "time:[-1]"
			case "oversize namespace":
				n = "time:[" + strings.Repeat("1", 65) + "]"
			case "negative offset":
				o = []byte("monotonic -1 0\nboottime 0 0\n")
			case "nonzero offset":
				o = []byte("monotonic 0 0\nboottime 0 1\n")
			case "duplicate clock":
				o = []byte("monotonic 0 0\nmonotonic 0 0\n")
			case "extra clock":
				o = append(o, []byte("realtime 0 0\n")...)
			case "missing offsets":
				o = nil
			}
			identity, err := parseLinuxAuthorityTimeIdentity(b, n, o)
			if name == "valid" {
				if err != nil || identity.boot == [32]byte{} || identity.namespace != namespace {
					t.Fatal(identity, err)
				}
			} else if err == nil {
				t.Fatal("invalid native identity accepted")
			}
		})
	}
}

func TestAuthorityTimeLinuxObservationsFailStop(t *testing.T) {
	identity := linuxAuthorityTimeIdentity{boot: [32]byte{1}, namespace: "time:[2]"}
	for _, name := range []string{"new boot", "new namespace", "read failure", "regression", "negative seconds", "negative nanos", "oversized nanos", "overflow", "zero process"} {
		t.Run(name, func(t *testing.T) {
			state := linuxAuthorityTimeState{identity: identity, process: [16]byte{3}}
			if _, err := state.observe(identity, unix.Timespec{Sec: 1}, nil); err != nil {
				t.Fatal(err)
			}
			next, stamp := identity, unix.Timespec{Sec: 2}
			var readErr error
			switch name {
			case "new boot":
				next.boot[0]++
			case "new namespace":
				next.namespace = "time:[4]"
			case "read failure":
				readErr = errors.New("native read failed")
			case "regression":
				stamp.Sec = 0
			case "negative seconds":
				stamp.Sec = -1
			case "negative nanos":
				stamp.Nsec = -1
			case "oversized nanos":
				stamp.Nsec = 1_000_000_000
			case "overflow":
				stamp.Sec = 1 << 62
			case "zero process":
				state.process = [16]byte{}
			}
			if _, err := state.observe(next, stamp, readErr); err == nil || !state.failed {
				t.Fatal("invalid observation admitted")
			}
			if _, err := state.observe(identity, unix.Timespec{Sec: 3}, nil); err == nil {
				t.Fatal("failed native sampler resurrected")
			}
		})
	}
}

func TestAuthorityTimeLinuxNativeSampler(t *testing.T) {
	sample, err := newNativeAuthorityTimeSampler()
	if err != nil {
		t.Fatal(err)
	}
	a, err := sample()
	if err != nil {
		t.Fatal(err)
	}
	b, err := sample()
	if err != nil {
		t.Fatal(err)
	}
	if a.boot == [32]byte{} || a.process == [16]byte{} || a.boot != b.boot || a.process != b.process || b.nanos < a.nanos {
		t.Fatal("invalid actual kernel samples")
	}
	another, err := newNativeAuthorityTimeSampler()
	if err != nil {
		t.Fatal(err)
	}
	c, err := another()
	if err != nil {
		t.Fatal(err)
	}
	if c.boot != a.boot || c.process == a.process {
		t.Fatal("process epoch was reused")
	}
	t.Logf("Linux native uid=%d boot=%x process=%x new_process=%x counter=%d; no host suspend/reboot claim", os.Getuid(), a.boot, a.process, c.process, c.nanos)
}

func TestAuthorityTimeLinuxConfiguredUpstream(t *testing.T) {
	if os.Getenv("LANTERN_TEST_CONFIGURED_TIME_UPSTREAM") != "1" {
		t.Skip("explicit bounded native campaign only")
	}
	o, err := newNativeAuthorityTimeOwner()
	if err != nil {
		t.Fatal(err)
	}
	defer o.close()
	deadline := time.Now().Add(45 * time.Second)
	for {
		if now, err := o.current(); err == nil {
			o.mu.Lock()
			m := *o.lastObservation
			o.mu.Unlock()
			t.Logf("Linux configured profile=%x host=%s configuration=%x endpoint=%s sequence=%d boot=%x process=%x sent=%d received=%d current=%d UTC=[%d,%d] request=%x response=%x; conditional source/path/rate premises", now.profile, m.source.host, m.source.configuration, m.endpoint, m.sequence, now.stamp.boot, now.stamp.process, m.sent.nanos, m.received.nanos, now.stamp.nanos, now.utc.low, now.utc.high, m.request, m.response)
			return
		}
		if time.Now().After(deadline) {
			o.mu.Lock()
			cause := o.lastError
			o.mu.Unlock()
			t.Fatal("bounded Linux source startup unavailable", cause)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The opt-in test adds a restrictive seccomp filter only to its own locked
// OS thread. Existing Docker restrictions remain in force. No procfs mount,
// host setting, added capability or production injection is involved.
func TestAuthorityTimeLinuxUnavailableNativeIdentity(t *testing.T) {
	if os.Getenv("LANTERN_TEST_NATIVE_IDENTITY_UNAVAILABLE") != "1" {
		t.Skip("explicit native failure campaign only")
	}
	runtime.LockOSThread()
	// Do not unlock: exiting this test goroutine retires its restricted thread.
	sample, err := newNativeAuthorityTimeSampler()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sample(); err != nil {
		t.Fatal(err)
	}
	arch := uint32(unix.AUDIT_ARCH_AARCH64)
	if runtime.GOARCH == "amd64" {
		arch = unix.AUDIT_ARCH_X86_64
	}
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4}, // seccomp_data.arch
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: arch, Jt: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, // syscall number
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.SYS_OPENAT, Jf: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err = unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0); err != nil {
		t.Fatal("unprivileged restrictive filter unavailable", err)
	}
	runtime.KeepAlive(filter)
	if _, err = readLinuxAuthorityTimeIdentity(); !errors.Is(err, unix.EPERM) {
		t.Fatal("actual identity read was not denied", err)
	}
	for range 2 {
		if _, err = sample(); err == nil {
			t.Fatal("failed native sampler admitted use")
		}
	}
	if _, err = newNativeAuthorityTimeSampler(); err == nil {
		t.Fatal("unavailable identity admitted by native factory")
	}
	if _, err = newNativeAuthorityTimeOwner(); err == nil {
		t.Fatal("unavailable native input admitted by production factory")
	}
	t.Log("actual procfs read denied with EPERM; native factory and existing sampler refused; no new capability")
}
