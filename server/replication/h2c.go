// Package replication supports bearer-free h2c peers and verified HTTPS
// peers with a separate, credential-bearing PeerTransport.
package replication

import (
	"net/http"
	"strings"
)

func defaultH2CClient() *http.Client {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Client{
		Transport: &http.Transport{Protocols: protocols},
	}
}

// peerBaseURL accepts the historical "host:port" peer address form
// and the new "http://host:port" / "https://host:port" forms.
// Returns the input unchanged when it already carries a scheme; adds
// "http://" otherwise. Trailing slashes are trimmed so the
// Connect-Go client's path concatenation produces clean URLs.
func peerBaseURL(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}

func peerURL(addr string, transport *PeerTransport) (string, error) {
	if transport != nil {
		return transport.BaseURL(addr)
	}
	return peerBaseURL(addr), nil
}
