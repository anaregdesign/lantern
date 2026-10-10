package security

import (
	"crypto/rand"
	"crypto/sha256"
	"math"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

func newNativeAuthorityTimeSampler() (func() (authorityTimeStamp, error), error) {
	if runtime.GOARCH != "arm64" {
		return nil, errAuthorityTime
	}
	return newDarwinAuthorityTimeSampler()
}

// Darwin CLOCK_MONOTONIC_RAW uses mach_continuous_time and includes sleep.
// That API property is not a rate bound or a resume-health certification.
func newDarwinAuthorityTimeSampler() (func() (authorityTimeStamp, error), error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		return nil, errAuthorityTime
	}
	var process [16]byte
	if _, err := rand.Read(process[:]); err != nil {
		return nil, err
	}
	if process == [16]byte{} {
		return nil, errAuthorityTime
	}
	bootID := sha256.Sum256([]byte(boot))
	var mu sync.Mutex
	var previous uint64
	failed := false
	return func() (authorityTimeStamp, error) {
		mu.Lock()
		defer mu.Unlock()
		if failed {
			return authorityTimeStamp{}, errAuthorityTime
		}
		currentBoot, err := unix.Sysctl("kern.bootsessionuuid")
		if err != nil || currentBoot != boot {
			failed = true
			return authorityTimeStamp{}, errAuthorityTime
		}
		var now unix.Timespec
		if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &now); err != nil {
			failed = true
			return authorityTimeStamp{}, err
		}
		if now.Sec < 0 || now.Nsec < 0 || now.Nsec >= 1_000_000_000 || uint64(now.Sec) > (math.MaxUint64-uint64(now.Nsec))/1_000_000_000 {
			failed = true
			return authorityTimeStamp{}, errAuthorityTime
		}
		n := uint64(now.Sec)*1_000_000_000 + uint64(now.Nsec)
		if n < previous {
			failed = true
			return authorityTimeStamp{}, errAuthorityTime
		}
		previous = n
		return authorityTimeStamp{bootID, process, n}, nil
	}, nil
}
