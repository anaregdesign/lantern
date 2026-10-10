// Command protectedquery prepares explicit, matched OFF/OIDC query diagnostics.
// It never participates in run.sh or the release sweep implicitly.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

type fixtureInput struct {
	Nodes []struct {
		PublicOrigin string `json:"public_origin"`
		PeerOrigin   string `json:"peer_origin"`
	} `json:"nodes"`
	CAFile    string `json:"ca_file"`
	TokenFile string `json:"token_file"`
	Query     *struct {
		Mode             string                      `json:"mode"`
		FixtureID        string                      `json:"fixture_id"`
		SecurityProfile  string                      `json:"security_profile"`
		CurrentProfile   *pb.CurrentAuthorityProfile `json:"current_profile"`
		CurrentBinding   string                      `json:"current_binding"`
		Exporter         *map[string]string          `json:"exporter"`
		Issuer           string                      `json:"issuer"`
		AdminTokenFile   string                      `json:"admin_token_file"`
		ReaderTokenFile  string                      `json:"reader_token_file"`
		InvalidTokenFile string                      `json:"invalid_token_file"`
		ReaderSubject    string                      `json:"reader_subject"`
		ReaderRole       string                      `json:"reader_role"`
		ExpiresAt        time.Time                   `json:"expires_at"`
		Server           map[string]string           `json:"server"`
	} `json:"protected_query"`
}

type queryEndpoint struct {
	client graphv1connect.LanternServiceClient
	reader string
	writer string
	mode   string
}

func authenticated[T any](token string, msg *T) *connect.Request[T] {
	request := connect.NewRequest(msg)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func verifiedClient(caFile string) (*http.Client, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid query CA")
	}
	return &http.Client{Transport: &http.Transport{Proxy: nil, ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, VerifyConnection: func(state tls.ConnectionState) error {
			if state.NegotiatedProtocol != "h2" {
				return errors.New("query transport requires verified TLS HTTP/2")
			}
			return nil
		}}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func readBounded(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("trailing fixture input")
	}
	return nil
}

func readToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(raw) == 0 || len(raw) > 8192 || strings.ContainsAny(string(raw), "\r\n\x00") {
		return "", errors.New("invalid bounded query token file")
	}
	return string(raw), nil
}

func validateFixture(f fixtureInput) error {
	if len(f.Nodes) == 0 || f.Query == nil || f.CAFile == "" || (f.Query.Mode != "off" && f.Query.Mode != "oidc") || f.Query.ReaderRole != "fixture_query_reader" || f.Query.ReaderSubject != "fixture-query-reader" || time.Until(f.Query.ExpiresAt) < time.Minute {
		return errors.New("fresh standalone protected-query fixture required")
	}
	expectedNodes := 1
	if f.Query.SecurityProfile == "current-v2" {
		expectedNodes = 3
	} else if f.Query.SecurityProfile != "legacy-v1" {
		return errors.New("explicit supported query security profile required")
	}
	if len(f.Nodes) != expectedNodes {
		return errors.New("query profile process layout mismatch")
	}
	seen := map[string]bool{}
	for _, node := range f.Nodes {
		u, err := url.Parse(node.PublicOrigin)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() != "localhost" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || node.PeerOrigin != "" || seen[node.PublicOrigin] {
			return errors.New("distinct local HTTPS data-only fixture origins required")
		}
		seen[node.PublicOrigin] = true
	}
	if f.Query.SecurityProfile == "current-v2" {
		if len(f.Query.FixtureID) != 32 {
			return errors.New("current query requires its owned fixture identity")
		}
		if _, err := hex.DecodeString(f.Query.FixtureID); err != nil {
			return err
		}
		if f.Query.Exporter == nil || (*f.Query.Exporter)["revision"] != f.Query.Server["revision"] || (*f.Query.Exporter)["modified"] != f.Query.Server["modified"] || len((*f.Query.Exporter)["sha256"]) != 64 {
			return errors.New("current query requires matching original-input exporter provenance")
		}
		if f.Query.Mode == "oidc" && (!validCurrentProfile(f.Query.CurrentProfile) || !strings.HasPrefix(f.Query.CurrentBinding, "current-v2:") || len(f.Query.CurrentBinding) != 75) {
			return errors.New("complete expected current profile/binding required")
		}
	}
	if f.Query.Mode == "off" || f.Query.SecurityProfile == "legacy-v1" {
		if f.Query.CurrentProfile != nil || f.Query.CurrentBinding != "" {
			return errors.New("OFF/legacy fixture cannot claim current authority")
		}
	}

	return nil
}

