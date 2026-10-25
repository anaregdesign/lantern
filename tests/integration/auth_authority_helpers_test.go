package integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// This fixture exercises the certified production public handler composition
// with independent original provisioning and the selected native time build.
type oidcControlWireFixture struct {
	client         graphv1connect.LanternSecurityServiceClient
	server         *httptest.Server
	provider       *httptest.Server
	private        ed25519.PrivateKey
	mu             sync.Mutex
	fetches        atomic.Int64
	codes          map[string]oidcWireCode
	graph          *graphcache.GraphCache[string, *pb.Vertex]
	data           graphv1connect.LanternServiceClient
	changes        graphv1connect.LanternChangeServiceClient
	dataRuntime    *service.ServingRuntime
	runtime        *provider.SecurityRuntime
	publicCA       string
	accessIssuedAt time.Time
}

type oidcWireCode struct {
	nonce, challenge, subject string
	wrongNonce                bool
	authTime                  string
}

// Shared local HTTPS issuer; native current tests use its real wall only for token issuance.
func newOIDCWireIssuer(t *testing.T) *oidcControlWireFixture {
	t.Helper()
	// Model one already-issued access credential per subject. Its signed start
	// predates the native interval, and repeated calls reuse its exact evidence.
	// Code/reauthentication issuance below remains fresh and independently paced.
	f := &oidcControlWireFixture{codes: make(map[string]oidcWireCode), accessIssuedAt: time.Now().UTC().Add(-time.Minute)}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.private = private
	f.provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("metadata fetch forwarded credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.provider.URL, "authorization_endpoint": f.provider.URL + "/authorize", "token_endpoint": f.provider.URL + "/token", "jwks_uri": f.provider.URL + "/jwks", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "kid": "key", "alg": "EdDSA", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)}}})
		case "/token":
			if r.Method != http.MethodPost || r.ParseForm() != nil {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if r.Form.Get("grant_type") == "client_credentials" {
				// Fixture issuance contract: this OAuth client cannot select or
				// impersonate a human subject, and never receives an ID token.
				if r.Form.Get("client_id") != "wire-worker" || r.Form.Get("requested_sub") != "" {
					http.Error(w, "invalid client profile", http.StatusBadRequest)
					return
				}
				now := f.clock()
				claims := map[string]any{"iss": f.provider.URL, "sub": "oauth-client:wire-worker", "aud": "api", "client_id": "wire-worker", "jti": "client-wire", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "auth_time": now.Unix()}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": f.signedToken(claims, "at+jwt"), "token_type": "Bearer"})
				return
			}
			f.mu.Lock()
			code, known := f.codes[r.Form.Get("code")]
			delete(f.codes, r.Form.Get("code"))
			f.mu.Unlock()
			challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			callback := sha256.Sum256([]byte(f.provider.URL))
			if !known || strings.HasPrefix(code.subject, "oauth-client:") || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != "admin" || r.Form.Get("redirect_uri") != fmt.Sprintf("https://admin.example/auth/callback/%x", callback) || base64.RawURLEncoding.EncodeToString(challenge[:]) != code.challenge {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			now := f.clock()
			nonce := code.nonce
			if code.wrongNonce {
				nonce = "wrong-nonce"
			}
			accessHash := sha512.Sum512([]byte("provider-private-access-token"))
			claims := map[string]any{"iss": f.provider.URL, "sub": code.subject, "aud": "admin", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "auth_time": now.Unix(), "nonce": nonce, "at_hash": base64.RawURLEncoding.EncodeToString(accessHash[:32])}
			switch code.authTime {
			case "missing":
				delete(claims, "auth_time")
			case "stale":
				claims["auth_time"] = now.Add(-6 * time.Minute).Unix()
			case "future":
				claims["auth_time"] = now.Add(time.Second).Unix()
			case "contradictory":
				claims["iat"] = now.Add(-2 * time.Minute).Unix()
				claims["auth_time"] = now.Add(-time.Minute).Unix()
			case "null":
				claims["auth_time"] = nil
			}
			id := f.signedToken(claims, "JWT")
			_ = json.NewEncoder(w).Encode(map[string]any{"id_token": id, "access_token": "provider-private-access-token", "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.provider.Close)
	return f
}

func newOIDCControlWireFixture(t *testing.T, traversalLimits ...service.TraversalLimits) *oidcControlWireFixture {
	return newOIDCControlWireFixtureConfigured(t, nil, traversalLimits...)
}

func newOIDCControlWireFixtureConfigured(t *testing.T, configure func(*oidcFixtureBootstrap), traversalLimits ...service.TraversalLimits) *oidcControlWireFixture {
	return newOIDCControlWireFixtureOptions(t, configure, false, traversalLimits...)
}

func newOIDCControlWireFixtureOptions(t *testing.T, configure func(*oidcFixtureBootstrap), durableReceipts bool, traversalLimits ...service.TraversalLimits) *oidcControlWireFixture {
	return newAuthorityWireFixture(t, configure, durableReceipts, false, traversalLimits...)
}

func newAuthorityWireFixture(t *testing.T, configure func(*oidcFixtureBootstrap), durableReceipts, publicTLS bool, traversalLimits ...service.TraversalLimits) *oidcControlWireFixture {
	t.Helper()
	if os.Getenv("LANTERN_CURRENT_PUBLIC_GATE") != "1" {
		t.Skip("current-authority wire case requires the coordinated functional-validation/configured-source-integration campaign")
	}
	f := newOIDCWireIssuer(t)
	root, resolveErr := filepath.EvalSymlinks(t.TempDir())
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	dir := root
	bootstrap := oidcFixtureBootstrap{AdminSubjects: []string{"admin", "other"}, HumanSubjectNamespaceQualified: true}
	if configure != nil {
		configure(&bootstrap)
	}
	if bootstrap.Issuer == nil {
		bootstrap.Issuer = newAuthorityWireSeedIssuer(f.provider.URL, bootstrap.HumanSubjectNamespaceQualified)
	}
	raw, encodeErr := json.Marshal(bootstrap)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	bootstrapFile := filepath.Join(dir, "bootstrap-input.json")
	if err := createPrivateTestFile(bootstrapFile, raw); err != nil {
		t.Fatal(err)
	}
	exportAuthorityWireFixture(t, dir, f.provider.URL, 1, bootstrapFile)
	roots := filepath.Join(dir, "provider.pem")
	if err := createPrivateTestFile(roots, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.provider.Certificate().Raw})); err != nil {
		t.Fatal(err)
	}
	configureSecurityInput := func(input map[string]any) {
		input["issuers"] = []map[string]any{bootstrap.Issuer.currentInput()}
	}
	config := loadAuthorityWireSecurityConfig(t, dir, 1, f.provider.URL, roots, configureSecurityInput)
	configureGraph := func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		graph.EnablePrefixIndex(func(key string) string { return key })
		graph.EnableSearchIndex(func(key string, vertex *pb.Vertex) search.Document {
			return search.Fields{{ID: search.FieldKey, Text: strings.TrimPrefix(key, "data:")}, {ID: search.FieldValue, Text: vertex.GetString_()}}
		}, strings.Compare)
		return nil
	}
	var data *service.ServingRuntime
	var err error
	if durableReceipts {
		durable := durableReceiptWireConfig(filepath.Join(dir, "receipts.wal"), hlc.NodeID{8})
		durable.NamespaceFormat = "namespaced-v1"
		durable.ConfigureGraph = configureGraph
		data, err = service.CreateDurableReceiptWALServingRuntime(durable)
	} else {
		graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
		if err := configureGraph(graph); err != nil {
			t.Fatal(err)
		}
		data, err = service.NewGraphOnlyServingRuntime(graph, mutationlog.New(mutationlog.Options{Capacity: 16}), hlc.New(hlc.NodeID{8}, hlc.Options{}), "namespaced-v1")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	f.dataRuntime = data
	f.graph = data.GraphCache()
	if _, err := provider.InitializeSecurity(t.Context(), config); err != nil {
		reportAuthorityStartupFailure(t, err)
		t.Fatal("explicit original authority initialization", err)
	}
	// Supply the same declared operator intent again for ordinary startup.
	config = loadAuthorityWireSecurityConfig(t, dir, 1, f.provider.URL, roots, configureSecurityInput)
	runtime, closeRuntime, err := provider.NewSecurityRuntime(t.Context(), config, data)
	if err != nil {
		reportAuthorityStartupFailure(t, err)
		t.Fatal(err)
	}
	t.Cleanup(closeRuntime)
	f.runtime = runtime
	dataService := data.NewLanternService(nil).WithSearchLimits(service.SearchLimits{Enabled: true, PositionsEnabled: true})
	if durableReceipts {
		dataService.WithTombstoneTTL(time.Hour)
	}
	if len(traversalLimits) > 0 {
		dataService.WithTraversalLimits(traversalLimits[0])
	}
	peer, err := data.NewLanternReplicationService(dataService)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := provider.NewRuntimeRestored(data, dataService)
	if err != nil {
		t.Fatal(err)
	}
	limits := provider.NetConfig{MaxRecvMsgBytes: 8 << 20, MaxSendMsgBytes: 8 << 20, MaxConcurrentStreams: 16}
	certified, err := provider.NewRuntimeCertified(data, dataService, peer, restored, limits)
	if err != nil {
		t.Fatal(err)
	}
	if durableReceipts {
		if err := data.CertifyReceiptBackup(dataService, peer); err != nil {
			t.Fatal(err)
		}
		if err := data.ActivatePublicReceipts(dataService, peer); err != nil {
			t.Fatal(err)
		}
	}
	publicCertified, err := provider.NewPublicSecurityCertified(runtime, provider.TLSConfig{}, nil, data, dataService, certified)
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewUnstartedServer(nil)
	listener, err := provider.NewPublicLanternListener(f.server.Listener, limits, provider.TLSConfig{}, provider.ObservabilityConfig{EnableReflection: true}, provider.CORSConfig{}, dataService, runtime,
		provider.ChangeConfig{Enabled: true, Options: service.ChangeServiceOptions{CursorKeys: []service.ChangeCursorKey{{Version: 1, Key: [32]byte{13}}}, CurrentKeyVersion: 1, Heartbeat: 100 * time.Millisecond}},
		nil, nil, nil, nil, nil, provider.NewHealthChecker(), slog.New(slog.NewTextHandler(io.Discard, nil)), publicCertified)
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config = listener.Server()
	var transport *http.Client
	if publicTLS {
		f.server.EnableHTTP2 = true
		f.publicCA = configureSDKServerTLS(t, f.server)
		f.server.StartTLS()
		transport = f.server.Client()
	} else {
		f.server.Start()
		transport = h2cClient()
	}
	t.Cleanup(f.server.Close)
	clientHTTP := &http.Client{Transport: authIngressRoundTripper{transport.Transport}}
	f.client = graphv1connect.NewLanternSecurityServiceClient(clientHTTP, f.server.URL)
	f.data = graphv1connect.NewLanternServiceClient(clientHTTP, f.server.URL)
	f.changes = graphv1connect.NewLanternChangeServiceClient(clientHTTP, f.server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	for !runtime.Ready(ctx) {
		if ctx.Err() != nil {
			t.Fatal("current fixture native time/quorum readiness", ctx.Err())
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Log("authority fixture: original singleton voter and separate origin key; actual selected native time constructor")
	return f
}
func (f *oidcControlWireFixture) clock() time.Time { return time.Now().UTC() }

type authorityStartupRecord struct {
	Attempt          uint64                     `json:"attempt"`
	Stage            string                     `json:"stage"`
	Reason           string                     `json:"reason"`
	RemainingNS      *int64                     `json:"remaining_ns,omitempty"`
	ExchangeContext  decisiontrace.ContextState `json:"exchange_context,omitempty"`
	IncomingContext  decisiontrace.ContextState `json:"incoming_context,omitempty"`
	AnchoringContext decisiontrace.ContextState `json:"anchoring_context,omitempty"`
	OwnerContext     decisiontrace.ContextState `json:"owner_context,omitempty"`
}

func authorityStartupLabel(value string) string {
	switch value {
	case "dns", "random", "dial", "native_send", "send", "read", "native_receive", "packet", "native_binding", "measured",
		"binding", "rtt", "source_bound", "transmit", "receive", "reference", "overflow", "interval", "native_install",
		"elapsed", "age", "anchor_interval", "utc_window", "width", "contradiction", "installed",
		"ok", "read_timeout", "rate", "deny", "source_fault", "install_rejected", "native_terminal":
		return value
	default:
		return "unclassified"
	}
}

func authorityStartupContext(value decisiontrace.ContextState) decisiontrace.ContextState {
	switch value {
	case decisiontrace.ContextLive, decisiontrace.ContextCanceled, decisiontrace.ContextDeadlineExceeded, decisiontrace.ContextOtherError:
		return value
	default:
		return decisiontrace.ContextUnobserved
	}
}

func authorityStartupFailureRecords(err error) []authorityStartupRecord {
	var diagnostic interface {
		AuthorityTimeStartupSnapshot() decisiontrace.StartupSnapshot
	}
	if !errors.As(err, &diagnostic) {
		return nil
	}
	snapshot := diagnostic.AuthorityTimeStartupSnapshot()
	records := make([]authorityStartupRecord, 0, 9)
	if int(snapshot.Count) > len(snapshot.Attempts) {
		return []authorityStartupRecord{{Stage: "startup_history", Reason: "invalid_snapshot"}}
	}
	for _, attempt := range snapshot.Attempts[:snapshot.Count] {
		record := authorityStartupRecord{Attempt: attempt.Attempt, Stage: authorityStartupLabel(attempt.Stage), Reason: authorityStartupLabel(attempt.Reason),
			ExchangeContext: authorityStartupContext(attempt.ExchangeContext), IncomingContext: authorityStartupContext(attempt.IncomingContext)}
		if attempt.HasReadBudget {
			remaining := attempt.ReadRemainingNS
			record.RemainingNS = &remaining
		}
		records = append(records, record)
	}
	status := func(reason string, attempt uint64) {
		records = append(records, authorityStartupRecord{Attempt: attempt, Stage: "startup_history", Reason: reason})
	}
	if snapshot.Count == 0 {
		status("no_completed_attempt", snapshot.StartedAttempt)
	}
	if snapshot.Overflow {
		status("overflow", snapshot.PublishedAttempt)
	}
	if snapshot.CompletedAttempt != snapshot.PublishedAttempt {
		status("publication_pending", snapshot.CompletedAttempt)
	}
	if snapshot.InFlight {
		status("in_flight", snapshot.StartedAttempt)
	}
	if snapshot.OwnerFailed {
		status("owner_failed", snapshot.PublishedAttempt)
	}
	if snapshot.OwnerClosed {
		status("owner_closed", snapshot.PublishedAttempt)
	}
	records = append(records, authorityStartupRecord{Attempt: snapshot.PublishedAttempt, Stage: "startup_wait", Reason: string(authorityStartupContext(snapshot.AnchoringContext)),
		RemainingNS: &snapshot.RemainingNS, AnchoringContext: authorityStartupContext(snapshot.AnchoringContext),
		IncomingContext: authorityStartupContext(snapshot.IncomingContext), OwnerContext: authorityStartupContext(snapshot.OwnerContext)})
	return records
}

func reportAuthorityStartupFailure(t *testing.T, err error) {
	t.Helper()
	if records := authorityStartupFailureRecords(err); len(records) != 0 {
		raw, marshalErr := json.Marshal(records)
		if marshalErr != nil {
			t.Error("startup diagnostic serialization failed")
			return
		}
		t.Log("AUTHORITY_TIME_STARTUP_DIAGNOSTIC " + string(raw))
	}
}

type authorityStartupErrorFixture struct{ snapshot decisiontrace.StartupSnapshot }

func (e authorityStartupErrorFixture) Error() string {
	return "private endpoint and token must stay out of diagnostic objects"
}
func (e authorityStartupErrorFixture) AuthorityTimeStartupSnapshot() decisiontrace.StartupSnapshot {
	return e.snapshot
}

func TestAuthorityStartupFailureRecordAllowlist(t *testing.T) {
	snapshot := decisiontrace.StartupSnapshot{Count: 2, PublishedAttempt: 2, CompletedAttempt: 3, StartedAttempt: 4, InFlight: true, Overflow: true,
		AnchoringContext: decisiontrace.ContextDeadlineExceeded, IncomingContext: decisiontrace.ContextLive, OwnerContext: decisiontrace.ContextLive}
	snapshot.Attempts[0] = decisiontrace.StartupAttempt{Attempt: 1, Stage: "read", Reason: "read_timeout", HasReadBudget: true, ReadRemainingNS: 0,
		ExchangeContext: decisiontrace.ContextDeadlineExceeded, IncomingContext: decisiontrace.ContextLive}
	snapshot.Attempts[1] = decisiontrace.StartupAttempt{Attempt: 2, Stage: "private.endpoint", Reason: "private token", IncomingContext: "private"}
	records := authorityStartupFailureRecords(fmt.Errorf("outer: %w", authorityStartupErrorFixture{snapshot}))
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "endpoint") || strings.Contains(string(raw), "token") {
		t.Fatal("startup reporter forwarded arbitrary diagnostic text")
	}
	var decoded []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, record := range decoded {
		for key := range record {
			switch key {
			case "stage", "reason", "attempt", "remaining_ns", "exchange_context", "incoming_context", "anchoring_context", "owner_context":
			default:
				t.Fatal("startup reporter exceeded the public field allowlist")
			}
		}
	}
	if string(decoded[0]["remaining_ns"]) != "0" || records[1].Stage != "unclassified" || records[1].Reason != "unclassified" || records[1].IncomingContext != decisiontrace.ContextUnobserved {
		t.Fatal("expired budget or unrecognized labels became misleading")
	}
	if len(authorityStartupFailureRecords(errors.New("ordinary startup failure"))) != 0 {
		t.Fatal("ordinary failure acquired invented time observations")
	}
}

func (f *oidcControlWireFixture) token(t *testing.T, subject string, mutate func(map[string]any)) string {
	t.Helper()
	now := f.accessIssuedAt
	claims := map[string]any{"iss": f.provider.URL, "sub": subject, "aud": "api", "client_id": "client", "jti": "wire", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "auth_time": now.Unix()}
	if mutate != nil {
		mutate(claims)
	}
	return f.signedToken(claims, "at+jwt")
}
func (f *oidcControlWireFixture) signedToken(claims map[string]any, typ string) string {
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "kid": "key", "typ": typ})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.private, []byte(input)))
}
func securityWireRequest[T any](token string, message *T) *connect.Request[T] {
	req := connect.NewRequest(message)
	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}
	return req
}

