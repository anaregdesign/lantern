package integration_test

// This additional, explicitly selected proof renders the built Admin in an
// actual Chromium process. Ordinary F/Q runs acquire no browser dependency.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const currentAdminBrowserOrigin = "https://admin.example"
const currentAdminBrowserLimit = 1 << 20

var errCurrentAdminBrowserResponseLost = errors.New("completed fixture Apply response withheld")

type currentAdminBrowserScenario string

const (
	currentAdminBrowserLog             currentAdminBrowserScenario = "log"
	currentAdminBrowserOperationalSlow currentAdminBrowserScenario = "operational-slow-rpc"
)

func parseCurrentAdminBrowserScenario(value string) (currentAdminBrowserScenario, error) {
	switch value {
	case "", string(currentAdminBrowserLog):
		return currentAdminBrowserLog, nil
	case string(currentAdminBrowserOperationalSlow):
		return currentAdminBrowserOperationalSlow, nil
	default:
		return "", errors.New("unsupported additional Admin browser scenario")
	}
}

type currentAdminBrowserEvidence struct {
	Schema               int    `json:"schema"`
	Status               string `json:"status"`
	Scenario             string `json:"scenario"`
	Generation           string `json:"generation"`
	Level                int32  `json:"level"`
	Group                int32  `json:"group"`
	ThresholdMS          int64  `json:"thresholdMs"`
	ApplyRequests        int    `json:"applyRequests"`
	StatusRequests       int    `json:"statusRequests"`
	RevokedReadStatus    int    `json:"revokedReadStatus"`
	RevokedPrepareStatus int    `json:"revokedPrepareStatus"`
	OriginalBindingSame  bool   `json:"originalBindingSame"`
}

