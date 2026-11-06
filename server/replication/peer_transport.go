package replication

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// PeerTransport binds a certified workload client to current membership.
// Workload TLS and request admission remain owned by that client.
type PeerTransport struct {
	client         *http.Client
	approvedOrigin func(string) bool
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

func parsePeerOrigin(peer string) (*url.URL, error) {
	if !strings.HasPrefix(peer, "https://") {
		return nil, errors.New("workload peers must use explicit https:// URLs")
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

// BaseURL admits only strict HTTPS origins in current operator membership.
func (t *PeerTransport) BaseURL(addr string) (string, error) {
	if t == nil || t.approvedOrigin == nil {
		return "", errors.New("unapproved workload peer origin")
	}
	if _, err := parsePeerOrigin(addr); err != nil || !t.approvedOrigin(addr) {
		return "", errors.New("unapproved workload peer origin")
	}
	return addr, nil
}