type oidcFixtureBootstrap struct {
	Issuer                         *authorityWireSeedIssuer `json:"issuer,omitempty"`
	AdminSubjects                  []string                 `json:"admin_subjects"`
	HumanSubjectNamespaceQualified bool                     `json:"human_subject_namespace_qualified"`
	Roles                          json.RawMessage          `json:"roles,omitempty"`
	Machines                       json.RawMessage          `json:"machines,omitempty"`
}

func exportAuthorityWireFixture(t *testing.T, dir, issuer string, voters int, bootstrap string) {
	t.Helper()
	var command *exec.Cmd
	if binary := os.Getenv("LANTERN_CURRENT_FIXTURE_EXPORTER"); binary != "" {
		command = exec.CommandContext(t.Context(), binary, "-test.run=^TestCurrentProvisioningExportPublicFixture$", "-test.v")
	} else {
		command = exec.CommandContext(t.Context(), "go", "test", "./server/internal/security", "-run", "^TestCurrentProvisioningExportPublicFixture$", "-count=1", "-v")
		command.Dir = filepath.Join("..", "..")
	}
	command.Env = append(os.Environ(), "LANTERN_CURRENT_FIXTURE_DIR="+dir, "LANTERN_CURRENT_FIXTURE_ISSUER="+issuer,
		"LANTERN_CURRENT_FIXTURE_VOTERS="+strconv.Itoa(voters), "LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE="+bootstrap,
		"LANTERN_CURRENT_FIXTURE_QUERY_ROLES_FILE=", "LANTERN_CURRENT_FIXTURE_HOSTS=", "LANTERN_NATIVE_FIXTURE_SERVE=",
		"LANTERN_CURRENT_MEMBERSHIP_GATE=", "LANTERN_CURRENT_MEMBERSHIP_CONTAINER=", "LANTERN_CURRENT_DATA_HA=")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("independent original provisioning: %v\n%s", err, output)
	}
	prepareAuthorityWireSecurityMaterial(t, dir)
}