func assignReader(ctx context.Context, f fixtureInput, httpClient *http.Client) error {
	token, err := readToken(f.Query.AdminTokenFile)
	if err != nil {
		return err
	}
	client := graphv1connect.NewLanternSecurityServiceClient(httpClient, f.Nodes[0].PublicOrigin)
	status, err := client.ListRoles(ctx, authenticated(token, &pb.ListRolesRequest{}))
	if err != nil {
		return fmt.Errorf("setup ListRoles: %w", err)
	}
	if f.Query.SecurityProfile == "current-v2" {
		if !proto.Equal(status.Msg.GetVersion().GetCurrentProfile(), f.Query.CurrentProfile) || status.Msg.GetVersion().GetCurrentCut() == nil {
			return errors.New("setup did not return current full binding")
		}
		for _, role := range status.Msg.Roles {
			if role.Id == f.Query.ReaderRole {
				return nil
			}
		}
		return errors.New("original current reader Role missing")
	}
	changeID := sha256.Sum256([]byte("protected-query-reader-assignment-v1"))
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.Query.Issuer, Subject: f.Query.ReaderSubject}
	response, err := client.ApplySecurityChanges(ctx, authenticated(token, &pb.ApplySecurityChangesRequest{
		ExpectedRevision: status.Msg.Version.Revision, ChangeId: changeID[:16],
		Changes: []*pb.SecurityChange{
			{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
			{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: f.Query.ReaderRole}}},
		},
	}))
	if err != nil {
		return fmt.Errorf("setup assignment: %w", err)
	}
	if len(response.Msg.Applied) != 2 || !response.Msg.Applied[0] || !response.Msg.Applied[1] || response.Msg.Enforcement != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED {
		return errors.New("reader assignment not enforced")
	}
	return nil
}

func execute(ctx context.Context, f fixtureInput, action string, cfg loadConfig) (map[string]any, error) {
	if err := validateFixture(f); err != nil {
		return nil, err
	}
	httpClient, err := verifiedClient(f.CAFile)
	if err != nil {
		return nil, err
	}
	defer httpClient.CloseIdleConnections()
	if err := verifyQueryCapabilities(ctx, f, httpClient); err != nil {
		return nil, err
	}
	endpoint := &queryEndpoint{mode: f.Query.Mode, client: graphv1connect.NewLanternServiceClient(httpClient, f.Nodes[0].PublicOrigin)}
	if endpoint.mode == "oidc" {
		endpoint.reader, err = readToken(f.Query.ReaderTokenFile)
		if err != nil {
			return nil, err
		}
		var tokens []string
		if err := readBounded(f.TokenFile, &tokens); err != nil || len(tokens) != 1 || !strings.HasPrefix(tokens[0], "lnt_m1_") {
			return nil, errors.New("independent machine writer credential required")
		}
		endpoint.writer = tokens[0]
	}
	report := map[string]any{"schema_version": 1, "qualification": "preparation_only", "action": action, "mode": endpoint.mode,
		"security_profile": f.Query.SecurityProfile, "server_processes": len(f.Nodes), "comparison_axis": "end_to_end_mode_specific_authorized_results", "expected_search_keys": expectedSearchKeys(endpoint.mode),
		"transport": "verified_tls_http2", "topology": "standalone_broad_illuminate_with_hidden_bridge", "corpus_sha256": corpusDigest(),
		"reader_actor": "unauthenticated_off", "writer_actor": "unauthenticated_off", "driver_source": buildSource(), "server_binary": f.Query.Server,
		"measurements": map[string]string{"server_allocations": "not_measured", "server_retained_memory": "not_measured", "server_peak_memory": "not_measured", "internal_writer_lock_wait": "not_measured", "export_revocation": "not_measured", "ttl_delete_restore": "not_measured", "host_qualification": "not_measured"}}
	if f.Query.SecurityProfile == "current-v2" {
		report["fixture_id"] = f.Query.FixtureID
		report["exporter_binary"] = f.Query.Exporter
		report["current_profile_binding"] = f.Query.CurrentBinding
	}
	if endpoint.mode == "oidc" {
		report["reader_actor"] = "synthetic_local_end_user_bearer_jwt_role_bound"
		report["writer_actor"] = "named_machine_token_all_data_role"
	}
	switch action {
	case "seed":
		if endpoint.mode == "oidc" {
			if err := assignReader(ctx, f, httpClient); err != nil {
				return report, err
			}
		}
		err = seedCorpus(ctx, endpoint)
	case "preflight":
		invalid := ""
		if endpoint.mode == "oidc" {
			invalid, err = readToken(f.Query.InvalidTokenFile)
		}
		if err == nil {
			err = verifyCorpus(ctx, endpoint, invalid)
		}
	case "measure":
		source := buildSource()
		if source["modified"] != "false" || f.Query.Server["modified"] != "false" || len(source["revision"]) != 40 || source["revision"] != f.Query.Server["revision"] || len(f.Query.Server["sha256"]) != 64 || f.Query.SecurityProfile == "current-v2" && (*f.Query.Exporter)["modified"] != "false" {
			return report, errors.New("measurement requires matching immutable Server and driver source")
		}
		if err := cfg.validate(); err != nil {
			return report, err
		}
		if time.Until(f.Query.ExpiresAt) < cfg.duration()+cfg.Timeout+time.Minute {
			return report, errors.New("JWT lifetime does not cover declared load")
		}
		var results *loadReport
		results, err = runLoad(ctx, endpoint, cfg)
		report["load"] = results
	default:
		err = errors.New("action must be seed, preflight or measure")
	}
	report["passed"] = err == nil
	if err != nil {
		report["failure_code"] = connect.CodeOf(err).String()
	}
	return report, err
}

