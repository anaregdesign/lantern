package client

import (
	"net/http"
)

// defaultH2CClient returns an http.Client configured to speak HTTP/2 over
// plaintext (h2c). It is the out-of-the-box transport for
// NewLanternConnect when WithHTTPClient is not supplied so the SDK works
// against the server's primary `:6380` Connect listener (h2c; see
// server/provider/lantern_listener.go) without any TLS setup.
//
// Production deployments should pass an http.Client backed by a proper
// HTTP/2 TLS transport instead — h2c is for development and
// in-cluster traffic, never for the public internet.
//
// The returned *http.Client is safe to share across goroutines; the
// underlying http.Transport pools connections internally.
func defaultH2CClient() *http.Client {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Client{
		Transport: &http.Transport{Protocols: protocols},
	}
}
