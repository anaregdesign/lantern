package integration_test

// Deterministic provider fixture shared by the current-wire gate and its SDK
// consumers. Run only in the isolated fixture container: no host DNS, proxy,
// Fetcher hook or production endpoint/profile is changed. This is source
// evidence, never real Google/Apple or installed-device qualification.
import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const nativeGoogleIssuer = "https://accounts.google.com"
const nativeAppleIssuer = "https://appleid.apple.com"

var nativeFixtureHosts = []string{"accounts.google.com", "oauth2.googleapis.com", "www.googleapis.com", "appleid.apple.com"}

type nativeFixtureServingOptions struct {
	Origin, Prefix, ExportDir string
	ListenPort                int
	Lifetime                  time.Duration
	MembershipExpires         time.Time
}

func nativeFixtureServingFromEnv(getenv func(string) string) (*nativeFixtureServingOptions, error) {
	keys := []string{"ORIGIN", "PREFIX", "EXPORT_DIR", "LISTEN_PORT", "LIFETIME_SECONDS"}
	if getenv("LANTERN_NATIVE_FIXTURE_SERVE") == "" {
		for _, key := range keys {
			if getenv("LANTERN_NATIVE_FIXTURE_"+key) != "" {
				return nil, errors.New("native serving configuration requires explicit opt-in")
			}
		}
		return nil, nil
	}
	if getenv("LANTERN_NATIVE_FIXTURE_SERVE") != "1" || getenv("LANTERN_NATIVE_LOGIN_GATE") != "1" || getenv("LANTERN_CURRENT_PUBLIC_GATE") != "1" ||
		!strings.HasPrefix(currentSourceMarker, "production native constructor:") {
		return nil, errors.New("native serving requires the explicit normal-build current/native gate")
	}
	for _, key := range []string{"LANTERN_CURRENT_PUBLIC_SDK4", "LANTERN_CURRENT_MEMBERSHIP_GATE", "LANTERN_CURRENT_MEMBERSHIP_CONTAINER", "LANTERN_CURRENT_FIXTURE_HOSTS", "LANTERN_CURRENT_FIXTURE_QUERY_ROLES_FILE", "LANTERN_CURRENT_DATA_HA"} {
		if value := getenv(key); value != "" && value != "0" {
			return nil, errors.New("native serving cannot combine fixture campaigns")
		}
	}
	o := &nativeFixtureServingOptions{Origin: getenv("LANTERN_NATIVE_FIXTURE_ORIGIN"), Prefix: getenv("LANTERN_NATIVE_FIXTURE_PREFIX"), ExportDir: getenv("LANTERN_NATIVE_FIXTURE_EXPORT_DIR")}
	u, err := url.Parse(o.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != o.Origin || strings.ToLower(u.Host) != u.Host {
		return nil, errors.New("canonical HTTPS native service origin required")
	}
	if !strings.HasPrefix(o.Prefix, "orders:") || !strings.HasSuffix(o.Prefix, ":") || len(o.Prefix) > 128 || strings.ContainsAny(o.Prefix, " \t\r\n\x00") {
		return nil, errors.New("bounded orders: native fixture prefix required")
	}
	o.ListenPort, err = strconv.Atoi(getenv("LANTERN_NATIVE_FIXTURE_LISTEN_PORT"))
	if err != nil || o.ListenPort < 1024 || o.ListenPort > 65535 {
		return nil, errors.New("explicit unprivileged native listener port required")
	}
	seconds, err := strconv.Atoi(getenv("LANTERN_NATIVE_FIXTURE_LIFETIME_SECONDS"))
	if err != nil || seconds < 15 || seconds > 540 {
		return nil, errors.New("native serving lifetime must be 15..540 seconds")
	}
	o.Lifetime = time.Duration(seconds) * time.Second
	info, err := os.Lstat(o.ExportDir)
	if err != nil || !filepath.IsAbs(o.ExportDir) || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("new private mode-0700 native export directory required")
	}
	for _, name := range []string{"ca.pem", "fixture.json", "ready", "release"} {
		if _, err := os.Lstat(filepath.Join(o.ExportDir, name)); !os.IsNotExist(err) {
			return nil, errors.New("native export/release paths must be newly allocated")
		}
	}
	return o, nil
}