func buildSource() map[string]string {
	result := map[string]string{"revision": "unknown", "modified": "unknown"}
	if info, ok := debug.ReadBuildInfo(); ok {
		result["go_version"] = info.GoVersion
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				result["revision"] = setting.Value
			}
			if setting.Key == "vcs.modified" {
				result["modified"] = setting.Value
			}
			if setting.Key == "GOOS" || setting.Key == "GOARCH" {
				result[setting.Key] = setting.Value
			}
		}
	}
	if path, err := os.Executable(); err == nil {
		if file, err := os.Open(path); err == nil {
			digest := sha256.New()
			if _, err := io.Copy(digest, file); err == nil {
				result["sha256"] = hex.EncodeToString(digest.Sum(nil))
			}
			_ = file.Close()
		}
	}
	return result
}

func main() {
	fixturePath := flag.String("fixture", "", "private authfixture readiness JSON")
	action := flag.String("action", "preflight", "explicit seed, preflight or measure")
	output := flag.String("report", "", "new content-free JSON report")
	cfg := loadConfig{}
	flag.StringVar(&cfg.Family, "family", "search", "search, bfs, ppr or community")
	flag.StringVar(&cfg.Phase, "phase", "first", "first, warm or update-mixed")
	flag.IntVar(&cfg.Count, "count", 1, "fixed offered query count")
	flag.IntVar(&cfg.RPS, "rps", 1, "fixed offered query requests/second")
	flag.IntVar(&cfg.WriterRPS, "writer-rps", 0, "independent PutVertices requests/second in update-mixed")
	flag.IntVar(&cfg.Concurrency, "concurrency", 1, "maximum in-flight calls per producer; saturation fails")
	flag.DurationVar(&cfg.Timeout, "timeout", 5*time.Second, "per-RPC deadline")
	flag.Parse()
	var fixture fixtureInput
	err := readBounded(*fixturePath, &fixture)
	var report map[string]any
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		report, err = execute(ctx, fixture, *action, cfg)
		cancel()
	}
	if report != nil && *output != "" {
		file, writeErr := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if writeErr == nil {
			writeErr = errors.Join(json.NewEncoder(file).Encode(report), file.Close())
		}
		err = errors.Join(err, writeErr)
	}
	if err != nil {
		// Preserve the returned error in the owner's diagnostic log. Requests,
		// Authorization headers and credential file contents are never logged.
		fmt.Fprintln(os.Stderr, "protectedquery failed:", err)
		os.Exit(1)
	}
	if *output == "" {
		_ = json.NewEncoder(os.Stdout).Encode(report)
	}
}

func validCurrentProfile(p *pb.CurrentAuthorityProfile) bool {
	if p == nil || p.Version != 2 || len(p.Generation) != 16 || bytes.Equal(p.Generation, make([]byte, 16)) {
		return false
	}
	for _, hash := range [][]byte{p.Domain, p.Cohort, p.Protocol, p.TimeProfile, p.Membership, p.Configuration} {
		if len(hash) != 32 || bytes.Equal(hash, make([]byte, 32)) {
			return false
		}
	}
	return true
}

// Setup-only probes bind the selected profile, never a cached admission grant.
func verifyQueryCapabilities(ctx context.Context, f fixtureInput, httpClient *http.Client) error {
	for i, node := range f.Nodes {
		client := graphv1connect.NewLanternSecurityServiceClient(httpClient, node.PublicOrigin)
		response, err := client.GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
		if err != nil {
			return fmt.Errorf("query capability probe node %d: %w", i+1, err)
		}
		cap := response.Msg
		if err := validateQueryCapabilities(f, i, cap); err != nil {
			return err
		}
	}
	return nil
}

func validateQueryCapabilities(f fixtureInput, node int, cap *pb.GetAuthCapabilitiesResponse) error {
	if cap == nil || !cap.Ready {
		return errors.New("query authority is not ready")
	}
	if f.Query.Mode == "off" {
		if cap.Mode != pb.AuthMode_AUTH_MODE_OFF || cap.ProtocolVersion != 1 || cap.CurrentProfile != nil || cap.CurrentMember != 0 {
			return errors.New("OFF mode/profile mismatch")
		}
	} else if f.Query.SecurityProfile == "legacy-v1" {
		if cap.Mode != pb.AuthMode_AUTH_MODE_OIDC || cap.ProtocolVersion != 1 || cap.CurrentProfile != nil || cap.CurrentMember != 0 {
			return errors.New("legacy mode/profile mismatch")
		}
	} else if f.Query.SecurityProfile != "current-v2" || cap.Mode != pb.AuthMode_AUTH_MODE_OIDC || cap.ProtocolVersion != 2 || cap.CurrentMember != uint32(node+1) || !validCurrentProfile(cap.CurrentProfile) || !proto.Equal(cap.CurrentProfile, f.Query.CurrentProfile) {
		return errors.New("current query refused mode/protocol/member/full-profile fallback")
	}
	return nil
}