func prepareAuthorityWireReview(ctx context.Context, control graphv1connect.LanternSecurityServiceClient, token string, version *pb.SecurityVersion, changes []*pb.SecurityChange) (*pb.PrepareSecurityChangesResponse, error) {
	if version == nil || version.CurrentProfile == nil || version.CurrentCut == nil {
		return nil, errors.New("full authority profile and semantic cut required")
	}
	review := &pb.CurrentSecurityReview{Profile: version.CurrentProfile, ExpectedCut: version.CurrentCut, Changes: changes}
	response, err := control.PrepareSecurityChanges(ctx, securityWireRequest(token, &pb.PrepareSecurityChangesRequest{CurrentReview: review}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// Data-case setup performs the real public review and Apply. It never maps a
// scalar revision, manufactures an original ID or approves a high-impact change.
func (f *oidcControlWireFixture) prepareDataChanges(t *testing.T, ctx context.Context, token string, changes []*pb.SecurityChange) (*pb.CurrentSecurityReview, error) {
	t.Helper()
	principal, err := f.client.GetCurrentPrincipal(ctx, securityWireRequest(token, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		return nil, err
	}
	prepared, err := prepareAuthorityWireReview(ctx, f.client, token, principal.Msg.Version, changes)
	if err != nil {
		return nil, err
	}
	if prepared.Requirement != pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY {
		return nil, errors.New("data setup unexpectedly requires per-operation reauthentication")
	}
	return prepared.CurrentReview, nil
}

func (f *oidcControlWireFixture) waitForAppliedAuthority(t *testing.T, parent context.Context, result *pb.CurrentSecurityChangeResult) error {
	t.Helper()
	if result == nil || result.Progress != pb.CurrentSecurityProgress_CURRENT_SECURITY_PROGRESS_APPLIED || result.Original == nil || result.Original.Disposition != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
		return errors.New("public Apply did not prove an applied original")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	for {
		capabilities, err := f.client.GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
		if err != nil {
			return err
		}
		if capabilities.Msg.Ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (f *oidcControlWireFixture) applyChanges(t *testing.T, ctx context.Context, token string, changes []*pb.SecurityChange) (*connect.Response[pb.ApplySecurityChangesResponse], error) {
	t.Helper()
	review, err := f.prepareDataChanges(t, ctx, token, changes)
	if err != nil {
		return nil, err
	}
	response, err := f.client.ApplySecurityChanges(ctx, securityWireRequest(token, &pb.ApplySecurityChangesRequest{CurrentReview: review}))
	if err == nil {
		err = f.waitForAppliedAuthority(t, ctx, response.Msg.CurrentResult)
	}
	return response, err
}

func (f *oidcControlWireFixture) applyChange(t *testing.T, ctx context.Context, token string, change *pb.SecurityChange) (*connect.Response[pb.ApplySecurityChangeResponse], error) {
	t.Helper()
	review, err := f.prepareDataChanges(t, ctx, token, []*pb.SecurityChange{change})
	if err != nil {
		return nil, err
	}
	response, err := f.client.ApplySecurityChange(ctx, securityWireRequest(token, &pb.ApplySecurityChangeRequest{CurrentReview: review}))
	if err == nil {
		err = f.waitForAppliedAuthority(t, ctx, response.Msg.CurrentResult)
	}
	return response, err
}