// The public Playwright owner reports its exact newly launched root process.
// This says nothing about any prior fixture group or every OS descendant.
type currentAdminBrowserLifecycle struct {
	Schema                    int     `json:"schema"`
	Owner                     string  `json:"owner"`
	PID                       int     `json:"pid"`
	RootExitObserved          bool    `json:"rootExitObserved"`
	RootCloseObserved         bool    `json:"rootCloseObserved"`
	ServerCloseCompleted      bool    `json:"serverCloseCompleted"`
	ClientDisconnectCompleted bool    `json:"clientDisconnectCompleted"`
	ExitCode                  *int    `json:"exitCode"`
	SignalCode                *string `json:"signalCode"`
	Binary                    struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"binary"`
	Version string `json:"version"`
}

type currentAdminBrowserNodeBinding struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

func currentAdminBrowserNode(t *testing.T) currentAdminBrowserNodeBinding {
	t.Helper()
	path := os.Getenv("LANTERN_CURRENT_ADMIN_BROWSER_NODE")
	if path == "" {
		var err error
		path, err = exec.LookPath("node")
		if err != nil {
			t.Fatal("installed Node runtime required for Playwright", err)
		}
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(path) {
		t.Fatal("absolute installed Node runtime required", err)
	}
	version, err := exec.CommandContext(t.Context(), path, "--version").Output()
	if err != nil || len(version) > 128 || !bytes.HasPrefix(version, []byte("v")) {
		t.Fatal("installed Node version unavailable", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	return currentAdminBrowserNodeBinding{Path: path, Version: strings.TrimSpace(string(version)), SHA256: hex.EncodeToString(hash.Sum(nil))}
}

type currentAdminBrowserGateway struct {
	mu             sync.Mutex
	scenario       currentAdminBrowserScenario
	applyRequests  int
	prepareCount   int
	statusRequests int
	droppedStatus  int
	droppedBytes   int
	droppedType    string
	review         *pb.CurrentSecurityReview
	committed      bool
	latest         *pb.GetLogSettingsResponse
	operational    *pb.GetOperationalSettingsResponse
	failed         bool
	failureCodes   []string
	readCanceled   int
	highestDesired uint64
}

func (g *currentAdminBrowserGateway) operationalSlow() bool {
	return g.scenario == currentAdminBrowserOperationalSlow
}

func (g *currentAdminBrowserGateway) originalKind() string {
	if g.operationalSlow() {
		return "operational_policy.put"
	}
	return "log_policy.put"
}

func (g *currentAdminBrowserGateway) acceptsReview(review *pb.CurrentSecurityReview) bool {
	if review == nil || len(review.Changes) != 1 {
		return false
	}
	if g.operationalSlow() {
		change := review.Changes[0].GetPutOperationalPolicy()
		return change != nil && change.Version == 1 && change.Values.GetSlowRpc() != nil && change.Values.GetSlowRpc().ThresholdMs >= 0
	}
	return review.Changes[0].GetPutLogPolicy() != nil
}

// desiredMatches binds the rendered result to this scenario's real native
// response and retained review, rather than inferring application from commit.
func (g *currentAdminBrowserGateway) desiredMatches(result currentAdminBrowserEvidence, generation uint64) bool {
	if g.operationalSlow() {
		if result.Group != int32(pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC) || g.operational == nil || !g.acceptsReview(g.review) {
			return false
		}
		change := g.review.Changes[0].GetPutOperationalPolicy()
		if change.ExpectedGeneration == ^uint64(0) || change.ExpectedGeneration+1 != generation || change.Values.GetSlowRpc().ThresholdMs != result.ThresholdMS {
			return false
		}
		for _, state := range g.operational.Groups {
			if state == nil || state.Group != pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC {
				continue
			}
			return state.Desired != nil && state.Desired.Generation == generation && state.Desired.Values.GetSlowRpc() != nil && state.Desired.Values.GetSlowRpc().ThresholdMs == result.ThresholdMS &&
				state.Effective.GetSlowRpc() != nil && state.Effective.GetSlowRpc().ThresholdMs == result.ThresholdMS && state.AppliedGeneration == generation && state.AppliedCut != nil &&
				state.Adoption == pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_MANAGED && state.Application == pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED &&
				state.ChangeMode == pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT && state.Error == pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_NONE && !state.RestartRequired &&
				len(state.Provenance) == 1 && state.Provenance[0].GetSetting() == pb.OperationalSetting_OPERATIONAL_SETTING_SLOW_RPC_THRESHOLD_MS && state.Provenance[0].GetSource() == pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_MANAGED
		}
		return false
	}
	return g.latest != nil && g.latest.Desired != nil && g.latest.DesiredGeneration == generation && int32(g.latest.Desired.Level) == result.Level && g.latest.Source == pb.LogSettingsSource_LOG_SETTINGS_SOURCE_MANAGED
}

func (g *currentAdminBrowserGateway) failLocked(code string) {
	g.failed = true
	for _, prior := range g.failureCodes {
		if prior == code {
			return
		}
	}
	if len(g.failureCodes) < 8 {
		g.failureCodes = append(g.failureCodes, code)
	}
}

func currentAdminBrowserAPI(method, path string) bool {
	if method == http.MethodGet && path == "/auth/session" {
		return true
	}
	if method != http.MethodPost {
		return false
	}
	switch path {
	case graphv1connect.LanternSecurityServiceGetAuthCapabilitiesProcedure,
		"/browser" + graphv1connect.LanternSecurityServiceGetCurrentPrincipalProcedure,
		"/browser" + graphv1connect.LanternSecurityServiceGetLogSettingsProcedure,
		"/browser" + graphv1connect.LanternSecurityServiceGetOperationalSettingsProcedure,
		"/browser" + graphv1connect.LanternSecurityServicePreviewOperationalPolicyProcedure,
		"/browser" + graphv1connect.LanternSecurityServicePrepareSecurityChangesProcedure,
		"/browser" + graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure,
		"/browser" + graphv1connect.LanternSecurityServiceGetSecurityChangeStatusProcedure:
		return true
	default:
		return false
	}
}

func currentAdminBrowserRewrite(request *httputil.ProxyRequest, target *url.URL) {
	request.SetURL(target)
	request.Out.Host = "admin.example"
	request.Out.Header.Set("X-Forwarded-Proto", "https")
	request.Out.Header.Set("X-Forwarded-Host", "admin.example")
	request.Out.Header.Del("Forwarded")
	// Let the fixed upstream's Go transport own compression and decompression.
	// A forwarded browser header leaves gzip bytes opaque to the JSON observer.
	request.Out.Header.Del("Accept-Encoding")
}

func (g *currentAdminBrowserGateway) request(req *http.Request) error {
	path := req.URL.Path
	if path != "/browser"+graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure && path != "/browser"+graphv1connect.LanternSecurityServiceGetSecurityChangeStatusProcedure && path != "/browser"+graphv1connect.LanternSecurityServicePrepareSecurityChangesProcedure {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, currentAdminBrowserLimit+1))
	_ = req.Body.Close()
	if err != nil || len(raw) > currentAdminBrowserLimit {
		return errors.New("bounded browser request required")
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	g.mu.Lock()
	defer g.mu.Unlock()
	switch path {
	case "/browser" + graphv1connect.LanternSecurityServicePrepareSecurityChangesProcedure:
		g.prepareCount++
		// One UI review, followed only by a denied old-cookie probe.
		if g.prepareCount > 2 {
			g.failLocked("preparation_count")
			return errors.New("unexpected additional browser preparation")
		}
	case "/browser" + graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure:
		g.applyRequests++
		var message pb.ApplySecurityChangesRequest
		if g.applyRequests != 1 || protojson.Unmarshal(raw, &message) != nil || !g.acceptsReview(message.CurrentReview) {
			g.failLocked("apply_request")
			return errors.New("expected exactly one typed browser scenario Apply")
		}
		g.review = proto.Clone(message.CurrentReview).(*pb.CurrentSecurityReview)
	case "/browser" + graphv1connect.LanternSecurityServiceGetSecurityChangeStatusProcedure:
		g.statusRequests++
		var message pb.GetSecurityChangeStatusRequest
		if protojson.Unmarshal(raw, &message) != nil || g.review == nil || !proto.Equal(message.CurrentProfile, g.review.Profile) || !proto.Equal(message.CurrentChangeId, g.review.ChangeId) || !bytes.Equal(message.CurrentIntentDigest, g.review.IntentDigest) {
			g.failLocked("status_request_binding")
			return errors.New("browser status changed the retained original binding")
		}
	}
	return nil
}

func (g *currentAdminBrowserGateway) response(response *http.Response) error {
	path := response.Request.URL.Path
	drop := path == "/browser"+graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure
	observe := drop || (response.StatusCode == http.StatusOK && (path == "/browser"+graphv1connect.LanternSecurityServiceGetLogSettingsProcedure || path == "/browser"+graphv1connect.LanternSecurityServiceGetOperationalSettingsProcedure || path == "/browser"+graphv1connect.LanternSecurityServiceGetSecurityChangeStatusProcedure))
	if !observe {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, currentAdminBrowserLimit+1))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || len(raw) > currentAdminBrowserLimit {
		g.mu.Lock()
		if errors.Is(response.Request.Context().Err(), context.Canceled) {
			g.readCanceled++
		}
		code := "settings_response_incomplete"
		if drop {
			code = "apply_response_incomplete"
		} else if path == "/browser"+graphv1connect.LanternSecurityServiceGetSecurityChangeStatusProcedure {
			code = "status_response_incomplete"
		}
		if len(raw) > currentAdminBrowserLimit {
			code = "response_limit"
		}
		g.failLocked(code)
		g.mu.Unlock()
		return errors.New("complete bounded native response required")
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	g.mu.Lock()
	defer g.mu.Unlock()
	if drop {
		g.droppedStatus, g.droppedBytes = response.StatusCode, len(raw)
		g.droppedType = response.Header.Get("Content-Type")
		if response.StatusCode != http.StatusOK || len(raw) == 0 {
			g.failLocked("completed_apply_response")
		}
		// Native already completed its real response, including output guards.
		// The gateway preserves headers, then closes before emitting its body.
		return errCurrentAdminBrowserResponseLost
	}
	switch path {
	case "/browser" + graphv1connect.LanternSecurityServiceGetLogSettingsProcedure:
		var value pb.GetLogSettingsResponse
		if protojson.Unmarshal(raw, &value) != nil {
			g.failLocked("settings_response_json")
			return errors.New("native settings JSON required")
		}
		g.latest = &value
		if !g.operationalSlow() {
			g.highestDesired = max(g.highestDesired, value.DesiredGeneration)
		}
	case "/browser" + graphv1connect.LanternSecurityServiceGetOperationalSettingsProcedure:
		var value pb.GetOperationalSettingsResponse
		if protojson.Unmarshal(raw, &value) != nil || value.SchemaVersion != 1 || len(value.Groups) != 8 {
			g.failLocked("operational_response_json")
			return errors.New("complete typed native operational settings required")
		}
		seen := make(map[pb.OperationalGroup]bool, 8)
		for _, group := range value.Groups {
			if group == nil || group.Group < pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC || group.Group > pb.OperationalGroup_OPERATIONAL_GROUP_REPLICATION || seen[group.Group] {
				g.failLocked("operational_response_groups")
				return errors.New("all eight native operational groups required")
			}
			seen[group.Group] = true
			if g.operationalSlow() && group.Group == pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC {
				g.highestDesired = max(g.highestDesired, group.GetDesired().GetGeneration())
			}
		}
		g.operational = &value
	default:
		var value pb.GetSecurityChangeStatusResponse
		if protojson.Unmarshal(raw, &value) != nil {
			g.failLocked("status_response_json")
			return errors.New("native original status JSON required")
		}
		result := value.GetCurrentResult()
		if result == nil || g.review == nil || !proto.Equal(result.Profile, g.review.Profile) || !proto.Equal(result.ChangeId, g.review.ChangeId) || !bytes.Equal(result.IntentDigest, g.review.IntentDigest) {
			g.failLocked("status_response_binding")
			return errors.New("native status changed the browser original binding")
		}
		original := result.GetOriginal()
		if original != nil && original.Disposition == pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			if !proto.Equal(original.ChangeId, g.review.ChangeId) || !bytes.Equal(original.IntentDigest, g.review.IntentDigest) || original.Commit == nil || len(original.Items) != 1 || original.Items[0].Kind != g.originalKind() || original.Items[0].Disposition != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
				g.failLocked("original_commit_metadata")
				return errors.New("exact native committed scenario original required")
			}
			g.committed = true
		}
	}
	return nil
}

func (g *currentAdminBrowserGateway) responseError(w http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, errCurrentAdminBrowserResponseLost) {
		g.mu.Lock()
		status, size, contentType := g.droppedStatus, g.droppedBytes, g.droppedType
		g.mu.Unlock()
		// The native response has completed, but no result body reaches the
		// client. Headers prevent a transparent stale-connection POST retry;
		// the nonzero length makes the missing body an indeterminate result.
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.WriteHeader(status)
		if flushErr := http.NewResponseController(w).Flush(); flushErr != nil {
			g.mu.Lock()
			g.failLocked("loss_header_flush")
			g.mu.Unlock()
		}
		panic(http.ErrAbortHandler)
	}
	http.Error(w, "fixture native response unavailable", http.StatusBadGateway)
}

// Only the exact fixture origin may be tunneled. No generic forward proxy, DNS
// override or host trust-store change is needed to keep its registered origin.
func currentAdminBrowserTunnel(w http.ResponseWriter, req *http.Request, target string) {
	if req.Method != http.MethodConnect || req.Host != "admin.example:443" || req.URL.Host != "admin.example:443" || req.URL.RawQuery != "" || req.URL.User != nil {
		http.Error(w, "fixture tunnel target refused", http.StatusForbidden)
		return
	}
	backend, err := net.DialTimeout("tcp4", target, 3*time.Second)
	if err != nil {
		http.Error(w, "fixture gateway unavailable", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = backend.Close()
		http.Error(w, "fixture tunnel unavailable", http.StatusBadGateway)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = backend.Close()
		return
	}
	defer func(connection net.Conn) { _ = connection.Close() }(client)
	defer func(connection net.Conn) { _ = connection.Close() }(backend)
	_ = client.SetDeadline(time.Now().Add(90 * time.Second))
	_ = backend.SetDeadline(time.Now().Add(90 * time.Second))
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil || buffered.Flush() != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(backend, buffered)
		_ = backend.Close()
		close(done)
	}()
	_, _ = io.Copy(client, backend)
	_ = client.Close()
	<-done
}

func currentAdminBrowserTLS(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1995), Subject: pkix.Name{CommonName: "Lantern disposable Admin gateway"}, DNSNames: []string{"admin.example"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return certificate, base64.StdEncoding.EncodeToString(pin[:])
}

func currentAdminBrowserInputs(t *testing.T, root, assets string) (string, string, string) {
	t.Helper()
	status := exec.CommandContext(t.Context(), "git", "status", "--porcelain", "--untracked-files=all")
	status.Dir = root
	if raw, err := status.Output(); err != nil || len(raw) != 0 {
		t.Fatal("additional browser proof requires clean, committed source", err)
	}
	revision := func(name string) string {
		command := exec.CommandContext(t.Context(), "git", "rev-parse", "--verify", name)
		command.Dir = root
		raw, err := command.Output()
		if err != nil {
			t.Fatal("additional browser source binding", err)
		}
		return strings.TrimSpace(string(raw))
	}
	digest := sha256.New()
	if err := filepath.WalkDir(assets, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("built SPA must contain only ordinary files")
		}
		relative, err := filepath.Rel(assets, path)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		content := sha256.New()
		_, copyErr := io.Copy(content, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		_, _ = io.WriteString(digest, filepath.ToSlash(relative)+"\x00"+hex.EncodeToString(content.Sum(nil))+"\n")
		return nil
	}); err != nil {
		t.Fatal("additional browser built SPA binding", err)
	}
	return revision("HEAD"), revision("HEAD^{tree}"), hex.EncodeToString(digest.Sum(nil))
}

type currentAdminBrowserBoundedLog struct {
	file      io.Writer
	remaining int
	truncated bool
}

func (w *currentAdminBrowserBoundedLog) Write(raw []byte) (int, error) {
	n := len(raw)
	if n > w.remaining {
		w.truncated = true
		raw = raw[:w.remaining]
	}
	written, err := w.file.Write(raw)
	w.remaining -= written
	if err != nil {
		return written, err
	}
	return n, nil
}

// Called only by the explicit functional-validation browser hook, after a real
// Code exchange supplied its server-issued cookies. revoke is the existing
// exact-session logout body, invoked once while Chromium retains those cookies.
func runCurrentAdminBrowser(t *testing.T, ctx context.Context, upstream *httptest.Server, transport http.RoundTripper, cookies []*http.Cookie, revoke func()) uint64 {
	t.Helper()
	scenario, err := parseCurrentAdminBrowserScenario(os.Getenv("LANTERN_CURRENT_ADMIN_BROWSER_MODE"))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LANTERN_CURRENT_ADMIN_BROWSER") != "1" || os.Getenv("LANTERN_CURRENT_CONFIGURED_SOURCE_INTEGRATION") != "0" {
		t.Fatal("additional Admin browser proof requires explicit functional-validation opt-in; Q is forbidden")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Fatal("additional Admin browser fixture supports Darwin/Linux only; no browser was started")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	assets := filepath.Join(root, "admin", "build", "client")
	if _, err := os.Stat(filepath.Join(assets, "index.html")); err != nil {
		t.Fatal("additional Admin browser proof requires the current built SPA", err)
	}
	head, tree, assetHash := currentAdminBrowserInputs(t, root, assets)
	evidence := os.Getenv("LANTERN_CURRENT_ADMIN_BROWSER_EVIDENCE")
	if !filepath.IsAbs(evidence) || evidence == root || strings.HasPrefix(evidence, root+string(os.PathSeparator)) {
		t.Fatal("additional Admin browser evidence must be an absolute new directory outside the checkout")
	}
	if err := os.Mkdir(evidence, 0700); err != nil {
		t.Fatal("new additional Admin browser evidence directory", err)
	}
	passed := false
	defer func() {
		if !passed {
			_ = os.WriteFile(filepath.Join(evidence, "fixture-result.json"), []byte("{\"schema\":1,\"status\":\"FAIL\",\"proof\":\"additional-real-admin-browser\"}\n"), 0600)
		}
	}()
	state := &currentAdminBrowserGateway{scenario: scenario}
	target, err := url.Parse(upstream.URL)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.User != nil || target.Path != "" {
		t.Fatal("fixed native upstream required", err)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			currentAdminBrowserRewrite(request, target)
		},
		Transport:      transport,
		ModifyResponse: state.response,
		ErrorHandler:   state.responseError,
	}
	assetServer := http.FileServer(http.Dir(assets))
	gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != "admin.example" || req.URL.RawPath != "" {
			http.Error(w, "fixture origin refused", http.StatusForbidden)
			return
		}
		if currentAdminBrowserAPI(req.Method, req.URL.Path) {
			if req.URL.RawQuery != "" {
				http.Error(w, "fixture RPC query refused", http.StatusBadRequest)
				return
			}
			if err := state.request(req); err != nil {
				http.Error(w, "fixture request contract refused", http.StatusBadRequest)
				return
			}
			proxy.ServeHTTP(w, req)
			return
		}
		if (req.Method != http.MethodGet && req.Method != http.MethodHead) || strings.HasPrefix(req.URL.Path, "/auth/") || strings.HasPrefix(req.URL.Path, "/browser/") || strings.HasPrefix(req.URL.Path, "/graph.") {
			http.NotFound(w, req)
			return
		}
		if req.URL.Path == "/settings" {
			http.ServeFile(w, req, filepath.Join(assets, "index.html"))
			return
		}
		assetServer.ServeHTTP(w, req)
	}))
	certificate, spki := currentAdminBrowserTLS(t)
	gateway.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	gateway.StartTLS()
	defer gateway.Close()
	tunnel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		currentAdminBrowserTunnel(w, req, gateway.Listener.Addr().String())
	}))
	defer tunnel.Close()
	var browserCookies []map[string]any
	seenCookies := make(map[string]bool)
	for _, cookie := range cookies {
		if (cookie.Name != "__Host-lantern-session" && cookie.Name != "__Host-lantern-csrf") || seenCookies[cookie.Name] || cookie.Value == "" || !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode || cookie.Expires.IsZero() {
			t.Fatal("exact Code-issued browser cookies required")
		}
		seenCookies[cookie.Name] = true
		browserCookies = append(browserCookies, map[string]any{"name": cookie.Name, "value": cookie.Value, "url": currentAdminBrowserOrigin, "secure": true, "httpOnly": true, "sameSite": "Strict", "expires": cookie.Expires.Unix()})
	}
	if len(browserCookies) != 2 {
		t.Fatal("exact Code-issued session and CSRF cookies required")
	}
	manifest, err := json.Marshal(map[string]any{"schema": 1, "scenario": scenario, "origin": currentAdminBrowserOrigin, "proxy": tunnel.URL, "spki": spki, "cookies": browserCookies, "evidence": evidence})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), "browser-input.json")
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	clear(manifest)
	node := currentAdminBrowserNode(t)
	buildRoot := filepath.Join(root, "admin", "test-results")
	if err := os.MkdirAll(buildRoot, 0700); err != nil {
		t.Fatal(err)
	}
	bundleDir, err := os.MkdirTemp(buildRoot, "managed-log-native-")
	if err != nil {
		t.Fatal(err)
	}
	defer func(path string) { _ = os.RemoveAll(path) }(bundleDir)
	bundle := filepath.Join(bundleDir, "managed-log-settings-native.mjs")
	buildCtx, stopBuild := context.WithTimeout(ctx, 20*time.Second)
	defer stopBuild()
	build := exec.CommandContext(buildCtx, "bun", "build", "--target=node", "--format=esm", "--packages=external", "--outfile", bundle, filepath.Join(root, "admin", "tests", "e2e", "managed-log-settings-native.ts"))
	build.Dir = filepath.Join(root, "admin")
	buildFile, err := os.OpenFile(filepath.Join(evidence, "driver-build-stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	buildLog := &currentAdminBrowserBoundedLog{file: buildFile, remaining: 16 << 10}
	build.Stdout, build.Stderr = io.Discard, buildLog
	buildErr := build.Run()
	_ = buildFile.Close()
	if buildErr != nil || buildLog.truncated {
		t.Fatal("Node-target browser driver build failed; private diagnostic retained", buildErr)
	}
	bundleBytes, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundleHash := sha256.Sum256(bundleBytes)
	if err := os.WriteFile(filepath.Join(evidence, "driver.mjs"), bundleBytes, 0600); err != nil {
		t.Fatal(err)
	}
	childCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(childCtx, node.Path, bundle)
	// Give the tool owner its ordinary graceful shutdown path before the
	// bounded exec backstop. No process-group inventory or signal is used.
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 8 * time.Second
	command.Dir = filepath.Join(root, "admin")
	command.Env = append(os.Environ(), "LANTERN_CURRENT_ADMIN_BROWSER_MANIFEST="+manifestPath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrFile, err := os.OpenFile(filepath.Join(evidence, "child-stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func(file *os.File) { _ = file.Close() }(stderrFile)
	stderr := &currentAdminBrowserBoundedLog{file: stderrFile, remaining: 64 << 10}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal("additional Admin browser child start", err)
	}
	defer func() {
		_ = stdin.Close()
		cancel()
		if command.ProcessState == nil {
			_ = command.Wait()
		}
	}()
	ready, complete := false, false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 16<<10)
	for scanner.Scan() {
		switch scanner.Text() {
		case "current_admin_browser ready-for-revocation":
			if ready {
				t.Fatal("duplicate browser revocation handoff")
			}
			ready = true
			revoke()
			if _, err := io.WriteString(stdin, "revoked\n"); err != nil {
				t.Fatal("browser revocation handoff", err)
			}
		case "current_admin_browser PASS":
			complete = true
		default:
			t.Fatal("unexpected additional browser proof output")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal("bounded browser proof output", err)
	}
	if err := command.Wait(); err != nil || !ready || !complete || stderr.truncated {
		t.Fatal("additional Admin browser proof failed; private evidence retained", err, "ready", ready, "complete", complete)
	}
	lifecycleBytes, err := os.ReadFile(filepath.Join(evidence, "browser-lifecycle.json"))
	var lifecycle currentAdminBrowserLifecycle
	if err != nil || len(lifecycleBytes) > 16<<10 || json.Unmarshal(lifecycleBytes, &lifecycle) != nil || lifecycle.Schema != 1 || lifecycle.Owner != "playwright" || lifecycle.PID <= 0 || !lifecycle.RootExitObserved || !lifecycle.RootCloseObserved || !lifecycle.ServerCloseCompleted || !lifecycle.ClientDisconnectCompleted || (lifecycle.ExitCode == nil && lifecycle.SignalCode == nil) || !filepath.IsAbs(lifecycle.Binary.Path) || len(lifecycle.Binary.SHA256) != 64 || lifecycle.Version == "" {
		t.Fatal("new Playwright-owned browser root closure evidence required", err)
	}
	raw, err := os.ReadFile(filepath.Join(evidence, "result.json"))
	var result currentAdminBrowserEvidence
	if err != nil || len(raw) > 16<<10 || json.Unmarshal(raw, &result) != nil || result.Schema != 1 || result.Status != "PASS" || result.Scenario != string(scenario) || result.ApplyRequests != 1 || result.StatusRequests < 1 || !result.OriginalBindingSame || (result.RevokedReadStatus != http.StatusUnauthorized && result.RevokedReadStatus != http.StatusForbidden) || (result.RevokedPrepareStatus != http.StatusUnauthorized && result.RevokedPrepareStatus != http.StatusForbidden) {
		t.Fatal("complete additional Admin browser evidence required", err)
	}
	generation, err := strconv.ParseUint(result.Generation, 10, 64)
	state.mu.Lock()
	defer state.mu.Unlock()
	observation := map[string]any{"schema": 1, "scenario": scenario, "failed": state.failed, "failure_codes": append([]string(nil), state.failureCodes...), "read_context_canceled": state.readCanceled, "apply_requests": state.applyRequests, "prepare_requests": state.prepareCount, "status_requests": state.statusRequests, "dropped_status": state.droppedStatus, "dropped_bytes": state.droppedBytes, "committed": state.committed, "has_latest": state.latest != nil, "has_operational": state.operational != nil, "highest_desired_generation": state.highestDesired, "expected_generation": generation, "expected_level": result.Level, "expected_threshold_ms": result.ThresholdMS, "generation_parse_ok": err == nil}
	if state.latest != nil {
		observation["latest_member"] = state.latest.Member
		observation["latest_generation"] = state.latest.DesiredGeneration
		observation["latest_source"] = int32(state.latest.Source)
		if state.latest.Desired != nil {
			observation["latest_level"] = int32(state.latest.Desired.Level)
		}
	}
	observed, observationErr := json.Marshal(observation)
	if observationErr != nil {
		t.Fatal("bounded gateway observation serialization", observationErr)
	}
	if observationErr := os.WriteFile(filepath.Join(evidence, "gateway-observation.json"), append(observed, '\n'), 0600); observationErr != nil {
		t.Fatal("bounded gateway observation persistence", observationErr)
	}
	if err != nil || generation == 0 || state.failed || state.applyRequests != 1 || state.prepareCount != 2 || state.statusRequests < 1 || state.droppedStatus != http.StatusOK || state.droppedBytes == 0 || !state.committed || !state.desiredMatches(result, generation) {
		t.Fatal("native browser proof did not preserve one committed original and current desired state", err)
	}
	finalHead, finalTree, finalAssets := currentAdminBrowserInputs(t, root, assets)
	finalNode := currentAdminBrowserNode(t)
	finalBundle, bundleErr := os.ReadFile(bundle)
	if finalHead != head || finalTree != tree || finalAssets != assetHash || finalNode != node || bundleErr != nil || sha256.Sum256(finalBundle) != bundleHash {
		t.Fatal("additional browser source/assets or bound runtime drifted")
	}
	resultHash := sha256.Sum256(raw)
	lifecycleHash := sha256.Sum256(lifecycleBytes)
	proof, _ := json.Marshal(map[string]any{"schema": 1, "status": "PASS", "proof": "additional-real-admin-browser", "scenario": scenario, "source_head": head, "source_tree": tree, "spa_files_sha256": assetHash, "chromium_sha256": lifecycle.Binary.SHA256, "node": node, "driver_bundle_sha256": hex.EncodeToString(bundleHash[:]), "browser_result_sha256": hex.EncodeToString(resultHash[:]), "browser_lifecycle_sha256": hex.EncodeToString(lifecycleHash[:]), "browser_owner": "playwright-new-root", "browser_root_exit_observed": true, "playwright_server_close_completed": true, "node_child_reaped": true, "apply_requests": state.applyRequests, "prepare_requests": state.prepareCount, "status_requests": state.statusRequests, "completed_response_lost": state.droppedStatus == http.StatusOK, "desired_generation": generation, "revocation_handoffs": 1, "ordinary_f_qualification": false, "configured_source_qualification": false})
	if err := os.WriteFile(filepath.Join(evidence, "fixture-result.json"), append(proof, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	passed = true
	t.Logf("additional_real_admin_browser PASS scenario=%s apply_requests=1 status_requests=%d desired_generation=%d revoked_read=%d revoked_prepare=%d; separate opt-in proof, not ordinary F/Q", scenario, state.statusRequests, generation, result.RevokedReadStatus, result.RevokedPrepareStatus)
	return generation
}

func TestCurrentAdminBrowserFixedTargets(t *testing.T) {
	for _, input := range []struct{ method, path string }{{http.MethodGet, "/auth/session"}, {http.MethodPost, "/browser" + graphv1connect.LanternSecurityServiceGetLogSettingsProcedure}, {http.MethodPost, "/browser" + graphv1connect.LanternSecurityServiceGetOperationalSettingsProcedure}, {http.MethodPost, "/browser" + graphv1connect.LanternSecurityServicePreviewOperationalPolicyProcedure}} {
		if !currentAdminBrowserAPI(input.method, input.path) {
			t.Fatal("required exact browser route missing")
		}
	}
	for _, input := range []struct{ method, path string }{{http.MethodGet, "/auth/logout"}, {http.MethodGet, "/browser" + graphv1connect.LanternSecurityServiceGetLogSettingsProcedure}, {http.MethodGet, "/browser" + graphv1connect.LanternSecurityServiceGetOperationalSettingsProcedure}, {http.MethodGet, "/browser" + graphv1connect.LanternSecurityServicePreviewOperationalPolicyProcedure}, {http.MethodPost, graphv1connect.LanternSecurityServicePreviewOperationalPolicyProcedure}, {http.MethodPost, "/graph.v1.LanternService/PutVertices"}, {http.MethodPost, "/browser/graph.v1.LanternSecurityService/ListIssuers"}, {http.MethodPost, "https://other.example/auth/session"}} {
		if currentAdminBrowserAPI(input.method, input.path) {
			t.Fatal("unexpected gateway route admitted", input)
		}
	}
	for _, target := range []string{"other.example:443", "admin.example:444", "admin.example", "127.0.0.1:443"} {
		request := httptest.NewRequest(http.MethodConnect, "http://"+target, nil)
		request.Host = target
		response := httptest.NewRecorder()
		currentAdminBrowserTunnel(response, request, "127.0.0.1:1")
		if response.Code != http.StatusForbidden {
			t.Fatal("unexpected CONNECT target admitted", target)
		}
	}
}

func TestCurrentAdminBrowserOperationalScenarioBinding(t *testing.T) {
	for _, value := range []string{"", "log", "operational-slow-rpc"} {
		mode, err := parseCurrentAdminBrowserScenario(value)
		if err != nil || (value == "operational-slow-rpc") != (mode == currentAdminBrowserOperationalSlow) {
			t.Fatal("closed browser scenario rejected", value, mode, err)
		}
	}
	for _, value := range []string{"slow", "rate", "https://other.example", "../driver.ts"} {
		if _, err := parseCurrentAdminBrowserScenario(value); err == nil {
			t.Fatal("unknown browser scenario accepted", value)
		}
	}
	values := &pb.OperationalPolicyValues{Group: &pb.OperationalPolicyValues_SlowRpc{SlowRpc: &pb.OperationalSlowRPCValues{ThresholdMs: 37}}}
	review := &pb.CurrentSecurityReview{IntentDigest: bytes.Repeat([]byte{7}, 32), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutOperationalPolicy{PutOperationalPolicy: &pb.OperationalPolicyChange{Version: 1, ExpectedGeneration: 7, Values: values}}}}}
	logReview := &pb.CurrentSecurityReview{Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutLogPolicy{PutLogPolicy: &pb.ManagedLogPolicy{Version: 1, Level: pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO}}}}}
	if (&currentAdminBrowserGateway{}).acceptsReview(review) || (&currentAdminBrowserGateway{scenario: currentAdminBrowserOperationalSlow}).acceptsReview(logReview) {
		t.Fatal("browser scenario admitted the other policy type")
	}
	state := &currentAdminBrowserGateway{scenario: currentAdminBrowserOperationalSlow}
	raw, err := protojson.Marshal(&pb.ApplySecurityChangesRequest{CurrentReview: review})
	if err != nil {
		t.Fatal(err)
	}
	applyPath := "/browser" + graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure
	if err := state.request(httptest.NewRequest(http.MethodPost, applyPath, bytes.NewReader(raw))); err != nil || !proto.Equal(state.review, review) {
		t.Fatal("typed original was not retained", err)
	}
	if err := state.request(httptest.NewRequest(http.MethodPost, applyPath, bytes.NewReader(raw))); err == nil {
		t.Fatal("second Apply dispatch accepted")
	}
	settings := &pb.GetOperationalSettingsResponse{SchemaVersion: 1, Member: 1}
	for group := pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC; group <= pb.OperationalGroup_OPERATIONAL_GROUP_REPLICATION; group++ {
		settings.Groups = append(settings.Groups, &pb.OperationalGroupSettings{Group: group})
	}
	settings.Groups[0] = &pb.OperationalGroupSettings{Group: pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC,
		Desired: &pb.ManagedOperationalPolicy{Version: 1, Generation: 8, Values: values}, Effective: values,
		Provenance:        []*pb.OperationalSettingProvenance{{Setting: pb.OperationalSetting_OPERATIONAL_SETTING_SLOW_RPC_THRESHOLD_MS, Source: pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_MANAGED}},
		AppliedGeneration: 8, AppliedCut: &pb.CurrentSemanticCut{Sequence: 9}, ChangeMode: pb.OperationalSettingsChangeMode_OPERATIONAL_SETTINGS_CHANGE_MODE_HOT,
		Adoption: pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_MANAGED, Application: pb.OperationalSettingsApplicationState_OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED, Error: pb.OperationalSettingsError_OPERATIONAL_SETTINGS_ERROR_NONE}
	result := currentAdminBrowserEvidence{Group: int32(pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC), ThresholdMS: 37}
	settingsPath := "/browser" + graphv1connect.LanternSecurityServiceGetOperationalSettingsProcedure
	response := func(value proto.Message, path string) *http.Response {
		t.Helper()
		raw, err := protojson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw)), Request: httptest.NewRequest(http.MethodPost, path, nil)}
	}
	observed := &currentAdminBrowserGateway{scenario: currentAdminBrowserOperationalSlow, review: review}
	if err := observed.response(response(settings, settingsPath)); err != nil || observed.operational == nil || observed.latest != nil || observed.highestDesired != 8 || !observed.desiredMatches(result, 8) {
		t.Fatal("typed operational observation failed", err)
	}
	// The page also reads logging. That response must never settle the selected
	// operational original or replace its typed effective observation.
	logSettings := &pb.GetLogSettingsResponse{DesiredGeneration: 99, Desired: &pb.ManagedLogPolicy{Version: 1, Level: pb.ManagedLogLevel_MANAGED_LOG_LEVEL_WARN}}
	if err := observed.response(response(logSettings, "/browser"+graphv1connect.LanternSecurityServiceGetLogSettingsProcedure)); err != nil || observed.highestDesired != 8 || !observed.desiredMatches(result, 8) {
		t.Fatal("unselected logging response changed operational observation", err)
	}
	for name, change := range map[string]func(*pb.OperationalGroupSettings){
		"pending": func(s *pb.OperationalGroupSettings) { s.AppliedGeneration-- },
		"source": func(s *pb.OperationalGroupSettings) {
			s.Provenance[0].Source = pb.OperationalSettingsSource_OPERATIONAL_SETTINGS_SOURCE_ENVIRONMENT
		},
		"no cut":      func(s *pb.OperationalGroupSettings) { s.AppliedCut = nil },
		"restart":     func(s *pb.OperationalGroupSettings) { s.RestartRequired = true },
		"wrong value": func(s *pb.OperationalGroupSettings) { s.Effective.GetSlowRpc().ThresholdMs++ },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := proto.Clone(settings).(*pb.GetOperationalSettingsResponse)
			change(wrong.Groups[0])
			probe := &currentAdminBrowserGateway{scenario: currentAdminBrowserOperationalSlow, review: review, operational: wrong}
			if probe.desiredMatches(result, 8) {
				t.Fatal("commit incorrectly implied actual application")
			}
		})
	}
	for _, kind := range []string{"operational_policy.put", "log_policy.put"} {
		probe := &currentAdminBrowserGateway{scenario: currentAdminBrowserOperationalSlow, review: review}
		status := &pb.GetSecurityChangeStatusResponse{CurrentResult: &pb.CurrentSecurityChangeResult{IntentDigest: review.IntentDigest, Original: &pb.CurrentSecurityOriginalOutcome{IntentDigest: review.IntentDigest, Commit: &pb.CurrentControlCommit{Slot: 1}, Disposition: pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED, Items: []*pb.CurrentSecurityItemOutcome{{Kind: kind, Disposition: pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED}}}}}
		err := probe.response(response(status, "/browser"+graphv1connect.LanternSecurityServiceGetSecurityChangeStatusProcedure))
		if (err == nil && probe.committed) != (kind == "operational_policy.put") {
			t.Fatal("native original kind was not scenario-bound", kind, err)
		}
	}
	for _, mutate := range []func(*pb.GetOperationalSettingsResponse){
		func(s *pb.GetOperationalSettingsResponse) { s.Groups = s.Groups[:7] },
		func(s *pb.GetOperationalSettingsResponse) { s.Groups[7].Group = s.Groups[0].Group },
	} {
		wrong := proto.Clone(settings).(*pb.GetOperationalSettingsResponse)
		mutate(wrong)
		if err := (&currentAdminBrowserGateway{}).response(response(wrong, settingsPath)); err == nil {
			t.Fatal("incomplete operational catalog admitted")
		}
	}
}

func TestCurrentAdminBrowserCompressedJSONObservation(t *testing.T) {
	digest := func(value byte) []byte { return bytes.Repeat([]byte{value}, 32) }
	profile := &pb.CurrentAuthorityProfile{Version: 2, Domain: digest(1), Cohort: digest(2), Generation: bytes.Repeat([]byte{3}, 16), Protocol: digest(4), TimeProfile: digest(5), Membership: digest(6), Configuration: digest(7)}
	cut := &pb.CurrentSemanticCut{Version: 1, Domain: profile.Domain, Cohort: profile.Cohort, Generation: profile.Generation, Sequence: 9, Previous: digest(8), Projection: digest(9), Frontier: digest(10), Fences: digest(11), Policy: digest(12)}
	want := &pb.GetLogSettingsResponse{SchemaVersion: 1, Member: 1, EffectiveLevel: pb.ManagedLogLevel_MANAGED_LOG_LEVEL_WARN, Source: pb.LogSettingsSource_LOG_SETTINGS_SOURCE_MANAGED, Desired: &pb.ManagedLogPolicy{Version: 1, Level: pb.ManagedLogLevel_MANAGED_LOG_LEVEL_WARN}, DesiredGeneration: 2, AppliedGeneration: 2, Adoption: pb.LogPolicyAdoptionState_LOG_POLICY_ADOPTION_STATE_MANAGED, Application: pb.LogSettingsApplicationState_LOG_SETTINGS_APPLICATION_STATE_APPLIED, Error: pb.LogSettingsError_LOG_SETTINGS_ERROR_NONE, Version: &pb.SecurityVersion{CurrentProfile: profile, CurrentCut: cut, AdmissionBinding: digest(13)}, AppliedCut: cut, Preparation: &pb.LogPolicyPreparationReference{ChangeId: &pb.CurrentSecurityChangeID{Version: 1, Domain: profile.Domain, Cohort: profile.Cohort, Namespace: 11, Nonce: bytes.Repeat([]byte{14}, 16)}, IntentDigest: digest(15), ActorDigest: digest(16)}}
	raw, err := protojson.Marshal(want)
	if err != nil || len(raw) <= 1024 {
		t.Fatal("typed current settings must exercise normal response compression", len(raw), err)
	}
	path := "/browser" + graphv1connect.LanternSecurityServiceGetLogSettingsProcedure
	handler := connect.NewUnaryHandler(path, func(context.Context, *connect.Request[pb.GetLogSettingsRequest]) (*connect.Response[pb.GetLogSettingsResponse], error) {
		return connect.NewResponse(want), nil
	}, connect.WithCompressMinBytes(1024))
	backend := httptest.NewServer(handler)
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		forwarded bool
		status    int
	}{
		{"forwarded browser encoding reproduces fixture failure", true, http.StatusBadGateway},
		{"transport owns encoding and preserves typed settings", false, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := http.DefaultTransport.(*http.Transport).Clone()
			defer transport.CloseIdleConnections()
			state := &currentAdminBrowserGateway{}
			var contentType, contentEncoding string
			var uncompressed bool
			proxy := &httputil.ReverseProxy{
				Rewrite: func(request *httputil.ProxyRequest) {
					currentAdminBrowserRewrite(request, target)
					if test.forwarded {
						// Reproduce the old fixture with the browser-owned header.
						request.Out.Header.Set("Accept-Encoding", request.In.Header.Get("Accept-Encoding"))
					}
				},
				Transport: transport,
				ModifyResponse: func(response *http.Response) error {
					contentType = response.Header.Get("Content-Type")
					contentEncoding = response.Header.Get("Content-Encoding")
					uncompressed = response.Uncompressed
					return state.response(response)
				},
				ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
					http.Error(w, "fixture native response unavailable", http.StatusBadGateway)
				},
			}
			request := httptest.NewRequest(http.MethodPost, currentAdminBrowserOrigin+path, strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Connect-Protocol-Version", "1")
			request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request)
			t.Logf("content_type=%s content_encoding=%q transport_uncompressed=%t status=%d", contentType, contentEncoding, uncompressed, response.Code)
			if response.Code != test.status || !strings.HasPrefix(contentType, "application/json") {
				t.Fatal("fixture response observation", response.Code, contentType)
			}
			if test.forwarded {
				if contentEncoding != "gzip" || uncompressed || state.latest != nil || !state.failed {
					t.Fatal("old compressed JSON failure was not reproduced")
				}
				return
			}
			var got pb.GetLogSettingsResponse
			if contentEncoding != "" || !uncompressed || response.Header().Get("Content-Encoding") != "" || protojson.Unmarshal(response.Body.Bytes(), &got) != nil || !proto.Equal(&got, want) || !proto.Equal(state.latest, want) || state.failed {
				t.Fatal("proxy did not preserve the full typed JSON observation")
			}
		})
	}
}

func TestCurrentAdminBrowserLossPreservesHeadersAndOriginal(t *testing.T) {
	digest := func(value byte) []byte { return bytes.Repeat([]byte{value}, 32) }
	profile := &pb.CurrentAuthorityProfile{Version: 2, Domain: digest(1), Cohort: digest(2), Generation: bytes.Repeat([]byte{3}, 16), Protocol: digest(4), TimeProfile: digest(5), Membership: digest(6), Configuration: digest(7)}
	review := &pb.CurrentSecurityReview{Profile: profile, ExpectedCut: &pb.CurrentSemanticCut{Version: 1, Domain: profile.Domain, Cohort: profile.Cohort, Generation: profile.Generation, Sequence: 9, Previous: digest(8), Projection: digest(9), Frontier: digest(10), Fences: digest(11), Policy: digest(12)}, ChangeId: &pb.CurrentSecurityChangeID{Version: 1, Domain: profile.Domain, Cohort: profile.Cohort, Namespace: 19, Nonce: bytes.Repeat([]byte{13}, 16)}, Actor: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://fixture.invalid", Subject: "admin"}, IntentDigest: digest(14), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutLogPolicy{PutLogPolicy: &pb.ManagedLogPolicy{Version: 1, Level: pb.ManagedLogLevel_MANAGED_LOG_LEVEL_INFO}}}}}
	resulting := proto.Clone(review.ExpectedCut).(*pb.CurrentSemanticCut)
	resulting.Sequence++
	completed := &pb.ApplySecurityChangesResponse{CurrentResult: &pb.CurrentSecurityChangeResult{Profile: profile, ChangeId: review.ChangeId, IntentDigest: review.IntentDigest, Progress: pb.CurrentSecurityProgress_CURRENT_SECURITY_PROGRESS_APPLIED, Original: &pb.CurrentSecurityOriginalOutcome{ChangeId: review.ChangeId, IntentDigest: review.IntentDigest, HandoffDigest: digest(15), Commit: &pb.CurrentControlCommit{Version: 1, Domain: profile.Domain, Cohort: profile.Cohort, Membership: profile.Membership, Configuration: profile.Configuration, Slot: 7, Value: digest(16)}, Disposition: pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED, Items: []*pb.CurrentSecurityItemOutcome{{Index: 0, Kind: "log_policy.put", Disposition: pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED}}, ObservedCut: review.ExpectedCut, ResultingCut: resulting}}}
	path := "/browser" + graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure
	var upstreamApplies atomic.Int32
	backend := httptest.NewServer(connect.NewUnaryHandler(path, func(_ context.Context, request *connect.Request[pb.ApplySecurityChangesRequest]) (*connect.Response[pb.ApplySecurityChangesResponse], error) {
		upstreamApplies.Add(1)
		if !proto.Equal(request.Msg.CurrentReview, review) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("original review changed"))
		}
		return connect.NewResponse(completed), nil
	}, connect.WithCompressMinBytes(1024)))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	state := &currentAdminBrowserGateway{}
	proxy := &httputil.ReverseProxy{
		Rewrite:        func(request *httputil.ProxyRequest) { currentAdminBrowserRewrite(request, target) },
		Transport:      transport,
		ModifyResponse: state.response,
		ErrorHandler:   state.responseError,
	}
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if err := state.request(request); err != nil {
			http.Error(w, "fixture request contract refused", http.StatusBadRequest)
			return
		}
		proxy.ServeHTTP(w, request)
	}))
	defer frontend.Close()
	raw, err := protojson.Marshal(&pb.ApplySecurityChangesRequest{CurrentReview: review})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, frontend.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := frontend.Client().Do(request)
	if err != nil {
		t.Fatal("loss injection withheld response headers", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	state.mu.Lock()
	defer state.mu.Unlock()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" || response.ContentLength <= 0 || response.ContentLength != int64(state.droppedBytes) || len(body) != 0 || !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatal("loss must expose only headers and an incomplete nonempty representation", response.StatusCode, response.ContentLength, len(body), readErr)
	}
	var result pb.ApplySecurityChangesResponse
	if protojson.Unmarshal(body, &result) == nil {
		t.Fatal("withheld result became a successful JSON response")
	}
	if upstreamApplies.Load() != 1 || state.applyRequests != 1 || state.droppedStatus != http.StatusOK || state.droppedBytes == 0 || state.failed || !proto.Equal(state.review, review) {
		t.Fatal("response loss changed dispatch count or retained original", upstreamApplies.Load(), state.applyRequests)
	}
}
