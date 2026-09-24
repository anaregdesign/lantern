package replication

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultH2CClient(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			http.Error(w, "HTTP/2 required", http.StatusHTTPVersionNotSupported)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = protocols
	srv.Start()
	defer srv.Close()

	resp, err := defaultH2CClient().Get(srv.URL)
	if err != nil {
		t.Fatalf("h2c GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent || resp.ProtoMajor != 2 {
		t.Fatalf("h2c response: status=%d protocol=%s", resp.StatusCode, resp.Proto)
	}
}
