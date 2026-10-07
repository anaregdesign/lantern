package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// queryFixture exposes paths only. The synthetic issuer does no provider login,
// client-credentials issuance or token renewal. Its signing key stays in memory.
type queryFixture struct {
	Mode             string      `json:"mode"`
	Issuer           string      `json:"issuer"`
	AdminTokenFile   string      `json:"admin_token_file"`
	ReaderTokenFile  string      `json:"reader_token_file"`
	InvalidTokenFile string      `json:"invalid_token_file"`
	ExpiresAt        time.Time   `json:"expires_at"`
	ReaderSubject    string      `json:"reader_subject"`
	ReaderRole       string      `json:"reader_role"`
	Server           queryBinary `json:"server"`
}

type queryBinary struct {
	SHA256   string `json:"sha256"`
	Revision string `json:"revision"`
	Modified string `json:"modified"`
}

func queryBinaryProvenance(path string) (queryBinary, error) {
	result := queryBinary{Revision: "unknown", Modified: "unknown"}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return result, err
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return result, err
	}
	result.SHA256 = hex.EncodeToString(digest.Sum(nil))
	for _, item := range info.Settings {
		if item.Key == "vcs.revision" {
			result.Revision = item.Value
		}
		if item.Key == "vcs.modified" {
			result.Modified = item.Value
		}
	}
	return result, nil
}

func queryRole() security.Role {
	visible, denied, all := "bench:", "bench:private:", ""
	return security.Role{ID: "fixture_query_reader", Name: "Local protected query reader", Rules: []security.PermissionRule{
		{ID: "read", Action: security.VertexRead, Effect: security.Allow, Resource: security.DataResource, Prefix: &visible},
		{ID: "deny", Action: security.VertexRead, Effect: security.Deny, Resource: security.DataResource, Prefix: &denied},
		{ID: "query", Action: security.Query, Effect: security.Allow, Resource: security.DataResource, Prefix: &all},
	}}
}

func queryJWT(private ed25519.PrivateKey, issuer, subject string, now time.Time) string {
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": "query", "typ": "at+jwt"})
	claims, _ := json.Marshal(map[string]any{"iss": issuer, "sub": subject, "aud": "lantern-fixture", "client_id": "fixture-query-client", "jti": "fixture-query-" + subject, "iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(), "auth_time": now.Unix()})
	payload := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	return payload + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(payload)))
}

// addFixtureQuery retains the standalone public TLS transport in OFF as well as
// OIDC. Only the existing operator bootstrap inputs and local issuer are set;
// the driver assigns the reader through the normal management API before load.
func addFixtureQuery(result *fixture, directory string) (func(), error) {
	if result == nil || len(result.Nodes) != 1 || result.Nodes[0].PeerOrigin != "" || result.Query != nil {
		return nil, errors.New("query fixture requires one standalone node")
	}
	node := &result.Nodes[0]
	mode := node.Environment["LANTERN_AUTH_MODE"]
	if mode == "" {
		mode = "off"
	}
	if mode != "off" && mode != "oidc" {
		return nil, errors.New("invalid query fixture mode")
	}
	certificate, err := tls.LoadX509KeyPair(node.Environment["LANTERN_TLS_CERT_FILE"], node.Environment["LANTERN_TLS_KEY_FILE"])
	if err != nil {
		return nil, err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	issuer := "https://" + listener.Addr().String()
	server := &http.Server{ReadHeaderTimeout: time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				http.Error(w, "metadata only", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}})
			case "/jwks":
				_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "kid": "query", "alg": "EdDSA", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)}}})
			default:
				http.NotFound(w, r)
			}
		})}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.ServeTLS(listener, "", "") }()
	stop := func() { _ = server.Close(); <-done }
	success := false
	defer func() {
		if !success {
			stop()
		}
	}()
	now := time.Now().UTC().Truncate(time.Second)
	query := &queryFixture{Mode: mode, Issuer: issuer, ReaderSubject: "fixture-query-reader", ReaderRole: queryRole().ID, ExpiresAt: now.Add(15 * time.Minute)}
	_, wrongPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	for _, item := range []struct {
		name, subject string
		key           ed25519.PrivateKey
		target        *string
	}{
		{"query-admin.jwt", "fixture-admin", private, &query.AdminTokenFile},
		{"query-reader.jwt", query.ReaderSubject, private, &query.ReaderTokenFile},
		{"query-invalid.jwt", query.ReaderSubject, wrongPrivate, &query.InvalidTokenFile},
	} {
		*item.target, err = writeFile(directory, item.name, []byte(queryJWT(item.key, issuer, item.subject, now)))
		if err != nil {
			return nil, err
		}
	}
	node.Environment["LANTERN_AUTH_MODE"] = mode
	node.Environment["LANTERN_SEARCH_ENABLED"] = "true"
	if mode == "oidc" {
		roles, err := json.Marshal(append(fixtureRoles(), queryRole()))
		if err != nil {
			return nil, err
		}
		origins, _ := json.Marshal(map[string][]string{issuer: {"127.0.0.1/32"}})
		node.Environment["LANTERN_SECURITY_BOOTSTRAP_ROLES"] = string(roles)
		node.Environment["LANTERN_OIDC_ADMIN_ISSUER"] = issuer
		node.Environment["LANTERN_OIDC_REDIRECT_URI"] = node.PublicOrigin + oidc.CallbackPath(issuer)
		node.Environment["LANTERN_OIDC_ROOT_CA_FILE"] = result.CAFile
		node.Environment["LANTERN_OIDC_PRIVATE_ORIGINS"] = string(origins)
		// The local issuer reserves the two synthetic end-user subjects and
		// serves no issuance endpoint, so no OAuth client can choose either.
		node.Environment["LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED"] = strconv.FormatBool(true)
	}
	if !strings.HasPrefix(node.PublicOrigin, "https://") {
		return nil, errors.New("query fixture requires HTTPS in both modes")
	}
	result.Query = query
	success = true
	return stop, nil
}
