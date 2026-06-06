package client

import "testing"

// TestNewLantern_BaseURLValidation covers the constructor's argument-
// shape guards: empty baseURL, missing scheme, bare host:port. The SDK
// must fail loudly rather than producing a *Lantern that 404s on every
// call.
//
// Happy-path round-trip coverage lives in tests/integration (against
// the real server/service handler) — keeping a fake-handler smoke
// suite here would just duplicate that without exercising any code
// path tests/integration can't reach.
func TestNewLantern_BaseURLValidation(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
	}{
		{name: "empty", baseURL: ""},
		{name: "missing scheme", baseURL: "lantern:6381"},
		{name: "bare host:port", baseURL: "localhost:6380"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLantern(tc.baseURL); err == nil {
				t.Fatalf("NewLantern(%q): want error, got nil", tc.baseURL)
			}
		})
	}
}
