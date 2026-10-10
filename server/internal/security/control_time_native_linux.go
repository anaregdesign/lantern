package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

type linuxAuthorityTimeIdentity struct {
	boot      [32]byte
	namespace string
}

// The process must remain in one zero-offset time namespace. No namespace,
// kernel clock, boot setting, service or privilege is changed by this sampler.
func parseLinuxAuthorityTimeIdentity(boot []byte, namespace string, offsets []byte) (linuxAuthorityTimeIdentity, error) {
	if len(boot) != 37 || boot[36] != '\n' || len(namespace) < 8 || len(namespace) > 64 || !strings.HasPrefix(namespace, "time:[") || !strings.HasSuffix(namespace, "]") {
		return linuxAuthorityTimeIdentity{}, errAuthorityTime
	}
	for _, i := range []int{8, 13, 18, 23} {
		if boot[i] != '-' {
			return linuxAuthorityTimeIdentity{}, errAuthorityTime
		}
	}
	compact := strings.ReplaceAll(string(boot[:36]), "-", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 || string(boot[:36]) != strings.ToLower(string(boot[:36])) || [16]byte(decoded) == [16]byte{} {
		return linuxAuthorityTimeIdentity{}, errAuthorityTime
	}
	inode, err := strconv.ParseUint(namespace[6:len(namespace)-1], 10, 64)
	if err != nil || inode == 0 {
		return linuxAuthorityTimeIdentity{}, errAuthorityTime
	}
	fields := strings.Fields(string(offsets))
	if len(fields) != 6 || fields[0] != "monotonic" || fields[1] != "0" || fields[2] != "0" || fields[3] != "boottime" || fields[4] != "0" || fields[5] != "0" {
		return linuxAuthorityTimeIdentity{}, errAuthorityTime
	}
	return linuxAuthorityTimeIdentity{sha256.Sum256(boot), namespace}, nil
}

func readLinuxAuthorityTimeIdentity() (linuxAuthorityTimeIdentity, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	boot, err := readAuthorityTimeFile("/proc/sys/kernel/random/boot_id", 64)
	if err != nil {
		return linuxAuthorityTimeIdentity{}, err
	}
	namespace, err := os.Readlink("/proc/thread-self/ns/time")
	if err != nil {
		return linuxAuthorityTimeIdentity{}, err
	}
	// timens_offsets is exposed for the process leader, and describes its
	// time_for_children namespace. Require all of these identities to agree;
	// reading only the leader's offsets could otherwise qualify another clock.
	for _, path := range []string{"/proc/self/ns/time", "/proc/self/ns/time_for_children", "/proc/thread-self/ns/time_for_children"} {
		other, err := os.Readlink(path)
		if err != nil {
			return linuxAuthorityTimeIdentity{}, err
		}
		if other != namespace {
			return linuxAuthorityTimeIdentity{}, errAuthorityTime
		}
	}
	offsets, err := readAuthorityTimeFile("/proc/self/timens_offsets", 128)
	if err != nil {
		return linuxAuthorityTimeIdentity{}, err
	}
	return parseLinuxAuthorityTimeIdentity(boot, namespace, offsets)
}

type linuxAuthorityTimeState struct {
	identity linuxAuthorityTimeIdentity
	process  [16]byte
	previous uint64
	failed   bool
}

// A private deterministic boundary for testing invalid observations. Production
// construction below always reads the actual procfs/kernel APIs; no caller can
// inject a clock or qualification flag into the current-authority constructor.
func (s *linuxAuthorityTimeState) observe(identity linuxAuthorityTimeIdentity, now unix.Timespec, readErr error) (authorityTimeStamp, error) {
	if s.failed || readErr != nil || identity != s.identity || identity.boot == [32]byte{} || s.process == [16]byte{} || now.Sec < 0 || now.Nsec < 0 || now.Nsec >= 1_000_000_000 || uint64(now.Sec) > (math.MaxUint64-uint64(now.Nsec))/1_000_000_000 {
		s.failed = true
		return authorityTimeStamp{}, errAuthorityTime
	}
	n := uint64(now.Sec)*1_000_000_000 + uint64(now.Nsec)
	if n < s.previous {
		s.failed = true
		return authorityTimeStamp{}, errAuthorityTime
	}
	s.previous = n
	return authorityTimeStamp{identity.boot, s.process, n}, nil
}

func newNativeAuthorityTimeSampler() (func() (authorityTimeStamp, error), error) {
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		return nil, errAuthorityTime
	}
	identity, err := readLinuxAuthorityTimeIdentity()
	if err != nil {
		return nil, err
	}
	var process [16]byte
	if _, err = rand.Read(process[:]); err != nil {
		return nil, err
	}
	if process == [16]byte{} {
		return nil, errAuthorityTime
	}
	state := linuxAuthorityTimeState{identity: identity, process: process}
	var mu sync.Mutex
	return func() (authorityTimeStamp, error) {
		mu.Lock()
		defer mu.Unlock()
		if state.failed {
			return authorityTimeStamp{}, errAuthorityTime
		}
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		current, err := readLinuxAuthorityTimeIdentity()
		var now unix.Timespec
		if err == nil {
			err = unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &now)
		}
		return state.observe(current, now, err)
	}, nil
}
