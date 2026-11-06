// Package provider: lantern_listener.go owns the primary listener's transport
// configuration and shared Connect handler options. The certified public
// composition owns its service, health and reflection mounts.
package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
)

// authenticatedHandlerOptions keeps logging and metrics outside admission,
// then applies the supplied rate/authentication owners before validation.
// Slow RPC observation is innermost.
//
// Nil entries are skipped. Operational owners remain mounted while disabled,
// so future validated settings can enable them without rebuilding handlers.
//
// connect.WithRecover catches panics in handlers and returns
// CodeInternal — the equivalent of the grpc-middleware recovery
// interceptor.
func authenticatedHandlerOptions(netCfg NetConfig, val *ValidationInterceptor, rl *RateLimitInterceptor, authentication connect.Interceptor, log *LoggingInterceptor, met *PrometheusInterceptor, slow *SlowRPCInterceptor, logger *slog.Logger) []connect.HandlerOption {
	var ints []connect.Interceptor
	if log != nil {
		ints = append(ints, log.ConnectInterceptor())
	}
	if met != nil {
		ints = append(ints, met.ConnectInterceptor())
	}
	if rl != nil {
		ints = append(ints, rl.ConnectInterceptor())
	}
	if authentication != nil {
		ints = append(ints, authentication)
	}
	if val != nil {
		ints = append(ints, val.ConnectInterceptor())
	}
	if slow != nil {
		ints = append(ints, slow.ConnectInterceptor())
	}
	compressMinBytes := 1024
	if maxSendBytes := netCfg.MaxSendMsgBytes; maxSendBytes > 0 && maxSendBytes < compressMinBytes {
		// Connect checks the send cap on compressed bytes when gzip is used.
		// Keep responses at or above the cap eligible for compression.
		compressMinBytes = maxSendBytes
	}
	opts := []connect.HandlerOption{
		connect.WithReadMaxBytes(netCfg.MaxRecvMsgBytes),
		connect.WithSendMaxBytes(netCfg.MaxSendMsgBytes),
		connect.WithCompressMinBytes(compressMinBytes),
		connect.WithRecover(func(ctx context.Context, _ connect.Spec, _ http.Header, p any) error {
			logger.ErrorContext(ctx, "connect handler panic", slog.Any("panic", p))
			return connect.NewError(connect.CodeInternal, errors.New("internal server error"))
		}),
	}
	if len(ints) > 0 {
		opts = append(opts, connect.WithInterceptors(ints...))
	}
	return opts
}

// LanternListener is the primary transport with its certified public handler.
// LanternServer.Run drives its lifecycle.
type LanternListener struct {
	server   *http.Server
	listener net.Listener
	tls      bool
}

// Server returns the underlying *http.Server so LanternServer can
// call Serve / ServeTLS / Shutdown on it. Exposed (rather than
// inlining the lifecycle here) so service.LanternServer keeps owning
// the health-flip + cache-GC + graceful-stop orchestration without
// importing the provider package.
func (l *LanternListener) Server() *http.Server { return l.server }

// Listener returns the wire-injected net.Listener bound by NewListener.
// Tests can swap it via wire.Bind(new(net.Listener), new(*bufconn.Listener))
// to drive the server over bufconn without a real port.
func (l *LanternListener) Listener() net.Listener { return l.listener }

// Addr is the listen address (host:port) the underlying listener is bound
// to. Used by LanternServer for the "lantern server starting" slog line so
// operators see a stable field across restarts.
func (l *LanternListener) Addr() string {
	if l.listener == nil {
		return l.server.Addr
	}
	return l.listener.Addr().String()
}

// TLSEnabled reports whether the listener is wired with a
// *tls.Config. LanternServer picks ServeTLS over Serve when true.
func (l *LanternListener) TLSEnabled() bool { return l.tls }

// newLanternListenerTransport configures TLS, protocols and server timeouts
// without selecting any HTTP handlers. Only the certified public composition
// installs the serving mux; tests can install a service-only transport fixture.
func newLanternListenerTransport(
	lis net.Listener,
	netCfg NetConfig,
	tlsCfg TLSConfig,
	logger *slog.Logger,
) (*LanternListener, error) {
	if netCfg.MaxRecvMsgBytes < 0 || netCfg.MaxSendMsgBytes < 0 {
		return nil, errors.New("connect message size limits must be zero (unlimited) or positive")
	}
	addr := lis.Addr().String()
	srv := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 30 * time.Second,
	}
	// Enable HTTP/2 cleartext (h2c) so Connect, gRPC, and gRPC-Web
	// clients can negotiate without TLS. Setting via the Protocols
	// field is the Go 1.24+ recommended path. Calling
	// SetUnencryptedHTTP2 on the zero-value Protocols also implicitly
	// preserves HTTP/1.1 and HTTP/2 (over TLS) so the same listener
	// serves browser fetch() + grpcurl + Connect-Go clients without
	// further tuning.
	var protos http.Protocols
	protos.SetHTTP1(true)
	protos.SetHTTP2(true)
	protos.SetUnencryptedHTTP2(true)
	srv.Protocols = &protos

	tlsConf, err := loadTLSConfig(tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("load tls: %w", err)
	}
	if tlsConf != nil {
		srv.TLSConfig = tlsConf
		logger.Info("tls enabled",
			slog.String("cert", tlsCfg.CertFile),
			slog.Bool("mtls", tlsCfg.ClientCAFile != "" || tlsCfg.ClientCAPEM != ""),
		)
	}

	return &LanternListener{
		server:   srv,
		listener: lis,
		tls:      tlsConf != nil,
	}, nil
}

// loadTLSConfig parses the LANTERN_TLS_* env vars into a *tls.Config
// suitable for http.Server.TLSConfig. Returns (nil, nil) when no
// cert / key are set so callers can serve plain h2c without
// special-casing the disabled path.
//
// When LANTERN_TLS_CLIENT_CA_FILE is set, ClientAuth is bumped to
// RequireAndVerifyClientCert so the server only accepts connections
// presenting a certificate signed by the configured CA — the
// mTLS-equivalent of the legacy
// credentials.NewTLS(...).WithClientCAs(...) path.
func loadTLSConfig(c TLSConfig) (*tls.Config, error) {
	if c.CertPEM != "" || c.KeyPEM != "" || c.ClientCAPEM != "" {
		if c.CertFile != "" || c.KeyFile != "" || c.ClientCAFile != "" || c.CertPEM == "" || c.KeyPEM == "" {
			return nil, errors.New("TLS requires exactly one complete certificate/key source")
		}
		cert, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM))
		if err != nil {
			return nil, errors.New("invalid security JSON TLS certificate/key")
		}
		cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
		if c.ClientCAPEM != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(c.ClientCAPEM)) {
				return nil, errors.New("invalid security JSON TLS client CA")
			}
			cfg.ClientCAs, cfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
		}
		return cfg, nil
	}
	if c.CertFile == "" && c.KeyFile == "" {
		if c.ClientCAFile != "" {
			return nil, errors.New("LANTERN_TLS_CLIENT_CA_FILE set but LANTERN_TLS_CERT_FILE/LANTERN_TLS_KEY_FILE are empty")
		}
		return nil, nil
	}
	if c.CertFile == "" || c.KeyFile == "" {
		return nil, errors.New("LANTERN_TLS_CERT_FILE and LANTERN_TLS_KEY_FILE must both be set")
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls cert/key: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}
	if c.ClientCAFile != "" {
		pool, err := loadClientCAPool(c.ClientCAFile)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
