package client

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	"golang.org/x/net/http2"
)

// defaultH2CClient returns an http.Client configured to speak HTTP/2 over
// plaintext (h2c). It is the out-of-the-box transport for
// NewLanternConnect when WithHTTPClient is not supplied so the SDK works
// against the server's primary `:6380` Connect listener (h2c; see
// server/provider/lantern_listener.go) without any TLS setup.
//
// Production deployments should pass an http.Client backed by a proper
// http2.Transport with TLS instead — h2c is for development and
// in-cluster traffic, never for the public internet.
//
// The returned *http.Client is safe to share across goroutines; the
// underlying http2.Transport pools connections internally.
func defaultH2CClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			// AllowHTTP=true makes http2.Transport treat DialTLSContext as
			// the plain TCP dialer (no TLS handshake). The tls.Config arg
			// is honored by the transport but ignored by us.
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
}
