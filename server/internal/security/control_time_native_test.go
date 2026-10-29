//go:build darwin || linux

package security

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAuthorityTimeNativeFileRefusesMissingAndOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configured-time-input")
	if _, err := readAuthorityTimeFile(path, 4096); err == nil {
		t.Fatal("missing native input admitted")
	}
	for _, size := range []int{0, 4096, 4097} {
		if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
		raw, err := readAuthorityTimeFile(path, 4096)
		if size == 4096 {
			if err != nil || len(raw) != size {
				t.Fatal("bounded input rejected", err)
			}
		} else if err == nil {
			t.Fatal("empty/oversized native input admitted")
		}
	}
}

func TestAuthorityTimeNativeFactoryRefusal(t *testing.T) {
	for _, mode := range []string{"missing file", "empty", "multiple sources", "source options", "sampler failure", "wrong host"} {
		t.Run(mode, func(t *testing.T) {
			var calls []string
			var producer *authorityTimeProducer
			cause := errors.New("native factory failure")
			read := func(path string, limit int64) ([]byte, error) {
				calls = append(calls, "read")
				if path != "/etc/ntp.conf" || limit != 4096 {
					t.Fatal("native configuration boundary changed")
				}
				switch mode {
				case "missing file":
					return nil, cause
				case "empty":
					return nil, nil
				case "multiple sources":
					return []byte("server time.asia.apple.com\nserver other.invalid\n"), nil
				case "source options":
					return []byte("server time.asia.apple.com iburst\n"), nil
				case "wrong host":
					return []byte("server other.invalid\n"), nil
				default:
					return []byte("server time.asia.apple.com\n"), nil
				}
			}
			owner, err := newNativeAuthorityTimeOwnerFrom(func() (*authorityTimeProducer, error) {
				var err error
				producer, err = newNativeAuthorityTimeProducerFrom(read, func() (func() (authorityTimeStamp, error), error) {
					calls = append(calls, "sampler")
					if mode == "sampler failure" {
						return nil, cause
					}
					return func() (authorityTimeStamp, error) {
						t.Fatal("refused factory sampled or started acquisition")
						return authorityTimeStamp{}, errAuthorityTime
					}, nil
				})
				if mode == "wrong host" && producer != nil {
					// Even a regression in the host guard must not send public
					// traffic from this negative composition test.
					producer.busy = true
				}
				return producer, err
			})
			if owner != nil {
				owner.close()
				t.Fatal("invalid native composition returned an owner")
			}
			if err == nil {
				t.Fatal("invalid native composition admitted")
			}
			want := []string{"read"}
			if mode == "sampler failure" || mode == "wrong host" {
				want = append(want, "sampler")
			}
			if !slices.Equal(calls, want) {
				t.Fatal("native construction order changed", calls)
			}
			if (mode == "missing file" || mode == "sampler failure") && !errors.Is(err, cause) {
				t.Fatal("native dependency error identity lost", err)
			}
			if mode == "wrong host" && (producer == nil || !producer.closed || producer.attempts != 0 || producer.writes != 0) {
				t.Fatal("unapproved source was not closed before acquisition")
			}
		})
	}
}

func TestAuthorityTimeNativeProducerComposition(t *testing.T) {
	config := []byte("# readonly native input\nserver time.asia.apple.com\n")
	var calls []string
	samples := 0
	producer, err := newNativeAuthorityTimeProducerFrom(func(path string, limit int64) ([]byte, error) {
		calls = append(calls, "read")
		if path != "/etc/ntp.conf" || limit != 4096 {
			t.Fatal("native configuration boundary changed")
		}
		return config, nil
	}, func() (func() (authorityTimeStamp, error), error) {
		calls = append(calls, "sampler")
		return func() (authorityTimeStamp, error) {
			samples++
			return fakeAuthorityTimeStamp(123), nil
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer producer.close()
	if !slices.Equal(calls, []string{"read", "sampler"}) || samples != 0 || producer.attempts != 0 || producer.writes != 0 || producer.source.host != "time.asia.apple.com" || producer.source.configuration != sha256.Sum256(config) {
		t.Fatal("factory changed source binding, order or started acquisition")
	}
	stamp, err := producer.sample()
	if err != nil || stamp != fakeAuthorityTimeStamp(123) || samples != 1 {
		t.Fatal("factory replaced its native sampler", err)
	}
}
