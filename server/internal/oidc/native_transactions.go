package oidc

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

const MaxNativeStateBytes = 101

var ErrNativeCapacity = errors.New("native login capacity exhausted")

// NativeTransactions is process-owned bounded delivery state. It has no
// persistence, worker or independent authorization decision.
type NativeTransactions struct {
	mu       sync.Mutex
	pending  map[[32]byte]*nativeTransaction
	bounds   func() (time.Time, time.Time, error)
	affinity string
	closed   bool
}

type nativeTransaction struct {
	trust                   Trust
	profile                 security.CurrentProfile
	client                  security.NativeClient
	discovery               Discovery
	proof, state            [32]byte
	nonce                   string
	createdAt, expiresAt    time.Time
	staged                  bool
	stagedToken, stagedCode [32]byte
	registration            security.NativeRegistrationPurpose
}

// NativeStart is app-owned ephemeral plugin/delivery configuration. Only the
// public client fields are returned; the private secret binding is excluded.
type NativeStart struct {
	AttemptID, CompletionSecret, Affinity                       string
	Kind, ClientProfileID, ClientID, ServiceOrigin, RedirectURI string
	State, Nonce                                                string
	ExpiresAt                                                   time.Time
}

func (NativeStart) String() string   { return "[redacted native login start]" }
func (NativeStart) GoString() string { return "[redacted native login start]" }

// NativeAttempt can only refer to a live entry of its concrete owner. Its zero
// value and a serialized/deserialized copy cannot satisfy completion.
type NativeAttempt struct {
	owner       *NativeTransactions
	id          [32]byte
	transaction *nativeTransaction
}

func (NativeAttempt) String() string   { return "[redacted native login attempt]" }
func (NativeAttempt) GoString() string { return "[redacted native login attempt]" }
func (a NativeAttempt) Trust() Trust {
	if a.transaction == nil {
		return Trust{}
	}
	return a.transaction.trust.detached()
}
func (a NativeAttempt) Profile() security.CurrentProfile {
	if a.transaction == nil {
		return security.CurrentProfile{}
	}
	return a.transaction.profile
}
func (a NativeAttempt) ClientProfileID() string {
	if a.transaction == nil {
		return ""
	}
	return a.transaction.client.ID
}

func (a NativeAttempt) IsLogin() bool {
	return a.owner != nil && a.transaction != nil && a.transaction.registration == (security.NativeRegistrationPurpose{})
}

func (a NativeAttempt) RegistrationPurpose() security.NativeRegistrationPurpose {
	if a.owner == nil || a.transaction == nil {
		return security.NativeRegistrationPurpose{}
	}
	return a.transaction.registration
}

type NativeCompletion struct {
	attempt         NativeAttempt
	credential      NativeProviderCredential
	consumedAt      time.Time
	exchangeStarted *atomic.Bool
}

func (NativeCompletion) String() string   { return "[redacted native login completion]" }
func (NativeCompletion) GoString() string { return "[redacted native login completion]" }

func (c NativeCompletion) IsLogin() bool {
	return c.credential.matches(c.attempt) && !c.consumedAt.IsZero() && c.attempt.IsLogin()
}

func (c NativeCompletion) RegistrationPurpose() security.NativeRegistrationPurpose {
	if !c.credential.matches(c.attempt) || c.consumedAt.IsZero() {
		return security.NativeRegistrationPurpose{}
	}
	return c.attempt.RegistrationPurpose()
}

// RetainedBytes conservatively charges one retained completion plus tokens.
// It reports sizes only; raw provider data and private trust never escape.
func (c NativeCompletion) RetainedBytes(tokens NativeTokens) (uint64, error) {
	if !c.credential.matches(c.attempt) || c.consumedAt.IsZero() {
		return 0, ErrInvalidToken
	}
	t := c.attempt.transaction
	raw, err := json.Marshal(struct {
		Trust     Trust
		Client    security.NativeClient
		Discovery Discovery
	}{t.trust, t.client, t.discovery})
	if err != nil {
		return 0, err
	}
	// Encoded trust dominates its bounded strings/slices. Evidence and all
	// fixed transaction/producer bookkeeping receive a separate upper charge.
	return uint64(2*len(raw) + len(c.credential.idToken) + len(c.credential.code) + len(t.nonce) + len(tokens.idToken) + len(tokens.accessToken) + 2*security.MaxAuthenticationEvidenceBytes + 8192), nil
}

