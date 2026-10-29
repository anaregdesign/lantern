//go:build darwin || linux

package security

import (
	"io"
	"os"
)

// Only the private production composition calls this factory. The configured
// source must be supplied read-only by the deployment; missing configuration
// refuses. Containers do not inherit a host time service or source implicitly.
func newNativeAuthorityTimeOwner() (*authorityTimeOwner, error) {
	return newNativeAuthorityTimeOwnerFrom(newNativeAuthorityTimeProducer)
}

// Explicit private dependencies let tests exercise refusal without consulting
// host configuration or sending public traffic. Normal composition is fixed.
func newNativeAuthorityTimeOwnerFrom(factory func() (*authorityTimeProducer, error)) (*authorityTimeOwner, error) {
	producer, err := factory()
	if err != nil {
		return nil, err
	}
	if producer.source.host != "time.asia.apple.com" {
		producer.close()
		return nil, errAuthorityTime
	}
	return startAuthorityTimeOwner(producer), nil
}

func newNativeAuthorityTimeProducer() (*authorityTimeProducer, error) {
	return newNativeAuthorityTimeProducerFrom(readAuthorityTimeFile, newNativeAuthorityTimeSampler)
}

func newNativeAuthorityTimeProducerFrom(read func(string, int64) ([]byte, error), sampler func() (func() (authorityTimeStamp, error), error)) (*authorityTimeProducer, error) {
	config, err := read("/etc/ntp.conf", 4096)
	if err != nil {
		return nil, err
	}
	source, err := parseAuthorityTimeSource(config)
	if err != nil {
		return nil, err
	}
	sample, err := sampler()
	if err != nil {
		return nil, err
	}
	return &authorityTimeProducer{source: source, sample: sample}, nil
}

func readAuthorityTimeFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return nil, errAuthorityTime
	}
	return raw, nil
}
