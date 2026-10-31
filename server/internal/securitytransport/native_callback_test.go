package securitytransport

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNativeAppleCallbackStrictFormBoundary(t *testing.T) {
	form := url.Values{"code": {"code"}, "id_token": {"provider-token"}, "state": {"state"}, "user": {`{"email":"not-retained@example.test"}`}}
	request := func(body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "https://lantern.example"+nativeAppleCallbackPrefix+"mobile", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}
	profile, input, err := decodeNativeAppleCallback(request(form.Encode()))
	if err != nil || profile != "mobile" || input.Provider != "apple" || input.AuthorizationCode != "code" || input.IDToken != "provider-token" {
		t.Fatal("form decode", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*http.Request)
	}{
		{"query/body mixup", func(r *http.Request) { r.URL.RawQuery = "state=other" }},
		{"GET", func(r *http.Request) { r.Method = http.MethodGet }},
		{"media", func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }},
		{"compression", func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }},
		{"bearer substitution", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }},
		{"encoded path", func(r *http.Request) { r.URL.RawPath = "/auth/native/apple/callback/%6dobile" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request(form.Encode())
			tc.edit(req)
			if _, _, err := decodeNativeAppleCallback(req); err == nil {
				t.Fatal("ambiguous callback accepted")
			}
		})
	}
	for _, body := range []string{form.Encode() + "&state=duplicate", form.Encode() + "&redirect_uri=https%3A%2F%2Fevil.test", strings.Repeat("x", NativeAuthMaxBytes+1), "code=c&state=s", "code=c&id_token=t&state=%ZZ"} {
		if _, _, err := decodeNativeAppleCallback(request(body)); err == nil {
			t.Fatal("invalid callback body accepted")
		}
	}
}