func NewNativeTransactions(bounds func() (time.Time, time.Time, error), affinity string) (*NativeTransactions, error) {
	if bounds == nil || !validLoginAffinity(affinity) {
		return nil, ErrLoginTransaction
	}
	return &NativeTransactions{pending: make(map[[32]byte]*nativeTransaction), bounds: bounds, affinity: affinity}, nil
}

func nativeRandomValue(raw string) bool {
	if len(raw) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == raw
}

func (m *NativeTransactions) validState(state string) bool {
	return m != nil && len(state) <= MaxNativeStateBytes && strings.HasPrefix(state, m.affinity+".") && nativeRandomValue(strings.TrimPrefix(state, m.affinity+"."))
}

func (m *NativeTransactions) currentLocked() (time.Time, time.Time, error) {
	if m.closed {
		return time.Time{}, time.Time{}, ErrLoginTransaction
	}
	low, high, err := m.bounds()
	if err != nil || low.IsZero() || high.Before(low) {
		return time.Time{}, time.Time{}, ErrLoginTransaction
	}
	for id, t := range m.pending {
		if !high.Before(t.expiresAt) {
			delete(m.pending, id)
		}
	}
	return low.UTC().Round(0), high.UTC().Round(0), nil
}

func (m *NativeTransactions) Begin(trust Trust, profile security.CurrentProfile, clientID string, discovery Discovery) (NativeStart, error) {
	return m.begin(trust, profile, clientID, discovery, security.NativeRegistrationPurpose{})
}

// BeginRegistration binds the closed use to this transaction before provider
// input exists. Recovery requires a commitment to the original review.
func (m *NativeTransactions) BeginRegistration(trust Trust, profile security.CurrentProfile, clientID string, discovery Discovery, purpose security.NativeRegistrationPurpose) (NativeStart, error) {
	if !purpose.Valid() {
		return NativeStart{}, ErrLoginTransaction
	}
	return m.begin(trust, profile, clientID, discovery, purpose)
}

