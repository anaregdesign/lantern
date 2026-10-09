package integration_test

// The controller runs in its own disposable container. Three separate product
// processes use original read-only provisioning and distinct journal/custody
// mounts. Docker orchestration lives outside the containers; this test uses only
// public TLS APIs and fixture coordination files, never an authority/test clock.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type custodyGateOriginal struct {
	review   *pb.CurrentSecurityReview
	original *pb.CurrentSecurityOriginalOutcome
	raw      []byte
}

type custodyProcessGate struct {
	t           *testing.T
	issuer      *oidcControlWireFixture
	http        *http.Client
	urls        []string
	controls    []graphv1connect.LanternSecurityServiceClient
	data        []graphv1connect.LanternServiceClient
	token       string
	originals   []custodyGateOriginal
	cookies     []*http.Cookie
	pending     []*http.Cookie
	callback    string
	purpose     *pb.BeginSecurityChangeAuthorizationResponse
	purposeView *pb.CurrentSecurityReview
}

func TestCurrentCustodyProcessGate(t *testing.T) {
	if os.Getenv("LANTERN_CURRENT_CUSTODY_GATE") != "1" {
		t.Skip("explicit three-container current custody gate")
	}
	hosts := strings.Split(os.Getenv("LANTERN_CURRENT_FIXTURE_HOSTS"), ",")
	driver := os.Getenv("LANTERN_CURRENT_CUSTODY_DRIVER")
	if len(hosts) != 3 || net.ParseIP(driver) == nil {
		t.Fatal("exact private container addresses required")
	}
	cert := custodyGatePublicTLS(t, append(append([]string{}, hosts...), driver))
	// Provision one immutable shared cursor key with the original fixture inputs.
	// The actual entry point requires it for protected CDC even when this bounded
	// lifecycle campaign makes only control and ordinary data calls.
	var cursorKey [32]byte
	if _, err := rand.Read(cursorKey[:]); err != nil {
		t.Fatal(err)
	}
	ring := fmt.Sprintf(`{"current_version":1,"keys":[{"version":1,"key":"%x"}]}`, cursorKey)
	if err := createPrivateTestFile("/provision/cursor-keys.json", []byte(ring)); err != nil {
		t.Fatal(err)
	}
	f := newOIDCWireIssuer(t)
	f.realClock = true
	handler := f.provider.Config.Handler
	f.provider.Close()
	issuer := httptest.NewUnstartedServer(handler)
	_ = issuer.Listener.Close()
	listener, err := net.Listen("tcp", "0.0.0.0:8443")
	if err != nil {
		t.Fatal(err)
	}
	issuer.Listener = listener
	issuer.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	issuer.StartTLS()
	issuer.URL = "https://" + driver + ":8443"
	f.provider = issuer
	t.Cleanup(issuer.Close)
	command := exec.CommandContext(t.Context(), "/exporter", "-test.v", "-test.run=^TestCurrentProvisioningExportPublicFixture$", "-test.timeout=1m")
	command.Env = append(os.Environ(), "LANTERN_CURRENT_FIXTURE_DIR=/provision", "LANTERN_CURRENT_FIXTURE_ISSUER="+issuer.URL)
	out, err := command.CombinedOutput()
	if err := os.WriteFile("/control/exporter.log", out, 0600); err != nil {
		t.Fatal(err)
	}
	if err != nil || !bytes.Contains(out, []byte("--- PASS: TestCurrentProvisioningExportPublicFixture")) || bytes.Contains(out, []byte("--- SKIP:")) {
		t.Fatalf("original provisioning export: %v\n%s", err, out)
	}
	token := f.token(t, "admin", func(c map[string]any) {
		c["iat"] = time.Now().Add(-time.Minute).Unix()
		c["auth_time"] = time.Now().Add(-time.Hour).Unix()
	})
	if err := createPrivateTestFile("/provision/admin.token", []byte(token)); err != nil {
		t.Fatal(err)
	}
	transport := custodyGateTransport(t)
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	g := &custodyProcessGate{t: t, issuer: f, http: httpClient, token: token}
	provisioning := custodyGateProvisioning(t)
	custodyGateJSON(t, "/control/provision-manifest.json", provisioning)
	for _, host := range hosts {
		endpoint := "https://" + host + ":6380"
		g.urls = append(g.urls, endpoint)
		g.controls = append(g.controls, graphv1connect.NewLanternSecurityServiceClient(httpClient, endpoint))
		g.data = append(g.data, graphv1connect.NewLanternServiceClient(httpClient, endpoint))
	}
	custodyGateJSON(t, "/control/initialized.json", map[string]any{"issuer": issuer.URL, "hosts": hosts, "native_clock_injected": false})
	previous := 0
	for {
		var command struct {
			Sequence int
			Phase    string
		}
		deadline := time.Now().Add(4 * time.Minute)
		for command.Sequence <= previous {
			raw, err := os.ReadFile("/control/command.json")
			if err == nil && json.Unmarshal(raw, &command) != nil {
				t.Fatal("invalid controller command")
			}
			if time.Now().After(deadline) {
				t.Fatal("controller command deadline")
			}
			if command.Sequence <= previous {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if command.Sequence != previous+1 {
			t.Fatal("controller skipped a required phase")
		}
		previous = command.Sequence
		passed := t.Run(command.Phase, func(t *testing.T) {
			g.t = t
			if !reflect.DeepEqual(provisioning, custodyGateProvisioning(t)) {
				t.Fatal("original provisioning changed during intact lifecycle")
			}
			transport.CloseIdleConnections()
			switch command.Phase {
			case "single-before-quorum":
				g.requireUnavailable(0)
			case "fresh":
				g.waitReady(0, 1, 2)
				g.apply("custody_original")
				g.checkOriginals(0, 1, 2)
				g.writeAndReadData()
				g.browserState()
			case "resume-one", "resume-two":
				g.waitReady(0, 1, 2)
				g.checkOriginals(0, 1, 2)
				g.readData()
				g.checkBrowserAfterRestart()
			case "partition-change":
				g.waitReady(0, 1)
				g.apply("custody_partition")
				g.checkOriginals(0, 1)
			case "reconnected", "isolated-restart-reconnected":
				g.waitReady(0, 1, 2)
				g.checkOriginals(0, 1, 2)
				g.readData()
			case "finish":
			default:
				t.Fatal("unknown phase", command.Phase)
			}
		})
		proofs := make([]map[string]any, 0, len(g.originals))
		for _, original := range g.originals {
			digest := sha256.Sum256(original.raw)
			review, _ := protojson.Marshal(original.review)
			outcome, _ := protojson.Marshal(original.original)
			proofs = append(proofs, map[string]any{"review": json.RawMessage(review), "original": json.RawMessage(outcome), "original_sha256": hex.EncodeToString(digest[:])})
		}
		custodyGateJSON(t, fmt.Sprintf("/control/result-%02d.json", command.Sequence), map[string]any{"phase": command.Phase, "pass": passed, "utc": time.Now().UTC(), "originals": proofs})
		if !passed || command.Phase == "finish" {
			return
		}
	}
}

func custodyGateProvisioning(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir("/provision", func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err == nil {
			digest := sha256.Sum256(raw)
			files[path] = hex.EncodeToString(digest[:])
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func custodyGatePublicTLS(t *testing.T, hosts []string) tls.Certificate {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1727), Subject: pkix.Name{CommonName: "custody disposable public TLS"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		if ip == nil {
			t.Fatal("invalid fixture host", host)
		}
		template.IPAddresses = append(template.IPAddresses, ip)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	for name, raw := range map[string][]byte{"public.pem": certPEM, "public.key": keyPEM, "oidc.pem": certPEM} {
		if err := createPrivateTestFile(filepath.Join("/provision", name), raw); err != nil {
			t.Fatal(err)
		}
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func custodyGateTransport(t *testing.T) *http.Transport {
	t.Helper()
	raw, err := os.ReadFile("/provision/public.pem")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		t.Fatal("fixture public trust missing")
	}
	return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}, ForceAttemptHTTP2: true, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
}

func custodyGateJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path+".next", append(raw, '\n'), 0600); err == nil {
		err = os.Rename(path+".next", path)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (g *custodyProcessGate) principal(id int) (*pb.GetCurrentPrincipalResponse, error) {
	ctx, cancel := context.WithTimeout(g.t.Context(), 5*time.Second)
	defer cancel()
	response, err := g.controls[id].GetCurrentPrincipal(ctx, securityWireRequest(g.token, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (g *custodyProcessGate) waitReady(ids ...int) {
	g.t.Helper()
	for _, id := range ids {
		deadline := time.Now().Add(100 * time.Second)
		for {
			if _, err := g.principal(id); err == nil {
				break
			} else if time.Now().After(deadline) {
				g.t.Fatal("fresh native/current authority did not become ready", id+1, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (g *custodyProcessGate) requireUnavailable(id int) {
	g.t.Helper()
	deadline := time.Now().Add(100 * time.Second)
	for {
		if !custodyGateReachableTLS(g.http, g.urls[id]) {
			if time.Now().After(deadline) {
				g.t.Fatal("public TLS never became reachable before quorum")
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_, err := g.principal(id)
		if custodyGateUnavailable(err) {
			g.t.Log("protected public API refused before quorum/current authority")
			return
		}
		if err == nil || time.Now().After(deadline) {
			g.t.Fatal("pre-quorum process did not refuse authorization", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func custodyGateReachableTLS(client *http.Client, endpoint string) bool {
	request, err := http.NewRequest(http.MethodPost, endpoint+"/grpc.health.v1.Health/Check", strings.NewReader("{}"))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	var health struct {
		Status string `json:"status"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&health)
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK && decodeErr == nil && health.Status != "" && response.TLS != nil && response.TLS.Version == tls.VersionTLS13
}

func custodyGateUnavailable(err error) bool {
	var transport *url.Error
	var socket *net.OpError
	return connect.CodeOf(err) == connect.CodeUnavailable && !errors.As(err, &transport) && !errors.As(err, &socket)
}

func (g *custodyProcessGate) apply(role string) {
	g.t.Helper()
	principal, err := g.principal(0)
	if err != nil {
		g.t.Fatal(err)
	}
	review, err := g.controls[0].PrepareSecurityChanges(g.t.Context(), securityWireRequest(g.token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: principal.Version.CurrentProfile, ExpectedCut: principal.Version.CurrentCut, Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: role, Name: role}}}}}}))
	if err != nil {
		g.t.Fatal("prepare actual control change", err)
	}
	// Exactly one Apply. All subsequent completion and restart recovery is status
	// only; no replacement ID or repeated operation is minted on uncertain output.
	_, _ = g.controls[0].ApplySecurityChanges(g.t.Context(), securityWireRequest(g.token, &pb.ApplySecurityChangesRequest{CurrentReview: review.Msg.CurrentReview}))
	original := awaitCurrentPublicOriginal(g.t, g.t.Context(), g.controls[0], g.token, review.Msg.CurrentReview)
	if original.GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
		g.t.Fatal("actual control operation not applied", original)
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(original)
	if err != nil {
		g.t.Fatal(err)
	}
	g.originals = append(g.originals, custodyGateOriginal{proto.Clone(review.Msg.CurrentReview).(*pb.CurrentSecurityReview), proto.Clone(original).(*pb.CurrentSecurityOriginalOutcome), raw})
}

func (g *custodyProcessGate) checkOriginals(ids ...int) {
	g.t.Helper()
	for _, id := range ids {
		for _, expected := range g.originals {
			original := awaitCurrentPublicOriginal(g.t, g.t.Context(), g.controls[id], g.token, expected.review)
			raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(original)
			if err != nil || !bytes.Equal(raw, expected.raw) {
				g.t.Fatal("original bytes/change identity changed after restart/catch-up", id+1, err)
			}
		}
	}
}

func (g *custodyProcessGate) writeAndReadData() {
	g.t.Helper()
	g.waitReady(0, 1, 2)
	for i, data := range g.data {
		_, err := data.PutVertex(g.t.Context(), securityWireRequest(g.token, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: fmt.Sprintf("orders:custody-%d", i+1), Value: &pb.Vertex_String_{String_: "independent data WAL"}}}))
		if err != nil {
			g.t.Fatal("protected data write", i+1, err)
		}
	}
	g.readData()
}

func (g *custodyProcessGate) readData() {
	g.t.Helper()
	for i, data := range g.data {
		response, err := data.GetVertex(g.t.Context(), securityWireRequest(g.token, &pb.GetVertexRequest{Key: fmt.Sprintf("orders:custody-%d", i+1)}))
		if err != nil || response.Msg.GetVertex().GetString_() != "independent data WAL" {
			g.t.Fatal("protected data restart read", i+1, err)
		}
	}
}

func (g *custodyProcessGate) browse(id int, path string, cookies []*http.Cookie) *http.Response {
	g.t.Helper()
	g.issuer.server = &httptest.Server{URL: g.urls[id]}
	request := g.issuer.browserRequest(g.t, http.MethodGet, path, cookies, "")
	response, err := g.http.Do(request)
	if err != nil {
		g.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response
}

func (g *custodyProcessGate) loginStart(id int) *http.Response {
	g.t.Helper()
	query := url.Values{"issuer": {g.issuer.provider.URL}, "return": {"/security/roles"}}
	response := g.browse(id, "/auth/login?"+query.Encode(), nil)
	if response.StatusCode != http.StatusFound || len(response.Cookies()) != 1 {
		g.t.Fatal("real Code login start", response.StatusCode)
	}
	return response
}

func (g *custodyProcessGate) browserState() {
	g.t.Helper()
	g.waitReady(0, 1, 2)
	start := g.loginStart(0)
	path := g.issuer.browserCallbackPath(g.t, start, "admin", false)
	time.Sleep(500 * time.Millisecond)
	response := g.browse(0, path, start.Cookies())
	if response.StatusCode != http.StatusSeeOther {
		g.t.Fatal("durable Code session creation", response.StatusCode)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Value != "" {
			g.cookies = append(g.cookies, cookie)
		}
	}
	if len(g.cookies) != 2 {
		g.t.Fatal("durable session cookies missing")
	}
	g.waitReady(0, 1, 2)
	pending := g.loginStart(1)
	g.pending, g.callback = pending.Cookies(), g.issuer.browserCallbackPath(g.t, pending, "admin", false)
	principal, err := g.principal(1)
	if err != nil {
		g.t.Fatal(err)
	}
	change := &pb.SecurityChange{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: g.issuer.provider.URL, Subject: "bob"}, RoleId: "security_admin"}}}
	prepared, err := g.controls[1].PrepareSecurityChanges(g.t.Context(), securityWireRequest(g.token, &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Profile: principal.Version.CurrentProfile, ExpectedCut: principal.Version.CurrentCut, Changes: []*pb.SecurityChange{change}}}))
	if err != nil {
		g.t.Fatal(err)
	}
	begin, err := g.controls[1].BeginSecurityChangeAuthorization(g.t.Context(), securityWireRequest(g.token, &pb.BeginSecurityChangeAuthorizationRequest{CurrentReview: prepared.Msg.CurrentReview}))
	if err != nil || len(begin.Msg.GetAuthorizationId()) == 0 || begin.Msg.GetAttemptAffinity() == "" {
		g.t.Fatal("process-owned purpose creation", err)
	}
	g.purpose, g.purposeView = begin.Msg, prepared.Msg.CurrentReview
}

func (g *custodyProcessGate) checkBrowserAfterRestart() {
	g.t.Helper()
	for id := range g.urls {
		if response := g.browse(id, "/auth/session", g.cookies); response.StatusCode != http.StatusOK {
			g.t.Fatal("normal restart invalidated a durable valid session", id+1, response.StatusCode)
		}
	}
	before := g.issuer.fetches.Load()
	if response := g.browse(1, g.callback, g.pending); response.StatusCode == http.StatusSeeOther || g.issuer.fetches.Load() != before {
		g.t.Fatal("old process callback reached Code exchange")
	}
	_, err := g.controls[1].GetSecurityChangeAuthorization(g.t.Context(), securityWireRequest(g.token, &pb.GetSecurityChangeAuthorizationRequest{CurrentProfile: g.purposeView.Profile, AuthorizationId: g.purpose.AuthorizationId, AttemptAffinity: g.purpose.AttemptAffinity}))
	if err == nil {
		g.t.Fatal("old process purpose restored")
	}
}

// Run by docker exec in the isolated node's own network namespace. The URL and
// TLS identity remain the node's original address; only the local dial targets
// loopback, so network disconnection cannot masquerade as authorization refusal.
func TestCurrentCustodyLocalProbe(t *testing.T) {
	if os.Getenv("LANTERN_CURRENT_CUSTODY_GATE") != "1" {
		t.Skip("explicit isolated-container custody probe")
	}
	endpoint := os.Getenv("LANTERN_CURRENT_CUSTODY_NODE_URL")
	raw, err := os.ReadFile("/provision/machine.token")
	if err != nil {
		t.Fatal(err)
	}
	transport := custodyGateTransport(t)
	defer transport.CloseIdleConnections()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, "127.0.0.1:6380")
	}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	control := graphv1connect.NewLanternSecurityServiceClient(client, endpoint)
	data := graphv1connect.NewLanternServiceClient(client, endpoint)
	deadline := time.Now().Add(80 * time.Second)
	for {
		if !custodyGateReachableTLS(client, endpoint) {
			if time.Now().After(deadline) {
				t.Fatal("isolated public TLS listener did not start")
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_, err = control.GetCurrentPrincipal(t.Context(), securityWireRequest(string(raw), &pb.GetCurrentPrincipalRequest{}))
		if custodyGateUnavailable(err) {
			break
		}
		if err == nil || time.Now().After(deadline) {
			t.Fatal("isolated node failed to refuse current authorization", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err = data.GetVertex(t.Context(), securityWireRequest(string(raw), &pb.GetVertexRequest{Key: "orders:custody-3"})); !custodyGateUnavailable(err) {
		t.Fatal("isolated protected data operation not refused", err)
	}
	t.Log("actual local public TLS returned unavailable for control and protected data with a valid durable machine credential; no peer/IdP lookup substituted for the refusal")
}
