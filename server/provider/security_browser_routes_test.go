package provider

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSecurityBrowserRoutesSessionLogout(t *testing.T) {
	runtime, _, session, csrf := browserSessionFixture(t)
	handler := runtime.AuthHTTPHandler()
	req := browserFixtureRequest(session, csrf)
	req.Method = http.MethodGet
	req.URL.Path = "/auth/session"
	req.Header.Del("Origin")
	req.Header.Del(browserCSRFHeader)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var result pb.BrowserSession
	if w.Code != http.StatusOK || protojson.Unmarshal(w.Body.Bytes(), &result) != nil || result.Mode != pb.AuthMode_AUTH_MODE_OIDC || result.Principal.CsrfToken != csrf || result.Principal.Identity.Subject != "admin" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session bootstrap", w.Code, w.Body.String())
	}
	request := browserFixtureRequest(session, csrf)
	request.URL.Path = "/auth/logout"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	var revoked pb.SessionRevocation
	if w.Code != http.StatusOK || protojson.Unmarshal(w.Body.Bytes(), &revoked) != nil || revoked.Enforcement != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED || len(w.Result().Cookies()) != 2 {
		t.Fatal("explicit revocation", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("revoked cookie admitted", w.Code)
	}
}
func TestSecurityBrowserRoutesOffAndInvalidQuery(t *testing.T) {
	runtime, cleanup, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	w := httptest.NewRecorder()
	runtime.AuthHTTPHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/auth/session", nil))
	var session pb.BrowserSession
	if w.Code != http.StatusOK || protojson.Unmarshal(w.Body.Bytes(), &session) != nil || session.Mode != pb.AuthMode_AUTH_MODE_OFF || session.Principal != nil {
		t.Fatal("OFF requires a browser session")
	}
	for _, query := range []string{"state=a&state=b", "unexpected=value", "state=%invalid"} {
		req := httptest.NewRequest(http.MethodGet, "https://admin.example/auth/callback?"+query, nil)
		if _, err := browserQuery(req, "state"); err == nil {
			t.Fatal("ambiguous query accepted", query)
		}
	}
}

func TestSecurityBrowserRoutesRetainsGenuinePurposeEvent(t *testing.T) {
	for _, failure := range []string{"", "wrong nonce", "missing auth_time", "wrong actor", "wrong at_hash", "exchange failure"} {
		t.Run(map[string]string{"": "approved"}[failure]+failure, func(t *testing.T) {
			config, data, clock := securityRuntimeFixture(t)
			idp := newSecurityProviderFixture(t, &config, clock)
			runtime, cleanup, err := NewSecurityRuntime(config, data)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			clock.advance(35 * time.Second)
			current, _ := runtime.native.Store().Current()
			actor := security.Identity{Kind: security.OIDCPrincipal, Issuer: idp.server.URL, Subject: "admin"}
			issuer, _ := current.Snapshot().Issuer(actor.Issuer)
			change := issuer
			change.URL = "https://another.example"
			change.ConfigRevision = 0
			change.EnvOwned = false
			prepared, err := runtime.native.Store().PrepareManagement(t.Context(), security.ManagementRequest{ExpectedRevision: current.Sequence(), ChangeID: [16]byte{42}, Actor: actor, Authentication: security.Authentication{Provenance: security.BrowserCode, Class: security.EndUser, IssuerConfigRevision: 1}, Now: clock.Now(), Changes: []security.Change{{Kind: security.PutIssuer, Issuer: &change}}})
			if err != nil {
				t.Fatal(err)
			}
			start, err := runtime.authorizations.Begin(prepared.Binding, clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			clock.advance(2 * time.Second)
			if _, _, err := runtime.authorizations.Start(start.Ticket, current, clock.Now()); err != nil {
				t.Fatal(err)
			}
			trust := oidc.Trust{Issuer: issuer, Generation: current.Generation(), ConfigRevision: issuer.ConfigRevision}
			discovery, err := runtime.fetcher.Discover(t.Context(), issuer)
			if err != nil {
				t.Fatal(err)
			}
			login, err := runtime.logins.BeginAuthorization(trust, discovery, start.ID)
			if err != nil {
				t.Fatal(err)
			}
			redirect, _ := url.Parse(login.AuthorizationURL)
			nonce := redirect.Query().Get("nonce")
			claims := jwt.MapClaims{"iss": issuer.URL, "sub": "admin", "aud": issuer.ClientID, "iat": clock.Now().Unix(), "exp": clock.Now().Add(time.Hour).Unix(), "auth_time": clock.Now().Unix(), "nonce": nonce}
			access := "co-returned-private-token"
			hash := sha512.Sum512([]byte(access))
			claims["at_hash"] = base64.RawURLEncoding.EncodeToString(hash[:32])
			switch failure {
			case "wrong nonce":
				claims["nonce"] = "wrong"
			case "missing auth_time":
				delete(claims, "auth_time")
			case "wrong actor":
				claims["sub"] = "other"
			case "wrong at_hash":
				claims["at_hash"] = "wrong"
			}
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
			token.Header["kid"], token.Header["typ"] = "key", "JWT"
			raw, err := token.SignedString(idp.private)
			if err != nil {
				t.Fatal(err)
			}
			idp.exchangeMu.Lock()
			idp.exchange = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure == "exchange failure" {
					http.Error(w, "failed", 500)
					return
				}
				if r.ParseForm() != nil || r.Form.Get("code") != "private-code" {
					t.Error("exchange omitted code")
				}
				challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(challenge[:]) != redirect.Query().Get("code_challenge") {
					t.Error("exchange lost PKCE association")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"id_token": raw, "access_token": access, "token_type": "Bearer"})
			})
			idp.exchangeMu.Unlock()
			req := httptest.NewRequest(http.MethodGet, config.BrowserOrigin+oidc.CallbackPath(issuer.URL)+"?state="+url.QueryEscape(redirect.Query().Get("state"))+"&code=private-code", nil)
			req.TLS = &tls.ConnectionState{}
			req.AddCookie(&http.Cookie{Name: transactionCookieName, Value: login.TransactionCookie})
			w := httptest.NewRecorder()
			runtime.AuthHTTPHandler().ServeHTTP(w, req)
			status, err := runtime.authorizations.Status(start.ID, actor, current, clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			if failure == "" {
				if w.Code != http.StatusOK || status.State != security.AuthorizationApproved || status.Proof == [32]byte{} {
					t.Fatal("genuine callback did not approve", w.Code, status.State)
				}
				if err := runtime.authorizations.Verify(status.Proof[:], prepared.Binding, clock.Now()); err != nil {
					t.Fatal(err)
				}
			} else if w.Code == http.StatusOK || status.State != security.AuthorizationDenied || status.Proof != [32]byte{} {
				t.Fatal("failed callback minted approval", w.Code, status.State)
			}
			after, _ := runtime.native.Store().Current()
			if after.Digest() != current.Digest() {
				t.Fatal("purpose callback changed reviewed native cut")
			}
			for _, cookie := range w.Result().Cookies() {
				if cookie.Name == sessionCookieName || cookie.Name == csrfCookieName {
					t.Fatal("purpose callback changed ordinary session")
				}
			}
			for _, secret := range []string{raw, access, "private-code", nonce, login.TransactionCookie} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("secret exported")
				}
			}
			cleanup()
			if err := runtime.authorizations.Verify(status.Proof[:], prepared.Binding, clock.Now()); err == nil {
				t.Fatal("runtime closure retained approval")
			}
		})
	}
}
