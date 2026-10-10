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

func newNativeAuthorityTimeProducer() (*authorityTimeProducer, error) {
	config, err := readAuthorityTimeFile("/etc/ntp.conf", 4096)
	if err != nil {
		return nil, err
	}
	source, err := parseAuthorityTimeSource(config)
	if err != nil {
		return nil, err
	}
	sample, err := newNativeAuthorityTimeSampler()
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
