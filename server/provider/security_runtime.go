package provider

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/readinessdiag"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
)

// SecurityRuntime owns native control state and authentication evidence beside
// one exact data runtime. The production listener requires certification of
// its role/query/CDC/peer/browser boundaries before accepting requests.
type SecurityRuntime struct {
	mode           string
	authentication *service.AuthenticationAdapter
	workflow       *authn.Workflow
	credentials    *authn.Credentials
	current        *security.CurrentAuthority
	output         *security.CurrentOutput
	attemptProcess [32]byte
	peer           *PeerIdentityRuntime
	fetcher        *oidc.Fetcher
	secrets        *oidc.SecretRegistry
	keys           *oidc.KeyCache
	verifier       *oidc.Verifier
	logins         *oidc.LoginTransactions
	nativeLogins   *oidc.NativeTransactions
	control        *service.SecurityConnectHandler
	data           *service.ServingRuntime
	config         SecurityConfig
	now            func() time.Time
	closeOnce      sync.Once
	closeErr       error
	closeOrderly   bool

	readinessObservation *readinessdiag.Observer
}

func NewSecurityRuntime(config SecurityConfig, data *service.ServingRuntime) (result *SecurityRuntime, cleanup func(), err error) {
	if err := validateSecurityConfig(config); err != nil {
		return nil, nil, err
	}
	runtime := &SecurityRuntime{mode: config.Mode, data: data, config: config, now: time.Now}
	runtime.config.TrustedProxyIPs = slices.Clone(config.TrustedProxyIPs)
	cleanup = func() { _ = runtime.Close() }
	defer func() {
		if result == nil {
			err = errors.Join(err, runtime.Close())
		}
	}()
	if config.Mode == "off" {
		runtime.composeAuthentication()
		runtime.control, err = service.NewSecurityConnectHandler(service.SecurityServiceOptions{Ready: runtime.Ready})
		if err != nil {
			return nil, nil, err
		}
		return runtime, cleanup, err
	}
	if data == nil || data.DataNamespaceFormat() != keyspace.Version {
		return nil, nil, errors.New("OIDC requires the certified data namespace boundary")
	}
	if err := runtime.openAuthority(); err != nil {
		return nil, nil, err
	}
	return runtime, cleanup, nil
}

// Close is abort cleanup. A partially constructed or failed runtime must never
// create an orderly current-authority checkpoint. Wire may call it repeatedly.
func (r *SecurityRuntime) Close() error { return r.finish(false) }

// Shutdown is the error-returning normal lifecycle used after App has joined
// all public/private workers. Current sys custody and data durability remain
// separate owners; App must observe both terminal results.
func (r *SecurityRuntime) Shutdown() error { return r.finish(true) }

func (r *SecurityRuntime) finish(orderly bool) error {
	if r == nil {
		return nil
	}
	var failure any
	r.closeOnce.Do(func() {
		closeOne := func(close func() error) {
			defer func() {
				if value := recover(); value != nil {
					if failure == nil {
						failure = value
					}
					r.closeErr = errors.Join(r.closeErr, errors.New("security runtime cleanup panicked"))
				}
			}()
			r.closeErr = errors.Join(r.closeErr, close())
		}
		if r.workflow != nil {
			closeOne(func() error { r.workflow.Close(); return nil })
		}
		if r.nativeLogins != nil {
			closeOne(func() error { r.nativeLogins.Close(); return nil })
		}
		if r.fetcher != nil {
			closeOne(func() error { r.fetcher.CloseIdleConnections(); return nil })
		}
		if r.current != nil {
			if orderly && r.closeErr == nil {
				closeOne(r.current.Shutdown)
			} else {
				closeOne(r.current.Close)
			}
		}
		r.closeOrderly = orderly && r.closeErr == nil
	})
	if failure != nil {
		panic(failure)
	}
	if orderly && !r.closeOrderly {
		return errors.Join(r.closeErr, errors.New("security runtime did not complete orderly shutdown"))
	}
	return r.closeErr
}
func (r *SecurityRuntime) Mode() string                                    { return r.mode }
func (r *SecurityRuntime) ControlHandler() *service.SecurityConnectHandler { return r.control }

// TimeAcquisitionCounts is local diagnostic evidence; it does not grant time.
func (r *SecurityRuntime) TimeAcquisitionCounts() decisiontrace.AcquisitionCounts {
	if r == nil || r.current == nil {
		return decisiontrace.AcquisitionCounts{}
	}
	return r.current.TimeAcquisitionCounts()
}
