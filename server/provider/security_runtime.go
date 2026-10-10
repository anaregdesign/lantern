package provider

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
)

// SecurityRuntime owns native control state and authentication evidence beside
// one exact data runtime. The production listener requires certification of
// its role/query/CDC/peer/browser boundaries before accepting requests.
type SecurityRuntime struct {
	mode           string
	current        *security.CurrentAuthority
	output         *security.CurrentOutput
	attemptProcess [32]byte
	native         *security.NativeStore
	authority      *security.LeaseAuthority
	receiver       *security.LeaseReceiver
	peer           *PeerIdentityRuntime
	fetcher        *oidc.Fetcher
	secrets        *oidc.SecretRegistry
	keys           *oidc.KeyCache
	verifier       *oidc.Verifier
	logins         *oidc.LoginTransactions
	authorizations *security.ManagementAuthorizations
	control        *service.SecurityConnectHandler
	data           *service.ServingRuntime
	config         SecurityConfig
	now            func() time.Time
	closeOnce      sync.Once
	closeErr       error
	closeOrderly   bool
}

type securityRuntimeClock struct{ now func() time.Time }

func (c securityRuntimeClock) Now() time.Time { return c.now() }

func NewSecurityRuntime(config SecurityConfig, data *service.ServingRuntime) (result *SecurityRuntime, cleanup func(), err error) {
	if err := validateSecurityConfig(config); err != nil {
		return nil, nil, err
	}
	if config.Mode == "oidc" && config.MachineBootstrapFile != "" && len(config.Bootstrap.Machines) == 0 {
		config.Bootstrap.Machines, err = loadSecurityMachines(config.MachineBootstrapFile)
		if err != nil {
			return nil, nil, err
		}
		if err = config.Bootstrap.Validate(); err != nil {
			return nil, nil, err
		}
	}
	runtime := &SecurityRuntime{mode: config.Mode, data: data, config: config, now: config.Clock}
	runtime.config.TrustedProxyIPs = slices.Clone(config.TrustedProxyIPs)
	if runtime.now == nil {
		runtime.now = time.Now
	}
	cleanup = func() { _ = runtime.Close() }
	defer func() {
		if result == nil {
			err = errors.Join(err, runtime.Close())
		}
	}()
	if config.Mode == "off" {
		runtime.control, err = service.NewSecurityConnectHandler(service.SecurityServiceOptions{Ready: func(ctx context.Context, _ *security.Revision) bool { return runtime.Ready(ctx) }})
		if err != nil {
			return nil, nil, err
		}
		return runtime, cleanup, err
	}
	if data == nil || data.DataNamespaceFormat() != keyspace.Version {
		return nil, nil, errors.New("OIDC requires the certified data namespace boundary")
	}
	if config.Profile == "current-v2" {
		if err := runtime.openCurrent(); err != nil {
			return nil, nil, err
		}
		return runtime, cleanup, nil
	}
	publicKey, err := loadSecurityWriterPublicKey(config.WriterPublicKeyFile)
	if err != nil {
		return nil, nil, err
	}
	options := security.NativeStoreOptions{Path: config.StorePath, Generation: config.Generation, PublicKey: publicKey, Limits: security.DefaultPolicyLimits(), Graph: data.GraphCache(), MaxJournalBytes: config.MaxJournalBytes}
	if config.NodeRole == "writer" {
		options.PrivateKey, err = loadSecurityWriterPrivateKey(config.WriterKeyFile)
		if err != nil {
			return nil, nil, err
		}
	}
	roots, err := loadSecurityRoots(config.RootCAFile)
	if err != nil {
		return nil, nil, err
	}
	runtime.fetcher, err = oidc.NewFetcher(oidc.FetcherOptions{PrivateOrigins: config.PrivateOrigins, Roots: roots})
	if err != nil {
		return nil, nil, err
	}
	runtime.secrets, err = oidc.NewSecretRegistry(config.SecretBindings)
	if err != nil {
		return nil, nil, err
	}
	runtime.keys = oidc.NewKeyCacheWithClock(runtime.fetcher, runtime.now)
	runtime.verifier = oidc.NewVerifierWithClock(runtime.keys, runtime.now)
	runtime.logins, err = oidc.NewLoginTransactionsWithClock(config.BrowserOrigin, []string{"/", "/vertices", "/edges", "/search", "/security/roles", "/security/users", "/security/issuers", "/server", "/replication"}, runtime.now)
	if err != nil {
		return nil, nil, err
	}
	runtime.authorizations = security.NewManagementAuthorizations(oidc.LoginTransactionLifetime, oidc.MaxLoginTransactions)
	if config.StoreMode == "fresh" {
		runtime.native, err = security.CreateNativeStore(options)
	} else {
		runtime.native, err = security.ResumeNativeStore(options)
	}
	if err != nil {
		return nil, nil, err
	}
	if config.NodeRole == "writer" {
		// Bootstrap is operator-declared trust. Online metadata validation occurs
		// for login/explicit management activation; restart does not depend on IdP
		// availability merely to recover policy/session state.
		if _, err = runtime.native.Store().ApplyBootstrap(context.Background(), config.Bootstrap); err != nil {
			return nil, nil, err
		}
		runtime.authority, err = security.NewLeaseAuthority(runtime.native.Store(), security.LeaseAuthorityOptions{Clock: securityRuntimeClock{runtime.now}})
		if err != nil {
			return nil, nil, err
		}
	}
	// Replica receiver identity is installed by approved workload composition,
	// never inferred from the graph's ephemeral origin. Until then it cannot serve.
	runtime.control, err = service.NewSecurityConnectHandler(service.SecurityServiceOptions{Store: runtime.native.Store(), Now: runtime.now, ValidateIssuer: func(ctx context.Context, issuer security.Issuer) error {
		if issuer.RedirectURI != config.BrowserOrigin+oidc.CallbackPath(issuer.URL) {
			return oidc.ErrInvalidDocument
		}
		return runtime.fetcher.ValidateIssuer(ctx, issuer, runtime.secrets)
	}, Ready: func(ctx context.Context, revision *security.Revision) bool {
		return runtime.authorityCheck(ctx, revision) == nil
	}, Enforced: func(result security.ChangeResult) bool {
		return runtime.authority != nil && runtime.authority.Enforced(result)
	}, BeginAuthorization: runtime.beginManagementAuthorization, ReadAuthorization: runtime.readManagementAuthorization, VerifyAuthorization: runtime.authorizations.Verify})
	if err != nil {
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
		closeOne(func() error { r.authorizations.Close(); return nil })
		if r.fetcher != nil {
			closeOne(func() error { r.fetcher.CloseIdleConnections(); return nil })
		}
		if r.native != nil {
			closeOne(r.native.Close)
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
func (r *SecurityRuntime) authorityCheck(ctx context.Context, revision *security.Revision) error {
	if r.current != nil {
		return security.ErrAuthorityUnavailable
	} // Legacy Revision never certifies current.
	if r.peer != nil && r.peer.CheckWorkload(ctx) != nil {
		return security.ErrAuthorityUnavailable
	}
	if r.authority != nil {
		return r.authority.Check(ctx, revision)
	}
	if r.receiver != nil {
		return r.receiver.Check(ctx, revision)
	}
	return security.ErrAuthorityUnavailable
}
