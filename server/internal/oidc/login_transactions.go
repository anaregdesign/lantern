package oidc

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

const loginTransactionLifetime = 10 * time.Minute
const maxLoginTransactions = 1024

var ErrLoginTransaction = errors.New("invalid or expired login transaction")

// LoginTransactions belongs exclusively to the pinned control Server process.
// Restart invalidates unfinished logins. State consumption is atomic and never
// promised across arbitrary replicas or an automatic owner replacement.
type LoginTransactions struct {
	mu      sync.Mutex
	pending map[[32]byte]loginTransaction
	origin  string
	returns map[string]bool
	now     func() time.Time
}
type loginTransaction struct {
	trust                                       Trust
	discovery                                   Discovery
	cookieDigest                                [32]byte
	nonce, verifier, returnPath, replacesDigest string
	createdAt, expiresAt                        time.Time
}

// LoginStart contains only the redirect and host-bound transaction-cookie
// material. Its String method prevents accidental credential logging.
type LoginStart struct {
	AuthorizationURL, TransactionCookie string
	ExpiresAt                           time.Time
}

func (LoginStart) String() string { return "[redacted OIDC login start]" }

// LoginCompletion is server-private single-use evidence for code exchange.
type LoginCompletion struct{ transaction loginTransaction }

func (LoginCompletion) String() string           { return "[redacted OIDC login completion]" }
func (c LoginCompletion) Trust() Trust           { return c.transaction.trust }
func (c LoginCompletion) Discovery() Discovery   { return c.transaction.discovery }
func (c LoginCompletion) Nonce() string          { return c.transaction.nonce }
func (c LoginCompletion) Verifier() string       { return c.transaction.verifier }
func (c LoginCompletion) ReturnPath() string     { return c.transaction.returnPath }
func (c LoginCompletion) ReplacesDigest() string { return c.transaction.replacesDigest }

func NewLoginTransactions(origin string, returnPaths []string) (*LoginTransactions, error) {
	return NewLoginTransactionsWithClock(origin, returnPaths, time.Now)
}
func NewLoginTransactionsWithClock(origin string, returnPaths []string, clock func() time.Time) (*LoginTransactions, error) {
	if clock == nil {
		clock = time.Now
	}
	canonical, err := endpointURL(origin)
	if err != nil || canonical.Path != "" && canonical.Path != "/" || canonical.RawQuery != "" || canonical.ForceQuery {
		return nil, ErrLoginTransaction
	}
	exact := canonical.Scheme + "://" + canonical.Host
	if origin != exact {
		return nil, ErrLoginTransaction
	}
	manager := &LoginTransactions{pending: make(map[[32]byte]loginTransaction), origin: exact, returns: make(map[string]bool), now: clock}
	for _, p := range returnPaths {
		if !validReturnPath(p) {
			return nil, ErrLoginTransaction
		}
		manager.returns[p] = true
	}
	if len(manager.returns) == 0 || len(manager.returns) > 64 {
		return nil, ErrLoginTransaction
	}
	return manager, nil
}
func validReturnPath(value string) bool {
	if len(value) == 0 || len(value) > 1024 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" && parsed.RawPath == "" && path.Clean(parsed.Path) == parsed.Path && !strings.HasPrefix(parsed.Path, "/auth")
}