func configureNativeFixtureServingTLS(t *testing.T, server *httptest.Server, options *nativeFixtureServingOptions) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(options.Origin)
	if err != nil {
		t.Fatal("invalid native service origin")
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1809), Subject: pkix.Name{CommonName: "synthetic native fixture only"}, NotBefore: now.Add(-time.Minute), NotAfter: options.MembershipExpires, BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		cert.IPAddresses = append(cert.IPAddresses, ip)
	} else {
		cert.DNSNames = []string{u.Hostname()}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	path := filepath.Join(options.ExportDir, "ca.pem")
	if err := createPrivateTestFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		t.Fatal("private native CA export", err)
	}
	return path
}

type nativeFixtureAccountExport struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type nativeFixtureServingExport struct {
	Schema       int                                   `json:"schema"`
	Kind         string                                `json:"kind"`
	Endpoint     string                                `json:"endpoint"`
	Prefix       string                                `json:"prefix"`
	CAFile       string                                `json:"ca_file"`
	ServingUntil time.Time                             `json:"serving_until"`
	Accounts     map[string]nativeFixtureAccountExport `json:"accounts"`
}

func writeNativeFixtureServingExport(options *nativeFixtureServingOptions, issued [2]*pb.CompleteNativeLoginResponse, verified [2]*pb.GetNativeSessionResponse) error {
	now := time.Now().UTC()
	until := now.Add(options.Lifetime)
	if !until.Add(15 * time.Second).Before(options.MembershipExpires) {
		return errors.New("signed native fixture membership has insufficient remaining serving lifetime")
	}
	export := nativeFixtureServingExport{Schema: 1, Kind: "synthetic-native-serving", Endpoint: options.Origin, Prefix: options.Prefix, CAFile: "ca.pem", ServingUntil: until, Accounts: map[string]nativeFixtureAccountExport{}}
	for i, name := range []string{"a", "b"} {
		session := verified[i].GetSession()
		original := issued[i].GetSession()
		if !strings.HasPrefix(issued[i].GetCredential(), "lnu1_") || session == nil || session.Identity == nil || session.CredentialProfile != "native_end_user" || session.ServiceOrigin != options.Origin ||
			len(session.GetUserId()) != 32 || session.GetPersonalPrefix() != "users:"+session.GetUserId()+":" || !session.ExpiresAt.AsTime().After(until) ||
			!session.ExpiresAt.AsTime().After(now.Add(10*time.Minute)) || session.ExpiresAt.AsTime().Sub(session.CreatedAt.AsTime()) != 8*time.Hour ||
			original.GetCredentialProfile() != session.CredentialProfile || original.GetServiceOrigin() != session.ServiceOrigin || !proto.Equal(original.GetIdentity(), session.Identity) ||
			original.GetUserId() != session.GetUserId() || original.GetPersonalPrefix() != session.GetPersonalPrefix() || !proto.Equal(original.GetExpiresAt(), session.ExpiresAt) || !proto.Equal(original.GetCreatedAt(), session.CreatedAt) ||
			session.GetVersion().GetCurrentProfile() == nil {
			return errors.New("original registered native credential and verified session required")
		}
		export.Accounts[name] = nativeFixtureAccountExport{issued[i].Credential, session.ExpiresAt.AsTime().UTC()}
	}
	a, b := verified[0].Session, verified[1].Session
	if proto.Equal(a.Identity, b.Identity) || a.GetUserId() == b.GetUserId() || a.GetPersonalPrefix() == b.GetPersonalPrefix() || issued[0].Credential == issued[1].Credential {
		return errors.New("native fixture accounts must be distinct registered Principals")
	}
	raw, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return err
	}
	if err := createPrivateTestFile(filepath.Join(options.ExportDir, "fixture.json"), append(raw, '\n')); err != nil {
		return err
	}
	return createPrivateTestFile(filepath.Join(options.ExportDir, "ready"), []byte("synthetic native fixture schema 1\n"))
}

func waitNativeFixtureServing(ctx context.Context, release string, lifetime time.Duration, check func() error) (string, error) {
	timeout := time.NewTimer(lifetime)
	defer timeout.Stop()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	health := time.NewTicker(2 * time.Second)
	defer health.Stop()
	for {
		if info, err := os.Lstat(release); err == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
				return "", errors.New("native release must be a private regular file")
			}
			return "released", nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "timeout", nil
		case <-poll.C:
		case <-health.C:
			if err := check(); err != nil {
				return "", err
			}
		}
	}
}

