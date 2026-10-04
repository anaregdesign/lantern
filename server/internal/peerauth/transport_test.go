package peerauth

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"
)

func TestWorkloadTransportRefusesUnapprovedDestinationsAndPublicCredentials(t *testing.T) {
	m, key, options, _ := membershipFixture(t)
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	client := NewHTTPClient(s, tls.Certificate{}, x509.NewCertPool())
	for _, url := range []string{"http://localhost:6381/test", "https://localhost:6382/test", "https://user@localhost:6381/test", "https://localhost:6381/test?q=x", "https://localhost:6381/test#x"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Do(request); err == nil {
			t.Fatal("unapproved URL accepted", url)
		}
	}
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, m.Members[0].Origin+"/test", nil)
		request.Header.Set(header, "public credential")
		if _, err := client.Do(request); err == nil {
			t.Fatal("public credential forwarded", header)
		}
	}
}
