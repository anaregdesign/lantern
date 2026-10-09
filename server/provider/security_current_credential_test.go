package provider

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCurrentBrowserReadProducerOnlyBootstrapsSession(t *testing.T) {
	r := &SecurityRuntime{mode: "oidc", config: SecurityConfig{BrowserOrigin: "https://admin.example"}}
	req := httptest.NewRequest(http.MethodGet, "https://admin.example/auth/session", nil)
	req.TLS = &tls.ConnectionState{}
	cookie := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookie})
	p, err := r.CurrentBrowserReadProducer(req)
	if err != nil || !p.(currentCredentialProducer).browserReadOnly {
		t.Fatal("CSRF bootstrap required the unavailable mutation header", err)
	}
	if _, err = r.CurrentBrowserProducer(req); err == nil {
		t.Fatal("read-only HTTP evidence became mutation proof")
	}
	for _, path := range []string{"/auth/logout", "/browser/graph.v1.LanternService/GetVertex"} {
		other := req.Clone(req.Context())
		other.URL.Path = path
		if _, err = r.CurrentBrowserReadProducer(other); err == nil {
			t.Fatal("read bootstrap escaped its exact endpoint", path)
		}
	}
	req.Method = http.MethodPost
	if _, err = r.CurrentBrowserReadProducer(req); err == nil {
		t.Fatal("mutation method used bootstrap evidence")
	}
	req.Header.Set("Origin", "https://admin.example")
	req.Header.Set(browserCSRFHeader, cookie)
	p, err = r.CurrentBrowserProducer(req)
	if err != nil || p.(currentCredentialProducer).browserReadOnly {
		t.Fatal("exact separate mutation proof refused", err)
	}
}

func TestCurrentCredentialProducerBoundary(t *testing.T) {
	r := &SecurityRuntime{mode: "oidc", keys: oidc.NewKeyCache(nil)}
	for _, values := range [][]string{nil, {"Bearer "}, {"Bearer a b"}, {"Bearer a", "Bearer b"}, {"Basic a"}, {"Bearer " + security.MachineTokenPrefix + "not-human"}} {
		if _, err := r.CurrentBearerProducer(http.Header{"Authorization": values}); err == nil {
			t.Fatal("ambiguous or nonhuman credential accepted")
		}
	}
	producer, err := r.CurrentBearerProducer(http.Header{"Authorization": {"Bearer confidential-placeholder"}})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v", producer) != "[redacted current credential producer]" || fmt.Sprintf("%#v", producer) != "[redacted current credential producer]" {
		t.Fatal("producer revealed credential")
	}
	if _, err := producer.VerifyCurrentCredential(t.Context(), security.CurrentCredentialView{}); err == nil {
		t.Fatal("zero installed S1 view accepted")
	}
	if _, err := r.CurrentBrowserProducer(nil); err == nil {
		t.Fatal("missing native browser request")
	}
	if _, err := (*SecurityRuntime)(nil).CurrentCodeProducer(oidc.LoginCompletion{}, oidc.CodeTokens{}); err == nil {
		t.Fatal("missing native Code producer")
	}
}

func TestCurrentCodeStartWaitPreservesStrictNativeBounds(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	calls := 0
	bounds := func() (time.Time, time.Time, error) {
		calls++
		if calls == 1 {
			return now.Add(-time.Nanosecond), now.Add(time.Millisecond), nil
		}
		return now, now.Add(time.Millisecond), nil
	}
	if err := waitCurrentCodeStart(t.Context(), bounds, now, now.Add(time.Hour)); err != nil || calls != 2 {
		t.Fatal("Code start accepted before lower endpoint or not rechecked", calls, err)
	}
	for _, kind := range []string{"expired", "uncertain", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sampled := 0
			bound := func() (time.Time, time.Time, error) {
				sampled++
				switch kind {
				case "expired":
					return now.Add(-time.Nanosecond), now.Add(time.Hour), nil
				case "uncertain":
					return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
				default:
					cancel()
					return now.Add(-time.Second), now.Add(time.Millisecond), nil
				}
			}
			err := waitCurrentCodeStart(ctx, bound, now, now.Add(time.Hour))
			if err == nil || sampled != 1 {
				t.Fatal("wait invented eligibility", sampled, err)
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