func TestNativeFixtureServing(t *testing.T) {
	t.Run("configuration", func(t *testing.T) {
		dir := t.TempDir()
		base := map[string]string{"LANTERN_NATIVE_FIXTURE_SERVE": "1", "LANTERN_NATIVE_LOGIN_GATE": "1", "LANTERN_CURRENT_PUBLIC_GATE": "1", "LANTERN_NATIVE_FIXTURE_ORIGIN": "https://installed.example:16443", "LANTERN_NATIVE_FIXTURE_PREFIX": "orders:installed:", "LANTERN_NATIVE_FIXTURE_EXPORT_DIR": dir, "LANTERN_NATIVE_FIXTURE_LISTEN_PORT": "16443", "LANTERN_NATIVE_FIXTURE_LIFETIME_SECONDS": "540"}
		if got, err := nativeFixtureServingFromEnv(func(string) string { return "" }); err != nil || got != nil {
			t.Fatal("ordinary fixture changed", err)
		}
		if strings.HasPrefix(currentSourceMarker, "test-build") {
			if _, err := nativeFixtureServingFromEnv(func(k string) string { return base[k] }); err == nil {
				t.Fatal("controlled-source build admitted serving")
			}
			return
		}
		if got, err := nativeFixtureServingFromEnv(func(k string) string { return base[k] }); err != nil || got.Lifetime != 540*time.Second {
			t.Fatal("valid serving configuration refused", err)
		}
		for key, value := range map[string]string{"LANTERN_NATIVE_FIXTURE_SERVE": "", "LANTERN_NATIVE_LOGIN_GATE": "", "LANTERN_NATIVE_FIXTURE_ORIGIN": "https://installed.example/", "LANTERN_NATIVE_FIXTURE_PREFIX": "other:", "LANTERN_NATIVE_FIXTURE_LISTEN_PORT": "443", "LANTERN_NATIVE_FIXTURE_LIFETIME_SECONDS": "541", "LANTERN_CURRENT_PUBLIC_SDK4": "1", "LANTERN_CURRENT_MEMBERSHIP_GATE": "1"} {
			t.Run(key, func(t *testing.T) {
				_, err := nativeFixtureServingFromEnv(func(k string) string {
					if k == key {
						return value
					}
					return base[k]
				})
				if err == nil {
					t.Fatal("unsafe serving configuration admitted")
				}
			})
		}
		if err := createPrivateTestFile(filepath.Join(dir, "release"), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := nativeFixtureServingFromEnv(func(k string) string { return base[k] }); err == nil {
			t.Fatal("reused export admitted")
		}
	})
	t.Run("TLS and original private export", func(t *testing.T) {
		o := &nativeFixtureServingOptions{Origin: "https://installed.example", Prefix: "orders:", ExportDir: t.TempDir(), Lifetime: time.Minute, MembershipExpires: time.Now().Add(5 * time.Minute)}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		ca := configureNativeFixtureServingTLS(t, server, o)
		server.StartTLS()
		t.Cleanup(server.Close)
		raw, err := os.ReadFile(ca)
		roots := x509.NewCertPool()
		if err != nil || !roots.AppendCertsFromPEM(raw) {
			t.Fatal("missing native CA")
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "installed.example", MinVersion: tls.VersionTLS12}}
		t.Cleanup(transport.CloseIdleConnections)
		response, err := (&http.Client{Transport: transport}).Get(server.URL)
		if err != nil || response.StatusCode != http.StatusNoContent {
			t.Fatal("native fixture service-origin TLS", err)
		}
		_ = response.Body.Close()
		var issued [2]*pb.CompleteNativeLoginResponse
		var verified [2]*pb.GetNativeSessionResponse
		for i, subject := range []string{"native-user", "registration-legacy"} {
			uid := strings.Repeat(fmt.Sprint(i+1), 32)
			prefix := "users:" + uid + ":"
			now := time.Now()
			session := &pb.NativeSession{CredentialProfile: "native_end_user", ServiceOrigin: o.Origin, Identity: &pb.SecurityIdentity{Issuer: nativeGoogleIssuer, Subject: subject}, UserId: &uid, PersonalPrefix: &prefix, CreatedAt: timestamppb.New(now), ExpiresAt: timestamppb.New(now.Add(8 * time.Hour)), Version: &pb.SecurityVersion{CurrentProfile: &pb.CurrentAuthorityProfile{}}}
			issued[i] = &pb.CompleteNativeLoginResponse{Credential: fmt.Sprintf("lnu1_original_%d", i), Session: session}
			verified[i] = &pb.GetNativeSessionResponse{Session: proto.Clone(session).(*pb.NativeSession)}
		}
		verified[1].Session.UserId = verified[0].Session.UserId
		if err := writeNativeFixtureServingExport(o, issued, verified); err == nil {
			t.Fatal("unverified/distinct account metadata not required")
		}
		verified[1].Session = proto.Clone(issued[1].Session).(*pb.NativeSession)
		savedSession := proto.Clone(issued[1].Session).(*pb.NativeSession)
		issued[1].Session.UserId, issued[1].Session.PersonalPrefix = nil, nil
		verified[1].Session = proto.Clone(issued[1].Session).(*pb.NativeSession)
		if err := writeNativeFixtureServingExport(o, issued, verified); err == nil {
			t.Fatal("UID-less native session admitted")
		}
		issued[1].Session = proto.Clone(savedSession).(*pb.NativeSession)
		issued[1].Session.CreatedAt = timestamppb.New(time.Now().Add(-8*time.Hour + 9*time.Minute))
		issued[1].Session.ExpiresAt = timestamppb.New(issued[1].Session.CreatedAt.AsTime().Add(8 * time.Hour))
		verified[1].Session = proto.Clone(issued[1].Session).(*pb.NativeSession)
		if err := writeNativeFixtureServingExport(o, issued, verified); err == nil {
			t.Fatal("native expiry with at most ten minutes remaining admitted")
		}
		issued[1].Session = savedSession
		verified[1].Session = proto.Clone(savedSession).(*pb.NativeSession)
		saved := o.MembershipExpires
		o.MembershipExpires = time.Now().Add(o.Lifetime)
		if err := writeNativeFixtureServingExport(o, issued, verified); err == nil {
			t.Fatal("insufficient signed membership lifetime admitted")
		}
		o.MembershipExpires = saved
		if err := writeNativeFixtureServingExport(o, issued, verified); err != nil {
			t.Fatal(err)
		}
		var exported nativeFixtureServingExport
		raw, err = os.ReadFile(filepath.Join(o.ExportDir, "fixture.json"))
		if err != nil || json.Unmarshal(raw, &exported) != nil || exported.Accounts["a"].AccessToken != issued[0].Credential || exported.Accounts["b"].AccessToken != issued[1].Credential {
			t.Fatal("original credential bytes changed")
		}
		for _, name := range []string{"ca.pem", "fixture.json", "ready"} {
			info, err := os.Lstat(filepath.Join(o.ExportDir, name))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
				t.Fatal("native export is not private", name)
			}
		}
		if err := writeNativeFixtureServingExport(o, issued, verified); err == nil {
			t.Fatal("private native export was replaced")
		}
	})
	t.Run("finite wait and explicit release", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "release")
		if reason, err := waitNativeFixtureServing(t.Context(), path, time.Millisecond, func() error { return nil }); err != nil || reason != "timeout" {
			t.Fatal(reason, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := waitNativeFixtureServing(ctx, path, time.Second, func() error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation hidden", err)
		}
		if err := createPrivateTestFile(path, nil); err != nil {
			t.Fatal(err)
		}
		if reason, err := waitNativeFixtureServing(t.Context(), path, time.Second, func() error { return nil }); err != nil || reason != "released" {
			t.Fatal(reason, err)
		}
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := waitNativeFixtureServing(t.Context(), path, time.Second, func() error { return nil }); err == nil {
			t.Fatal("public release marker accepted")
		}
	})
	t.Run("current guard failure remains failure", func(t *testing.T) {
		failure := errors.New("current session guard refused")
		if _, err := waitNativeFixtureServing(t.Context(), filepath.Join(t.TempDir(), "release"), 3*time.Second, func() error { return failure }); !errors.Is(err, failure) {
			t.Fatal("serving suppressed a current guard failure", err)
		}
	})
}

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