func (m *NativeTransactions) begin(trust Trust, profile security.CurrentProfile, clientID string, discovery Discovery, purpose security.NativeRegistrationPurpose) (NativeStart, error) {
	if m == nil || !trust.Issuer.Enabled || trust.ConfigRevision == 0 || trust.ConfigRevision != trust.Issuer.ConfigRevision || profile.Binding() == "" || profile.Generation != trust.Generation ||
		security.ValidateIssuerConfiguration(trust.Issuer) != nil || discovery.Issuer != trust.Issuer.URL || validateNativeDiscovery(discovery) != nil {
		return NativeStart{}, ErrLoginTransaction
	}
	client, ok := trust.Issuer.NativeClient(clientID)
	if !ok {
		return NativeStart{}, ErrLoginTransaction
	}
	discovery.ResponseTypes = slices.Clone(discovery.ResponseTypes)
	discovery.CodeChallengeMethods = slices.Clone(discovery.CodeChallengeMethods)
	values := [4]string{}
	for n := range values {
		var err error
		values[n], err = randomLoginValue()
		if err != nil {
			return NativeStart{}, err
		}
	}
	start := NativeStart{AttemptID: values[0], CompletionSecret: values[1], Affinity: m.affinity, Kind: client.Kind, ClientProfileID: client.ID, ClientID: client.ClientID, ServiceOrigin: client.ServiceOrigin, RedirectURI: client.RedirectURI}
	if client.Kind != security.GoogleIDToken {
		start.State, start.Nonce = m.affinity+"."+values[2], values[3]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, now, err := m.currentLocked()
	if err != nil {
		return NativeStart{}, err
	}
	if len(m.pending) >= MaxLoginTransactions {
		return NativeStart{}, ErrNativeCapacity
	}
	id := sha256.Sum256([]byte(start.AttemptID))
	if _, exists := m.pending[id]; exists {
		return NativeStart{}, ErrLoginTransaction
	}
	start.ExpiresAt = now.Add(security.NativeLoginLifetime)
	m.pending[id] = &nativeTransaction{trust: trust.detached(), profile: profile, client: client, discovery: discovery, proof: sha256.Sum256([]byte(start.CompletionSecret)), state: sha256.Sum256([]byte(start.State)), nonce: start.Nonce, createdAt: now, expiresAt: start.ExpiresAt, registration: purpose}
	return start, nil
}

// Lookup accepts only this process's optional routing hint. The hint grants no
// authority: the live transaction and private completion proof remain required.
func (m *NativeTransactions) Lookup(id, proof, affinity string) (NativeAttempt, error) {
	if m == nil || affinity != "" && affinity != m.affinity || !nativeRandomValue(id) || !nativeRandomValue(proof) {
		return NativeAttempt{}, ErrLoginTransaction
	}
	key, secret := sha256.Sum256([]byte(id)), sha256.Sum256([]byte(proof))
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.currentLocked(); err != nil {
		return NativeAttempt{}, err
	}
	t := m.pending[key]
	if t == nil || subtle.ConstantTimeCompare(secret[:], t.proof[:]) != 1 {
		return NativeAttempt{}, ErrLoginTransaction
	}
	return NativeAttempt{owner: m, id: key, transaction: t}, nil
}

// CallbackAttempt never accepts completion secrets in browser input. State only
// selects the pending web transaction; verified token/code commitments must be
// staged before the app can use its separate private completion proof.
func (m *NativeTransactions) CallbackAttempt(profileID, state string) (NativeAttempt, error) {
	if m == nil || !m.validState(state) || len(profileID) > 64 {
		return NativeAttempt{}, ErrLoginTransaction
	}
	wanted := sha256.Sum256([]byte(state))
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.currentLocked(); err != nil {
		return NativeAttempt{}, err
	}
	for id, t := range m.pending {
		if t.client.Kind == security.AppleAndroidWeb && t.client.ID == profileID && !t.staged && subtle.ConstantTimeCompare(wanted[:], t.state[:]) == 1 {
			return NativeAttempt{owner: m, id: id, transaction: t}, nil
		}
	}
	return NativeAttempt{}, ErrLoginTransaction
}

func (m *NativeTransactions) StageCallback(a NativeAttempt, c NativeProviderCredential) (string, error) {
	if m == nil {
		return "", ErrLoginTransaction
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.currentLocked(); err != nil {
		return "", err
	}
	t := m.pending[a.id]
	if a.owner != m || t == nil || t != a.transaction || t.staged || t.client.Kind != security.AppleAndroidWeb || !c.matches(a) {
		return "", ErrLoginTransaction
	}
	t.staged = true
	t.stagedToken, t.stagedCode = evidenceDigest("credential", c.idToken), evidenceDigest("authorization-code", c.code)
	return t.client.AndroidPackage, nil
}

// Consume precedes Apple's one code exchange. Failed exchanges and lost output
// require a new attempt; no subsequent call can recover raw session material.
func (m *NativeTransactions) Consume(a NativeAttempt, c NativeProviderCredential) (NativeCompletion, error) {
	if m == nil {
		return NativeCompletion{}, ErrLoginTransaction
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, now, err := m.currentLocked()
	if err != nil {
		return NativeCompletion{}, err
	}
	t := m.pending[a.id]
	if a.owner != m || t == nil || t != a.transaction || !c.matches(a) {
		return NativeCompletion{}, ErrLoginTransaction
	}
	if t.client.Kind == security.AppleAndroidWeb && (!t.staged || t.stagedToken != evidenceDigest("credential", c.idToken) || t.stagedCode != evidenceDigest("authorization-code", c.code)) {
		return NativeCompletion{}, ErrLoginTransaction
	}
	delete(m.pending, a.id)
	return NativeCompletion{attempt: a, credential: c, consumedAt: now, exchangeStarted: &atomic.Bool{}}, nil
}

// Close invalidates pending work; it owns no workers to join and no durable data.
func (m *NativeTransactions) Close() {
	if m != nil {
		m.mu.Lock()
		m.closed = true
		clear(m.pending)
		m.mu.Unlock()
	}
}
