package integration_test

// Deterministic provider fixture shared by the current-wire gate and its SDK
// consumers. Run only in the isolated fixture container: no host DNS, proxy,
// Fetcher hook or production endpoint/profile is changed. This is source
// evidence, never real Google/Apple or installed-device qualification.
import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
)

const nativeGoogleIssuer = "https://accounts.google.com"
const nativeAppleIssuer = "https://appleid.apple.com"

var nativeFixtureHosts = []string{"accounts.google.com", "oauth2.googleapis.com", "www.googleapis.com", "appleid.apple.com"}

type nativeLoginWireCode struct {
	token, client, redirect string
	loseResponse            bool
}
type nativeLoginWireFixture struct {
	key                *rsa.PrivateKey
	secretFile         string
	mu                 sync.Mutex
	codes              map[string]nativeLoginWireCode
	fetches, exchanges atomic.Int64
}

func newNativeLoginWireFixture(t *testing.T, dir, roots string) *nativeLoginWireFixture {
	t.Helper()
	for _, host := range nativeFixtureHosts {
		ips, err := net.DefaultResolver.LookupNetIP(t.Context(), "ip", host)
		if err != nil || len(ips) != 1 || ips[0].Unmap() != netip.MustParseAddr("127.0.0.1") {
			t.Fatal("native fixture requires isolated container host mappings", host, err)
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &nativeLoginWireFixture{key: key, secretFile: filepath.Join(dir, "fixture-apple.secret"), codes: make(map[string]nativeLoginWireCode)}
	if err := createPrivateTestFile(f.secretFile, []byte("fixture-only-server-secret")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1750), Subject: pkix.Name{CommonName: "native login source fixture"}, DNSNames: nativeFixtureHosts, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rootFile, err := os.OpenFile(roots, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rootFile.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if closeErr := rootFile.Close(); err != nil || closeErr != nil {
		t.Fatal("fixture root append", err, closeErr)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	_ = s.Listener.Close()
	s.Listener, err = net.Listen("tcp4", "127.0.0.1:443")
	if err != nil {
		t.Fatal("isolated fixture HTTPS listener", err)
	}
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return f
}

func (f *nativeLoginWireFixture) configure(t *testing.T, c *provider.SecurityConfig) {
	t.Helper()
	for _, host := range nativeFixtureHosts {
		c.PrivateOrigins["https://"+host] = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	}
	// The exported config owns this existing private type. JSON decoding uses
	// that concrete type without importing a Server-internal package from root.
	bindings := map[string]any{}
	for name, client := range map[string]string{"apple_ios": "com.example.native", "apple_web": "com.example.web"} {
		bindings[name] = map[string]string{"issuer": nativeAppleIssuer, "client_id": client, "token_origin": nativeAppleIssuer, "path": f.secretFile}
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &c.SecretBindings); err != nil {
		t.Fatal(err)
	}
}

func (f *nativeLoginWireFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.fetches.Add(1)
	w.Header().Set("Content-Type", "application/json")
	switch r.Host + r.URL.Path {
	case "accounts.google.com/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": nativeGoogleIssuer, "authorization_endpoint": nativeGoogleIssuer + "/o/oauth2/v2/auth", "token_endpoint": "https://oauth2.googleapis.com/token", "jwks_uri": "https://www.googleapis.com/oauth2/v3/certs", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}})
	case "appleid.apple.com/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": nativeAppleIssuer, "authorization_endpoint": nativeAppleIssuer + "/auth/authorize", "token_endpoint": nativeAppleIssuer + "/auth/token", "jwks_uri": nativeAppleIssuer + "/auth/keys", "response_types_supported": []string{"code", "id_token"}})
	case "www.googleapis.com/oauth2/v3/certs", "appleid.apple.com/auth/keys":
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "native", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), "e": "AQAB"}}})
	case "appleid.apple.com/auth/token":
		f.exchanges.Add(1)
		if r.Method != http.MethodPost || r.ParseForm() != nil || r.Header.Get("Authorization") != "" {
			http.Error(w, "invalid", 400)
			return
		}
		f.mu.Lock()
		code, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		if !ok || r.Form.Get("client_id") != code.client || r.Form.Get("client_secret") != "fixture-only-server-secret" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != code.redirect || r.Form.Get("code_verifier") != "" {
			http.Error(w, "invalid", 400)
			return
		}
		if code.loseResponse {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": code.token, "access_token": "fixture-discarded-access", "refresh_token": "fixture-discarded-refresh", "token_type": "Bearer"})
	default:
		http.NotFound(w, r)
	}
}

func (f *nativeLoginWireFixture) token(t *testing.T, issuer, client, subject, nonce string, mutate func(map[string]any)) string {
	t.Helper()
	claims := map[string]any{"iss": issuer, "sub": subject, "aud": client, "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	if mutate != nil {
		mutate(claims)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"native","typ":"JWT"}`))
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := header + "." + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *nativeLoginWireFixture) apple(t *testing.T, begin *pb.BeginNativeLoginResponse, lose bool) *pb.AppleNativeCredential {
	t.Helper()
	return f.appleSubject(t, begin, "native-user", lose)
}

func (f *nativeLoginWireFixture) appleSubject(t *testing.T, begin *pb.BeginNativeLoginResponse, subject string, lose bool) *pb.AppleNativeCredential {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw[:])
	c := begin.Configuration
	token := f.token(t, nativeAppleIssuer, c.ClientId, subject, c.Nonce, func(c map[string]any) { c["auth_time"] = time.Now().Add(-time.Hour).Unix() })
	f.mu.Lock()
	f.codes[code] = nativeLoginWireCode{token: token, client: c.ClientId, redirect: c.RedirectUri, loseResponse: lose}
	f.mu.Unlock()
	return &pb.AppleNativeCredential{IdentityToken: token, AuthorizationCode: code, State: c.State}
}

// Reads alone may wait for normal current-authority renewal after an Apply.
// Never replay a login completion or other possibly dispatched mutation here.
func nativeWireAwait(t *testing.T, ctx context.Context, check func() error) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		if err := check(); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("current native read did not become ready")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func nativeWireApply(t *testing.T, ctx context.Context, control graphv1connect.LanternSecurityServiceClient, admin string, change *pb.SecurityChange) {
	t.Helper()
	var current *pb.GetCurrentPrincipalResponse
	nativeWireAwait(t, ctx, func() error {
		v, err := control.GetCurrentPrincipal(ctx, securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
		if err == nil {
			current = v.Msg
		}
		return err
	})
	prepared, err := control.PrepareSecurityChanges(ctx, securityWireRequest(admin, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: current.Version.CurrentProfile, ExpectedCut: current.Version.CurrentCut, Changes: []*pb.SecurityChange{change}}}))
	if err != nil {
		t.Fatal("native policy prepare", err)
	}
	result, err := control.ApplySecurityChanges(ctx, securityWireRequest(admin, &pb.ApplySecurityChangesRequest{CurrentReview: prepared.Msg.CurrentReview}))
	var original *pb.CurrentSecurityOriginalOutcome
	if err == nil {
		original = result.Msg.GetCurrentResult().GetOriginal()
	}
	if original == nil {
		original = awaitCurrentPublicOriginal(t, ctx, control, admin, prepared.Msg.CurrentReview)
	}
	if original.GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
		t.Fatal("native policy mutation not applied")
	}
}
