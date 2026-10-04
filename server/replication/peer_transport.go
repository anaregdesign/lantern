package replication

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// PeerTransport is the shared, credential-bearing transport for an HA
// deployment. Static peers have individually verified HTTPS hostnames; DNS
// discovery dials IPs but verifies every peer against the configured DNS name.
// Neither redirects nor a plaintext fallback are permitted.
type PeerTransport struct {
	client         *http.Client
	staticPeers    map[string]struct{}
	approvedOrigin func(string) bool
	dnsName        string
	dnsPort        string
}

// NewVerifiedPeerTransport binds an already-certified workload HTTP client to
// current operator membership. It never attaches a public credential or turns
// an arbitrary resolved address into an eligible peer.
func NewVerifiedPeerTransport(client *http.Client, approvedOrigin func(string) bool) (*PeerTransport, error) {
	if client == nil || client.Transport == nil || approvedOrigin == nil {
		return nil, errors.New("verified peer transport requires owned client and current membership")
	}
	return &PeerTransport{client: client, approvedOrigin: approvedOrigin}, nil
}

// NewAuthenticatedPeerTransport pins the peer CA, TLS identity and allowed
// origins before any replication client can attach the deployment bearer.
func NewAuthenticatedPeerTransport(
	caPEM []byte,
	clientCertificate *tls.Certificate,
	token string,
	staticPeers []string,
	dnsName, dnsPort string,
) (*PeerTransport, error) {
	if token == "" {
		return nil, errors.New("authenticated peer transport requires a bearer token")
	}
	if (len(staticPeers) == 0) == (dnsName == "") {
		return nil, errors.New("authenticated peer transport requires either static HTTPS peers or a DNS discovery identity")
	}
	transport := &PeerTransport{
		staticPeers: make(map[string]struct{}, len(staticPeers)),
		dnsName:     dnsName,
		dnsPort:     dnsPort,
	}
	if dnsName != "" {
		if err := validatePeerDNSName(dnsName); err != nil {
			return nil, fmt.Errorf("peer DNS discovery identity: %w", err)
		}
		if err := validatePeerPort(dnsPort); err != nil {
			return nil, fmt.Errorf("peer DNS discovery port: %w", err)
		}
	} else {
		for _, peer := range staticPeers {
			if _, err := parseStaticPeer(peer); err != nil {
				return nil, fmt.Errorf("invalid static peer: %w", err)
			}
			transport.staticPeers[peer] = struct{}{}
		}
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("peer CA file contains no valid PEM certificates")
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: dnsName,
		NextProtos: []string{"h2", "http/1.1"},
	}
	if clientCertificate != nil {
		tlsConfig.Certificates = []tls.Certificate{*clientCertificate}
	}
	transport.client = &http.Client{
		Transport: &authTransport{
			base: &http.Transport{
				TLSClientConfig:   tlsConfig,
				ForceAttemptHTTP2: true,
				Proxy:             nil,
			},
			token:     token,
			authorize: transport.authorizeURL,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("replication peer redirect rejected")
		},
	}
	return transport, nil
}

func validatePeerDNSName(name string) error {
	if len(name) > 253 || name == "" || net.ParseIP(name) != nil {
		return errors.New("expected a nonempty DNS hostname, not an IP address")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid DNS hostname label")
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') &&
				(ch < '0' || ch > '9') && ch != '-' {
				return errors.New("invalid DNS hostname character")
			}
		}
	}
	return nil
}

func validatePeerPort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("port must be a decimal integer from 1 to 65535")
	}
	return nil
}

func parseStaticPeer(peer string) (*url.URL, error) {
	if !strings.HasPrefix(peer, "https://") {
		return nil, errors.New("bearer-bearing peers must use explicit https:// URLs")
	}
	u, err := url.Parse(peer)
	if err != nil || u == nil || u.Scheme != "https" || u.Host == "" ||
		u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" ||
		u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return nil, errors.New("expected an HTTPS host:port without credentials, path, query or fragment")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" {
		return nil, errors.New("expected an HTTPS host:port")
	}
	if err := validatePeerPort(port); err != nil {
		return nil, err
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return nil, errors.New("scoped IP addresses are not valid peer identities")
		}
	} else if err := validatePeerDNSName(host); err != nil {
		return nil, err
	}
	return u, nil
}

// BaseURL admits only explicitly configured static HTTPS origins or IPs on
// the configured discovery port. The DNS case still requires the peer's TLS
// certificate to match dnsName, never the untrusted resolved IP as a name.
func (t *PeerTransport) BaseURL(addr string) (string, error) {
	if t.approvedOrigin != nil {
		if _, err := parseStaticPeer(addr); err != nil || !t.approvedOrigin(addr) {
			return "", errors.New("unapproved workload peer origin")
		}
		return addr, nil
	}
	if t.dnsName == "" {
		if _, ok := t.staticPeers[addr]; !ok {
			return "", errors.New("unapproved static peer origin")
		}
		return addr, nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port != t.dnsPort {
		return "", errors.New("invalid DNS peer address")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return "", errors.New("DNS peer address must be an IP literal")
	}
	return "https://" + net.JoinHostPort(ip.String(), port), nil
}

func (t *PeerTransport) authorizeURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Host == "" {
		return false
	}
	if t.dnsName != "" {
		_, err := t.BaseURL(u.Host)
		return err == nil
	}
	for peer := range t.staticPeers {
		if strings.TrimPrefix(peer, "https://") == u.Host {
			return true
		}
	}
	return false
}

// authTransport refuses any unapproved or plaintext destination before it
// clones the request and adds the bearer header.
type authTransport struct {
	base      http.RoundTripper
	token     string
	authorize func(*url.URL) bool
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.authorize(req.URL) {
		return nil, errors.New("replication peer request refused: unapproved HTTPS origin")
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}
