package oidc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxDocumentBytes = 1 << 20

var ErrFetch = errors.New("OIDC endpoint unavailable")

// FetcherOptions are operator-owned. A private endpoint exception binds an
// exact HTTPS origin to allowed destination ranges; it is never accepted from
// an Issuer-management request. Default TLS verification remains mandatory.
type FetcherOptions struct {
	PrivateOrigins map[string][]netip.Prefix
	Roots          *x509.CertPool
}

// Fetcher uses a dedicated, proxy-free HTTPS transport. Every new connection
// resolves once, validates all answers, and dials a validated numeric address
// while TLS still authenticates the requested host. Redirects are prohibited.
type Fetcher struct {
	client  *http.Client
	private map[string][]netip.Prefix
	resolve func(context.Context, string) ([]netip.Addr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

func NewFetcher(options FetcherOptions) (*Fetcher, error) {
	if len(options.PrivateOrigins) > 64 {
		return nil, ErrFetch
	}
	f := &Fetcher{private: make(map[string][]netip.Prefix),
		resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}, dial: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext}
	for origin, ranges := range options.PrivateOrigins {
		u, err := endpointURL(origin)
		if err != nil || origin != endpointOrigin(u) || len(ranges) == 0 || len(ranges) > 32 {
			return nil, ErrFetch
		}
		for _, prefix := range ranges {
			if !prefix.IsValid() || prefix != prefix.Masked() || prefix.Bits() == 0 {
				return nil, ErrFetch
			}
		}
		f.private[origin] = append([]netip.Prefix(nil), ranges...)
	}
	var roots *x509.CertPool
	if options.Roots != nil {
		roots = options.Roots.Clone()
	}
	transport := &http.Transport{Proxy: nil, DialContext: f.dialValidated,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout: time.Minute, MaxIdleConns: 16, MaxIdleConnsPerHost: 2,
		MaxConnsPerHost: 4, MaxResponseHeaderBytes: 16 << 10, DisableCompression: true}
	f.client = &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrFetch }}
	return f, nil
}

func endpointURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 2048 || strings.Contains(raw, "#") {
		return nil, ErrFetch
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Fragment != "" || u.Opaque != "" || strings.Contains(u.Hostname(), "%") {
		return nil, ErrFetch
	}
	return u, nil
}

func endpointOrigin(u *url.URL) string {
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && port != "443" {
		host += ":" + port
	}
	return "https://" + host
}

var forbiddenNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.Zone() != "" || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return false
	}
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range forbiddenNetworks {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func (f *Fetcher) dialValidated(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || (network != "tcp" && network != "tcp4" && network != "tcp6") {
		return nil, ErrFetch
	}
	originHost := host
	if strings.Contains(host, ":") {
		originHost = "[" + host + "]"
	}
	origin := "https://" + originHost
	if port != "443" {
		origin += ":" + port
	}
	ranges := f.private[origin]
	addresses, err := f.resolve(ctx, host)
	if err != nil || len(addresses) == 0 || len(addresses) > 16 {
		return nil, ErrFetch
	}
	for _, resolved := range addresses {
		if resolved.Zone() != "" {
			return nil, ErrFetch
		}
		allowed := publicAddress(resolved)
		for _, prefix := range ranges {
			allowed = allowed || prefix.Contains(resolved.Unmap())
		}
		if !allowed {
			return nil, ErrFetch
		}
	}
	for _, resolved := range addresses {
		conn, err := f.dial(ctx, network, net.JoinHostPort(resolved.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, ErrFetch
}

func (f *Fetcher) GetJSON(ctx context.Context, endpoint string, target any) error {
	if _, err := endpointURL(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ErrFetch
	}
	req.Header.Set("Accept", "application/json")
	response, err := f.client.Do(req)
	if err != nil {
		return ErrFetch
	}
	defer response.Body.Close()
	contentType, _, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || response.ContentLength > maxDocumentBytes ||
		response.Header.Get("Content-Encoding") != "" || typeErr != nil ||
		(contentType != "application/json" && contentType != "application/jwk-set+json") {
		return ErrFetch
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDocumentBytes+1))
	if err != nil || len(body) > maxDocumentBytes {
		return ErrFetch
	}
	return decodeJSON(body, target)
}

func (f *Fetcher) CloseIdleConnections() { f.client.CloseIdleConnections() }
