// Package replication: h2c.go provides the shared default http.Client
// used by the Connect-Go replication clients (Pump + AntiEntropy) when
// the caller does not supply one via Config.HTTPClient /
// AntiEntropyConfig.HTTPClient.
//
// The default speaks HTTP/2 over plaintext (h2c) so the cluster-internal
// replication path (Tier-A topology) works without TLS plumbing.
// Operators that need TLS supply their own http.Client backed by an
// http2.Transport with a real *tls.Config.
//
// peerBaseURL prepends the "http://" scheme to a bare "host:port"
// peer address so Connect-Go's generated client constructor accepts
// it. The scheme intentionally cannot be configured at this layer —
// operators that need https:// build their own *http.Client via
// Config.HTTPClient and pass full URLs in Config.Peers (or via the
// PeerSource resolver).
package replication

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
)

func defaultH2CClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			// AllowHTTP=true makes http2.Transport treat
			// DialTLSContext as the plain TCP dialer (no TLS
			// handshake). The tls.Config arg is honored by the
			// transport but ignored by us.
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
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