// CallbackPath gives every exact Issuer a distinct registered callback. This
// prevents authorization-response mix-up even when the provider lacks RFC 9207.
func CallbackPath(issuer string) string {
	digest := sha256.Sum256([]byte(issuer))
	return "/auth/callback/" + hex.EncodeToString(digest[:])
}
func randomLoginValue() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
func (m *LoginTransactions) Begin(trust Trust, discovery Discovery, returnPath, replacesDigest string, stepUp bool) (LoginStart, error) {
	if m == nil || !trust.Issuer.Enabled || trust.Generation == [16]byte{} || trust.ConfigRevision == 0 || discovery.Issuer != trust.Issuer.URL || trust.Issuer.RedirectURI != m.origin+CallbackPath(trust.Issuer.URL) || !m.returns[returnPath] ||
		!slices.Contains(discovery.ResponseTypes, "code") || !slices.Contains(discovery.CodeChallengeMethods, "S256") {
		return LoginStart{}, ErrLoginTransaction
	}
	authorize, err := endpointURL(discovery.AuthorizationEndpoint)
	if err != nil {
		return LoginStart{}, ErrLoginTransaction
	}
	if replacesDigest != "" {
		raw, err := hex.DecodeString(replacesDigest)
		if err != nil || len(raw) != 32 || replacesDigest != hex.EncodeToString(raw) {
			return LoginStart{}, ErrLoginTransaction
		}
	}
	state, err := randomLoginValue()
	if err != nil {
		return LoginStart{}, err
	}
	cookie, err := randomLoginValue()
	if err != nil {
		return LoginStart{}, err
	}
	nonce, err := randomLoginValue()
	if err != nil {
		return LoginStart{}, err
	}
	verifier, err := randomLoginValue()
	if err != nil {
		return LoginStart{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	query := authorize.Query()
	query.Set("response_type", "code")
	query.Set("response_mode", "query")
	query.Set("client_id", trust.Issuer.ClientID)
	query.Set("redirect_uri", trust.Issuer.RedirectURI)
	query.Set("scope", "openid")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	query.Set("max_age", "300")
	// max_age alone does not make every provider include auth_time. Request
	// the signed claim explicitly; verification still rejects missing/stale
	// evidence rather than inferring authentication time from issuance.
	query.Set("claims", `{"id_token":{"auth_time":{"essential":true}}}`)
	if stepUp {
		query.Set("max_age", "0")
		query.Set("prompt", "login")
	}
	authorize.RawQuery = query.Encode()
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id, transaction := range m.pending {
		if !now.Before(transaction.expiresAt) {
			delete(m.pending, id)
		}
	}
	if len(m.pending) >= maxLoginTransactions {
		return LoginStart{}, ErrLoginTransaction
	}
	transaction := loginTransaction{trust: trust, discovery: discovery, cookieDigest: sha256.Sum256([]byte(cookie)), nonce: nonce, verifier: verifier, returnPath: returnPath, replacesDigest: replacesDigest, createdAt: now, expiresAt: now.Add(loginTransactionLifetime)}
	// Detach every mutable configuration slice retained across the redirect.
	transaction.trust.Issuer.Algorithms = append([]string(nil), trust.Issuer.Algorithms...)
	transaction.discovery.ResponseTypes = append([]string(nil), discovery.ResponseTypes...)
	transaction.discovery.CodeChallengeMethods = append([]string(nil), discovery.CodeChallengeMethods...)
	m.pending[sha256.Sum256([]byte(state))] = transaction
	return LoginStart{AuthorizationURL: authorize.String(), TransactionCookie: cookie, ExpiresAt: transaction.expiresAt}, nil
}
func (m *LoginTransactions) Consume(state, cookie, callbackPath, responseIssuer string) (LoginCompletion, error) {
	if m == nil || len(state) != 43 || len(cookie) != 43 || len(responseIssuer) > 2048 {
		return LoginCompletion{}, ErrLoginTransaction
	}
	stateBytes, err := base64.RawURLEncoding.Strict().DecodeString(state)
	if err != nil || len(stateBytes) != 32 {
		return LoginCompletion{}, ErrLoginTransaction
	}
	cookieBytes, err := base64.RawURLEncoding.Strict().DecodeString(cookie)
	if err != nil || len(cookieBytes) != 32 {
		return LoginCompletion{}, ErrLoginTransaction
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := sha256.Sum256([]byte(state))
	transaction, known := m.pending[id]
	if !known {
		return LoginCompletion{}, ErrLoginTransaction
	}
	now := m.now()
	cookieDigest := sha256.Sum256([]byte(cookie))
	if now.Before(transaction.createdAt) || !now.Before(transaction.expiresAt) || subtle.ConstantTimeCompare(cookieDigest[:], transaction.cookieDigest[:]) != 1 || callbackPath != CallbackPath(transaction.trust.Issuer.URL) || responseIssuer != "" && responseIssuer != transaction.trust.Issuer.URL {
		return LoginCompletion{}, ErrLoginTransaction
	}
	delete(m.pending, id)
	return LoginCompletion{transaction: transaction}, nil
}
