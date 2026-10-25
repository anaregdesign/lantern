// The Compose metrics relay shares one Lantern node's network namespace. It
// exposes exactly that node's loopback metrics over an independent mTLS domain.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	metricsEndpoint = "http://127.0.0.1:9090/metrics"
	metricsTimeout  = 5 * time.Second
	maxMetricsBytes = 64 << 20
)

var errMetricsRelayConfig = errors.New("metrics relay: invalid private TLS configuration")

type metricsRelayOptions struct {
	listen, certificate, key, clientCA string
}

func parseMetricsRelayOptions(args []string) (metricsRelayOptions, bool, error) {
	var options metricsRelayOptions
	flags := flag.NewFlagSet("metrics-relay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.listen, "listen", ":9443", "private operator-network listener")
	flags.StringVar(&options.certificate, "certificate", "", "metrics server certificate PEM")
	flags.StringVar(&options.key, "key", "", "private metrics server key PEM")
	flags.StringVar(&options.clientCA, "client-ca", "", "independent metrics client CA PEM")
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return options, true, nil
	}
	if err != nil || flags.NArg() != 0 {
		return metricsRelayOptions{}, false, errMetricsRelayConfig
	}
	_, port, err := net.SplitHostPort(options.listen)
	number, numberErr := strconv.Atoi(port)
	if err != nil || numberErr != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
		return metricsRelayOptions{}, false, errMetricsRelayConfig
	}
	for _, path := range []string{options.certificate, options.key, options.clientCA} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return metricsRelayOptions{}, false, errMetricsRelayConfig
		}
	}
	return options, false, nil
}

func readMetricsTLSFile(path string, private bool) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() == 0 || before.Size() > 1<<20 || private && before.Mode().Perm()&0077 != 0 {
		return nil, errMetricsRelayConfig
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errMetricsRelayConfig
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || private && opened.Mode().Perm()&0077 != 0 {
		return nil, errMetricsRelayConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errMetricsRelayConfig
	}
	return raw, nil
}

func metricsTLSConfig(options metricsRelayOptions) (*tls.Config, error) {
	certificate, err := readMetricsTLSFile(options.certificate, false)
	if err != nil {
		return nil, err
	}
	key, err := readMetricsTLSFile(options.key, true)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil || len(pair.Certificate) == 0 {
		return nil, errMetricsRelayConfig
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		return nil, errMetricsRelayConfig
	}
	raw, err := readMetricsTLSFile(options.clientCA, false)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(raw)) != 0 {
		raw = bytes.TrimSpace(raw)
		if !bytes.HasPrefix(raw, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errMetricsRelayConfig
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || count >= 8 {
			return nil, errMetricsRelayConfig
		}
		root, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !root.IsCA || !root.BasicConstraintsValid || root.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, errMetricsRelayConfig
		}
		roots.AddCert(root)
		count++
		raw = rest
	}
	if count == 0 {
		return nil, errMetricsRelayConfig
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs: roots, Certificates: []tls.Certificate{pair},
		SessionTicketsDisabled: true, NextProtos: []string{"http/1.1"},
	}, nil
}

// metricsRelay never accepts an upstream, destination path or credential from
// the request. One in-flight scrape bounds its response-buffer ownership.
type metricsRelay struct {
	client   *http.Client
	maxBytes int64
	inFlight chan struct{}
}

func newMetricsRelay() *metricsRelay {
	transport := &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: metricsTimeout}).DialContext,
		DisableCompression: true, MaxIdleConns: 1, MaxIdleConnsPerHost: 1,
		IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: metricsTimeout,
		MaxResponseHeaderBytes: 32 << 10,
	}
	return &metricsRelay{client: &http.Client{
		Transport: transport, Timeout: metricsTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, maxBytes: maxMetricsBytes, inFlight: make(chan struct{}, 1)}
}

func (h *metricsRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "metrics GET only", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.EscapedPath() != "/metrics" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.IsAbs() || r.URL.Host != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		http.Error(w, "metrics path only", http.StatusNotFound)
		return
	}
	select {
	case h.inFlight <- struct{}{}:
		defer func() { <-h.inFlight }()
	default:
		http.Error(w, "scrape already in progress", http.StatusServiceUnavailable)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, metricsEndpoint, nil)
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusBadGateway)
		return
	}
	request.Header.Set("Accept", "text/plain; version=0.0.4")
	request.Header.Set("User-Agent", "lantern-compose-metrics-relay")
	response, err := h.client.Do(request)
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > h.maxBytes || response.Header.Get("Content-Encoding") != "" {
		http.Error(w, "metrics unavailable", http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, h.maxBytes+1))
	if err != nil || int64(len(body)) > h.maxBytes {
		http.Error(w, "metrics unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func runMetricsRelay(ctx context.Context, args []string, out io.Writer) error {
	options, help, err := parseMetricsRelayOptions(args)
	if err != nil {
		return err
	}
	if help {
		_, err := io.WriteString(out, "Usage: metrics-relay --certificate FILE --key FILE --client-ca FILE [--listen :9443]\nMandatory TLS 1.3 mutual authentication; forwards only GET /metrics to fixed 127.0.0.1:9090.\n")
		return err
	}
	tlsConfig, err := metricsTLSConfig(options)
	if err != nil {
		return err
	}
	handler := newMetricsRelay()
	defer handler.client.CloseIdleConnections()
	server := &http.Server{
		Addr: options.listen, Handler: handler, TLSConfig: tlsConfig,
		ReadHeaderTimeout: metricsTimeout, ReadTimeout: metricsTimeout,
		WriteTimeout: metricsTimeout, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0),
	}
	lifetime, cancelLifetime := context.WithCancel(ctx)
	defer cancelLifetime()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-lifetime.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), metricsTimeout)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
	}()
	err = server.ListenAndServeTLS("", "")
	cancelLifetime()
	<-stopped
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return errors.New("metrics relay: listener stopped")
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if runMetricsRelay(ctx, os.Args[1:], os.Stdout) != nil {
		// Fixed category only: never log certificate/key paths or OS errors.
		_, _ = fmt.Fprintln(os.Stderr, "metrics relay stopped; check private TLS configuration and listener")
		os.Exit(1)
	}
}
