package security

import (
	"crypto/rand"
	"crypto/sha256"
	"io"
	"math"
	"os"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

// Only this native constructor is available to production composition. The
// existing public runtime and the data-domain HLC remain unchanged.
func newNativeAuthorityTimeOwner() (*authorityTimeOwner, error) {
	if runtime.GOARCH != "arm64" {
		return nil, errAuthorityTime
	}
	producer, err := newNativeAuthorityTimeProducer()
	if err != nil {
		return nil, err
	}
	if producer.source.host != "time.asia.apple.com" {
		producer.close()
		return nil, errAuthorityTime
	}
	return startAuthorityTimeOwner(producer), nil
}

// Darwin CLOCK_MONOTONIC_RAW uses mach_continuous_time and includes sleep.
// That API property is not a rate bound or a resume-health certification.
func newNativeAuthorityTimeProducer() (*authorityTimeProducer, error) {
	f, err := os.Open("/etc/ntp.conf")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	config, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return nil, err
	}
	source, err := parseAuthorityTimeSource(config)
	if err != nil {
		return nil, err
	}
	sample, err := newDarwinAuthorityTimeSampler()
	if err != nil {
		return nil, err
	}
	return &authorityTimeProducer{source: source, sample: sample}, nil
}

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
