package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"google.golang.org/protobuf/proto"
)

// Exercise the shipping CLI and required current configuration without opening any
// configured time source or creating mutable native custody.
func TestSecurityProvisioningCLI(t *testing.T) {
	binary := securityProvisioningBinary(t, false)
	for _, voters := range []int{1, 3} {
		t.Run(fmt.Sprintf("fixed_%d", voters), func(t *testing.T) {
			root := securityProvisioningDirectory(t)
			input := securityProvisioningIntent(t, root, "https://idp.example", voters, true, true)
			input["target_platform"] = "linux" // Explicitly independent of the Mac provisioning host.
			const secret = "SYNTHETIC_PROVISIONING_SECRET_1876"
			issuers := input["issuers"].([]map[string]any)
			issuers[0]["client_secret"], issuers[0]["token_origin"] = secret, "https://idp.example"
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			env := securityProvisioningEnvironment(string(raw))
			output, err := securityProvisioningCommand(t, binary, env, "provision-security", "--operator-directory", filepath.Join(root, "operator"))
			if err != nil || bytes.Contains(output, []byte(secret)) {
				if err != nil {
					t.Log(securityProvisioningDiagnostic(input))
				}
				t.Fatal("production provisioning failed or disclosed setup credentials", err)
			}
			if _, err := os.Stat(filepath.Join(root, "state", "node-1")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("provisioning created mutable runtime state", err)
			}
			before := securityProvisioningSnapshot(t, root)
			output, err = securityProvisioningCommand(t, binary, env, "provision-security", "--operator-directory", filepath.Join(root, "operator"))
			if err == nil || bytes.Contains(output, []byte(secret)) || !reflect.DeepEqual(before, securityProvisioningSnapshot(t, root)) {
				t.Fatal("repeated provisioning changed retained trust or disclosed credentials")
			}
			for i := 1; i <= voters; i++ {
				file := filepath.Join(root, "trust", "nodes", fmt.Sprintf("node-%d", i), "identity.json")
				config := securityProvisioningLoad(t, input, fmt.Sprintf("node-%d", i))
				if config.TargetPlatform != "linux" || config.ProvisionedPeer == nil || len(config.Issuers) != 1 || config.Issuers[0].ClientID != "admin" || config.Issuers[0].APIAudience != "api" {
					t.Fatal("current invocation lost supplied configuration")
				}
				original, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				// Completion signs exact immutable bytes; the runtime must not
				// repair tampering or substitute regenerated identity material.
				corrupt := append(bytes.Clone(original), '\n')
				if err := os.WriteFile(file, corrupt, 0600); err != nil {
					t.Fatal(err)
				}
				selectedRaw, _ := json.Marshal(securityProvisioningNodeInput(input, fmt.Sprintf("node-%d", i)))
				if _, err := provider.LoadSecurityConfigJSON(string(selectedRaw)); err == nil {
					t.Fatal("tampered immutable material accepted")
				}
				unchanged, err := os.ReadFile(file)
				if err != nil || !bytes.Equal(unchanged, corrupt) {
					t.Fatal("tamper refusal silently rewrote original material", err)
				}
				if err := os.WriteFile(file, original, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(file), "control-signing-key.pem")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("operator signing custody entered serving inputs", err)
				}
				if runtime.GOOS != "linux" {
					selected := securityProvisioningNodeInput(input, fmt.Sprintf("node-%d", i))
					reference, _ := json.Marshal(selected)
					if output, err := securityProvisioningCommand(t, binary, securityProvisioningEnvironment(string(reference)), "init-security"); err == nil || bytes.Contains(output, []byte(secret)) || !bytes.Contains(output, []byte("target does not match")) {
						t.Fatal("cross-target native initialization was accepted or failed without its target diagnostic", err)
					}
				}
			}
			for _, raw := range []string{`{"provisioned_file":"missing"}`, `{"provisioned_file":"missing","authority":{}}`, `{"authority":{},"issuers":[]}`} {
				t.Setenv("LANTERN_AUTH_MODE", "oidc")
				t.Setenv("LANTERN_SECURITY_CONFIG_JSON", raw)
				if _, err := provider.LoadSecurityConfig(); err == nil {
					t.Fatal("missing or retired runtime input accepted")
				}
			}
			if !reflect.DeepEqual(before, securityProvisioningSnapshot(t, root)) {
				t.Fatal("reference loading mutated provisioned trust")
			}
		})
	}
}

// The child is the actual App/CLI in an explicitly test-only executable. It
// uses bounded loopback UDP synthetic UTC and never probes a live NTP server.
// Normal-build configured-source qualification remains an independent gate.
func TestSecurityProvisioningFunctionalWire(t *testing.T) {
	supportedTarget := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" || runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
	if !supportedTarget {
		t.Skip("native controlled runtime requires a qualified platform")
	}
	binary := securityProvisioningBinary(t, true)
	for _, browser := range []bool{true, false} {
		name := "api_only"
		if browser {
			name = "browser_only"
		}
		t.Run(name, func(t *testing.T) {
			issuer := newOIDCWireIssuer(t)
			var bootstrap *oidcControlWireFixture
			root := securityProvisioningDirectory(t)
			input := securityProvisioningIntent(t, root, issuer.provider.URL, 1, browser, !browser)
			input["root_ca_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.provider.Certificate().Raw}))
			input["private_origins"] = map[string][]string{issuer.provider.URL: {"127.0.0.1/32"}}
			if !browser {
				// Initial administration retains a browser code profile for
				// purpose-bound reauthentication. The independent API issuer
				// is enrolled by that administrator over the real browser wire.
				bootstrap = newOIDCWireIssuer(t)
				input["initial_admin"] = map[string]string{"issuer": bootstrap.provider.URL, "subject": "admin"}
				input["issuers"] = append(input["issuers"].([]map[string]any), map[string]any{"issuer": bootstrap.provider.URL, "client_id": "admin", "algorithms": []string{"EdDSA"}})
				input["root_ca_pem"] = input["root_ca_pem"].(string) + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bootstrap.provider.Certificate().Raw}))
				input["private_origins"].(map[string][]string)[bootstrap.provider.URL] = []string{"127.0.0.1/32"}
			}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			env := securityProvisioningEnvironment(string(raw))
			if output, err := securityProvisioningCommand(t, binary, env, "provision-security", "--operator-directory", filepath.Join(root, "operator")); err != nil {
				t.Log(securityProvisioningDiagnostic(input))
				t.Fatalf("controlled CLI provisioning failed: %v\n%s", err, output)
			}
			ref, _ := json.Marshal(input)
			env = securityProvisioningEnvironment(string(ref))
			public := securityProvisioningAddress(t)
			_, port, _ := net.SplitHostPort(public)
			env = append(env, "LANTERN_PORT="+port, "LANTERN_METRICS_ADDR=", "LANTERN_BACKUP_ENABLED=false", "LANTERN_BACKUP_RESTORE_ON_START=false", "LANTERN_DRAIN_DELAY_SECONDS=0")
			before := securityProvisioningSnapshot(t, root)
			if _, err := securityProvisioningCommand(t, binary, env); err == nil {
				t.Fatal("ordinary startup initialized missing native state")
			}
			if !reflect.DeepEqual(before, securityProvisioningSnapshot(t, root)) {
				t.Fatal("missing-state startup changed trust or created custody")
			}
			if output, err := securityProvisioningCommand(t, binary, env, "init-security"); err != nil {
				t.Fatalf("explicit target initialization failed: %v\n%s", err, output)
			}
			initialized := securityProvisioningSnapshot(t, root)
			if _, err := securityProvisioningCommand(t, binary, env, "init-security"); err == nil || !reflect.DeepEqual(initialized, securityProvisioningSnapshot(t, root)) {
				t.Fatal("repeated initialization replaced retained native state")
			}
			issuer.server = &httptest.Server{URL: "http://" + public}
			stop := securityProvisioningStart(t, binary, env, issuer.server.URL)
			httpClient := &http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}
			control := graphv1connect.NewLanternSecurityServiceClient(httpClient, issuer.server.URL)
			if bootstrap != nil {
				bootstrap.server = issuer.server
				issuer.client = securityProvisioningBrowserControl(t, bootstrap)
				identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: issuer.provider.URL, Subject: "admin"}
				role := &pb.SecurityRole{Id: "singleton_writer", Rules: []*pb.SecurityRule{
					{Id: "read", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "singleton:"}},
					{Id: "write", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, Resource: &pb.SecurityRule_Prefix{Prefix: "singleton:"}},
				}}
				if _, err := issuer.applyChanges(t, t.Context(), "", []*pb.SecurityChange{
					{Operation: &pb.SecurityChange_PutRole{PutRole: role}},
					{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
					{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: role.Id}}},
				}); err != nil {
					t.Fatal("browser administrator failed to enroll the independent API principal", err)
				}
			}
			if browser {
				start := issuer.browserLoginStart(t, nil)
				response := wireBrowserDo(t, issuer.browserRequest(t, http.MethodGet, issuer.browserCallbackPath(t, start, "admin", false), start.Cookies(), ""))
				if response.StatusCode != http.StatusSeeOther {
					t.Fatal("browser-only login failed", response.StatusCode)
				}
				cookies := response.Cookies()
				csrf := browserCookieValue(cookies, "__Host-lantern-csrf")
				browserControl := graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: authWireRoundTripper{next: http.DefaultTransport, cookies: cookies, csrf: csrf}}, issuer.server.URL+"/browser")
				if roles, err := browserControl.ListRoles(t.Context(), connect.NewRequest(&pb.ListRolesRequest{})); err != nil || len(roles.Msg.Roles) != 1 {
					t.Fatal("browser-only session failed real Connect read", err)
				}
				calls := issuer.fetches.Load()
				if _, err := control.GetCurrentPrincipal(t.Context(), securityWireRequest(issuer.token(t, "admin", nil), &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated || issuer.fetches.Load() != calls {
					t.Fatal("browser-only issuer accepted API JWT or fetched API trust", err)
				}
			} else {
				if _, err := control.GetCurrentPrincipal(t.Context(), securityWireRequest(issuer.token(t, "admin", nil), &pb.GetCurrentPrincipalRequest{})); err != nil {
					t.Fatal("API-only issuer failed real Connect authentication", err)
				}
				securityProvisioningSingletonCreate(t, graphv1connect.NewLanternServiceClient(httpClient, issuer.server.URL), issuer.token(t, "admin", nil))
				for _, mutate := range []func(map[string]any){func(c map[string]any) { c["aud"] = "admin" }, func(c map[string]any) { c["iss"] = "https://unregistered.example" }, func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }} {
					if _, err := control.GetCurrentPrincipal(t.Context(), securityWireRequest(issuer.token(t, "admin", mutate), &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
						t.Fatal("API-only issuer accepted invalid signed token", err)
					}
				}
				calls := issuer.fetches.Load()
				query := url.Values{"issuer": {issuer.provider.URL}, "return": {"/security/roles"}}
				response := wireBrowserDo(t, issuer.browserRequest(t, http.MethodGet, "/auth/login?"+query.Encode(), nil, ""))
				if response.StatusCode == http.StatusFound || issuer.fetches.Load() != calls {
					t.Fatal("API-only issuer started browser login or fetched login trust")
				}
			}
			stop()
			stop = securityProvisioningStart(t, binary, env, issuer.server.URL)
			if !browser {
				if _, err := control.GetCurrentPrincipal(t.Context(), securityWireRequest(issuer.token(t, "admin", nil), &pb.GetCurrentPrincipalRequest{})); err != nil {
					t.Fatal("clean restart lost provisioned identity", err)
				}
			}
			stop()
			config := securityProvisioningLoad(t, input, "node-1")
			var original struct {
				Participant struct{ PPath string } `json:"participant"`
				FloorsFile  string                 `json:"floors_file"`
			}
			if err := json.Unmarshal(config.AuthorityJSON, &original); err != nil || original.Participant.PPath == "" {
				t.Fatal("generated participant state binding missing", err)
			}
			if err := os.Remove(original.Participant.PPath); err != nil {
				t.Fatal("isolated partial-state fixture could not be prepared", err)
			}
			marker := original.FloorsFile + ".state"
			var clean struct {
				Version         uint32
				Phase           string
				Cycle           uint64
				Binding, Floors [32]byte
			}
			markerRaw, err := os.ReadFile(marker)
			if err != nil || json.Unmarshal(markerRaw, &clean) != nil || clean.Phase != "CLEAN" {
				t.Fatal("clean custody record missing before deliberate partial-state damage", err)
			}
			partial := securityProvisioningSnapshot(t, root)
			if _, err := securityProvisioningCommand(t, binary, env); err == nil {
				t.Fatal("partial-state startup accepted missing custody")
			}
			if after := securityProvisioningSnapshot(t, root); !reflect.DeepEqual(partial, after) {
				// A constructor records RUNNING before opening the original
				// families. Later rejection deliberately leaves that poisoned
				// cycle in place; it cannot publish a new CLEAN checkpoint.
				dirty := clean
				markerRaw, err = os.ReadFile(marker)
				if err != nil || json.Unmarshal(markerRaw, &dirty) != nil || dirty.Phase != "RUNNING" || dirty.Cycle != clean.Cycle+1 || dirty.Version != clean.Version || dirty.Binding != clean.Binding || dirty.Floors != [32]byte{} {
					t.Fatal("partial-state refusal did not retain its fail-closed lifecycle record", err)
				}
				relative, _ := filepath.Rel(root, marker)
				delete(partial, relative)
				delete(after, relative)
				if !reflect.DeepEqual(partial, after) {
					t.Fatal("partial-state refusal erased/replaced an original family or regenerated fresh state")
				}
			}
		})
	}
	t.Run("supplied_configuration_lifecycle", func(t *testing.T) { securityProvisioningSuppliedLifecycle(t, binary) })
	t.Run("fixed_three_renewal_restart", func(t *testing.T) {
		securityProvisioningFixedCohort(t, binary)
	})
}

// The configured trust is supplied afresh; native history keeps grants and
// principals, while a current issuer addition does not create either one.
func securityProvisioningSuppliedLifecycle(t *testing.T, binary string) {
	t.Helper()
	adminIssuer, addedIssuer := newOIDCWireIssuer(t), newOIDCWireIssuer(t)
	root := securityProvisioningDirectory(t)
	input := securityProvisioningIntent(t, root, adminIssuer.provider.URL, 1, true, true)
	input["root_ca_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: adminIssuer.provider.Certificate().Raw}))
	input["private_origins"] = map[string][]string{adminIssuer.provider.URL: {"127.0.0.1/32"}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	env := securityProvisioningEnvironment(string(raw))
	if output, err := securityProvisioningCommand(t, binary, env, "provision-security", "--operator-directory", filepath.Join(root, "operator")); err != nil {
		t.Log(securityProvisioningDiagnostic(input))
		t.Fatalf("required-config provisioning failed: %v\n%s", err, output)
	}
	beforeInit := securityProvisioningSnapshot(t, root)
	for _, kind := range []string{"missing", "empty", "malformed"} {
		bad := securityProvisioningInvalidEnvironment(env, kind)
		if _, err := securityProvisioningCommand(t, binary, bad, "init-security"); err == nil {
			t.Fatal("initialization accepted invalid current config", kind)
		}
		if !reflect.DeepEqual(beforeInit, securityProvisioningSnapshot(t, root)) {
			t.Fatal("invalid init changed original material", kind)
		}
	}
	if output, err := securityProvisioningCommand(t, binary, env, "init-security"); err != nil {
		t.Fatalf("same JSON explicit initialization failed: %v\n%s", err, output)
	}
	address := securityProvisioningAddress(t)
	_, port, _ := net.SplitHostPort(address)
	operational := []string{"LANTERN_PORT=" + port, "LANTERN_METRICS_ADDR=", "LANTERN_BACKUP_ENABLED=false", "LANTERN_BACKUP_RESTORE_ON_START=false", "LANTERN_DRAIN_DELAY_SECONDS=0"}
	env = append(env, operational...)
	origin := "http://" + address
	adminIssuer.server = &httptest.Server{URL: origin}
	addedIssuer.server = adminIssuer.server
	client := graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, origin)
	data := graphv1connect.NewLanternServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, origin)
	stop := securityProvisioningStart(t, binary, env, origin)
	adminToken := adminIssuer.token(t, "admin", nil)
	roles, err := client.ListRoles(t.Context(), securityWireRequest(adminToken, &pb.ListRolesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	users, err := client.ListUsers(t.Context(), securityWireRequest(adminToken, &pb.ListUsersRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(roles.Msg.Roles) != 1 || len(users.Msg.Users) != 1 {
		t.Fatal("unexpected fresh setup policy")
	}
	addedToken := addedIssuer.token(t, "reader", nil)
	if _, err := client.GetCurrentPrincipal(t.Context(), securityWireRequest(addedToken, &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated || addedIssuer.fetches.Load() != 0 {
		t.Fatal("unconfigured issuer accepted or contacted", err)
	}
	stop()
	clean := securityProvisioningSnapshot(t, root)
	for _, kind := range []string{"missing", "empty", "malformed"} {
		bad := securityProvisioningInvalidEnvironment(env, kind)
		if _, err := securityProvisioningCommand(t, binary, bad); err == nil {
			t.Fatal("restart accepted invalid current config", kind)
		}
		if !reflect.DeepEqual(clean, securityProvisioningSnapshot(t, root)) {
			t.Fatal("invalid restart changed retained native state", kind)
		}
	}
	// Exact same JSON resumes the same historical state.
	stop = securityProvisioningStart(t, binary, env, origin)
	if _, err := client.GetCurrentPrincipal(t.Context(), securityWireRequest(adminToken, &pb.GetCurrentPrincipalRequest{})); err != nil {
		t.Fatal("same supplied JSON restart lost administrator", err)
	}
	stop()
	originalIssuers := input["issuers"].([]map[string]any)
	input["issuers"] = append(append([]map[string]any(nil), originalIssuers...), map[string]any{"issuer": addedIssuer.provider.URL, "api_audience": "api", "algorithms": []string{"EdDSA"}, "human_subject_namespace_qualified": true})
	input["root_ca_pem"] = input["root_ca_pem"].(string) + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: addedIssuer.provider.Certificate().Raw}))
	input["private_origins"].(map[string][]string)[addedIssuer.provider.URL] = []string{"127.0.0.1/32"}
	addedRaw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	stop = securityProvisioningStart(t, binary, append(securityProvisioningEnvironment(string(addedRaw)), operational...), origin)
	configured, err := client.ListIssuers(t.Context(), securityWireRequest(adminToken, &pb.ListIssuersRequest{Exact: addedIssuer.provider.URL}))
	if err != nil || len(configured.Msg.Issuers) != 1 || !configured.Msg.Issuers[0].Enabled || !configured.Msg.Issuers[0].EnvOwned {
		t.Fatal("supplied issuer addition did not reach native trust", err)
	}
	afterRoles, err := client.ListRoles(t.Context(), securityWireRequest(adminToken, &pb.ListRolesRequest{}))
	if err != nil || len(afterRoles.Msg.Roles) != 1 || !proto.Equal(roles.Msg.Roles[0], afterRoles.Msg.Roles[0]) {
		t.Fatal("issuer configuration created or replaced Roles", err)
	}
	afterUsers, err := client.ListUsers(t.Context(), securityWireRequest(adminToken, &pb.ListUsersRequest{}))
	if err != nil || len(afterUsers.Msg.Users) != 1 || !proto.Equal(users.Msg.Users[0], afterUsers.Msg.Users[0]) {
		t.Fatal("issuer configuration created a Principal or grant", err)
	}
	if _, err := client.GetCurrentPrincipal(t.Context(), securityWireRequest(addedToken, &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated || addedIssuer.fetches.Load() == 0 {
		t.Fatal("configured trust was ignored or automatically enrolled subject", err)
	}
	adminIssuer.client = securityProvisioningBrowserControl(t, adminIssuer)
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: addedIssuer.provider.URL, Subject: "reader"}
	role := &pb.SecurityRole{Id: "added_issuer_writer", Rules: []*pb.SecurityRule{
		{Id: "read", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "added:"}},
		{Id: "write", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, Resource: &pb.SecurityRule_Prefix{Prefix: "added:"}},
	}}
	if _, err := adminIssuer.applyChanges(t, t.Context(), "", []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: role}},
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: role.Id}}},
	}); err != nil {
		t.Fatal("explicit administrator enrollment failed", err)
	}
	if _, err := data.PutVertex(t.Context(), securityWireRequest(addedToken, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "added:proof", Value: &pb.Vertex_String_{String_: "configured profile"}}})); err != nil {
		t.Fatal("new configured API issuer unusable after explicit grant", err)
	}
	retainedUser, err := client.GetUser(t.Context(), securityWireRequest(adminToken, &pb.GetUserRequest{Identity: identity}))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	// Removing a supplied issuer revokes its trust while preserving its native
	// Principal and grants. The original setup JSON cannot reset runtime policy.
	stop = securityProvisioningStart(t, binary, env, origin)
	if _, err := client.GetCurrentPrincipal(t.Context(), securityWireRequest(addedToken, &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("withdrawn issuer credential remained authenticated", err)
	}
	if _, err := data.GetVertex(t.Context(), securityWireRequest(addedToken, &pb.GetVertexRequest{Key: "added:proof"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("withdrawn issuer retained data access", err)
	}
	preserved, err := client.GetUser(t.Context(), securityWireRequest(adminToken, &pb.GetUserRequest{Identity: identity}))
	if err != nil || !proto.Equal(retainedUser.Msg.User, preserved.Msg.User) {
		t.Fatal("current issuer withdrawal reset native Principal/grants", err)
	}
	preservedRoles, err := client.ListRoles(t.Context(), securityWireRequest(adminToken, &pb.ListRolesRequest{}))
	if err != nil || len(preservedRoles.Msg.Roles) != 2 {
		t.Fatal("same supplied initial setup reset later runtime Roles", err)
	}
	stop()
}

func securityProvisioningInvalidEnvironment(env []string, kind string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, "LANTERN_SECURITY_CONFIG_JSON=") {
			result = append(result, entry)
		}
	}
	switch kind {
	case "missing":
	case "empty":
		result = append(result, "LANTERN_SECURITY_CONFIG_JSON=")
	case "malformed":
		result = append(result, `LANTERN_SECURITY_CONFIG_JSON={"issuers":`)
	default:
		panic("unknown invalid input case")
	}
	return result
}

func securityProvisioningSingletonCreate(t *testing.T, data graphv1connect.LanternServiceClient, token string) {
	t.Helper()
	if _, err := data.PutVertices(t.Context(), securityWireRequest(token, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "singleton:tail", Value: &pb.Vertex_String_{String_: "tail"}},
		{Key: "singleton:head", Value: &pb.Vertex_String_{String_: "head"}},
	}})); err != nil {
		t.Fatal("generated singleton failed endpoint setup", err)
	}
	result, err := data.CreateEdges(t.Context(), securityWireRequest(token, &pb.CreateEdgesRequest{Edges: []*pb.Edge{
		{Tail: "singleton:tail", Head: "singleton:head", Weight: 2},
		{Tail: "singleton:tail", Head: "singleton:head", Weight: 4},
		{Tail: "singleton:tail", Head: "singleton:missing", Weight: 1},
	}}))
	want := []pb.CreateEdgeOutcome{pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EDGE_EXISTS, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_ENDPOINT_NOT_LIVE}
	if err != nil || !reflect.DeepEqual(result.Msg.GetOutcomes(), want) {
		t.Fatal("generated singleton changed existing-endpoint Create outcomes", err)
	}
	edge, err := data.GetEdge(t.Context(), securityWireRequest(token, &pb.GetEdgeRequest{Tail: "singleton:tail", Head: "singleton:head"}))
	if err != nil || edge.Msg.GetEdge().GetWeight() != 2 {
		t.Fatal("generated singleton Create overwrote an existing edge", err)
	}
	if _, err := data.GetVertex(t.Context(), securityWireRequest(token, &pb.GetVertexRequest{Key: "singleton:missing"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("failed existing-endpoint Create materialized an endpoint", err)
	}
}

func securityProvisioningFixedCohort(t *testing.T, binary string) {
	t.Helper()
	publisherBinary := filepath.Join(t.TempDir(), "membershippublisher")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", publisherBinary, "./server/cmd/membershippublisher")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("same-source publisher build failed: %v\n%s", err, output)
	}
	issuer := newOIDCWireIssuer(t)
	root := securityProvisioningDirectory(t)
	input := securityProvisioningIntent(t, root, issuer.provider.URL, 3, true, true)
	input["root_ca_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.provider.Certificate().Raw}))
	input["private_origins"] = map[string][]string{issuer.provider.URL: {"127.0.0.1/32"}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	operator := filepath.Join(root, "operator")
	if output, err := securityProvisioningCommand(t, binary, securityProvisioningEnvironment(string(raw)), "provision-security", "--operator-directory", operator); err != nil {
		t.Log(securityProvisioningDiagnostic(input))
		t.Fatalf("fixed cohort provisioning failed: %v\n%s", err, output)
	}
	publisherConfig, publisherState := filepath.Join(operator, "publisher.json"), filepath.Join(operator, "state.json")
	checkPublisher, stopPublisher := securityProvisioningPublisher(t, publisherBinary, publisherConfig, publisherState, "init")
	var environments [][]string
	var origins []string
	for i := 1; i <= 3; i++ {
		ref, _ := json.Marshal(securityProvisioningNodeInput(input, fmt.Sprintf("node-%d", i)))
		env := securityProvisioningEnvironment(string(ref))
		address := securityProvisioningAddress(t)
		_, port, _ := net.SplitHostPort(address)
		env = append(env, "LANTERN_PORT="+port, "LANTERN_METRICS_ADDR=", "LANTERN_BACKUP_ENABLED=false", "LANTERN_BACKUP_RESTORE_ON_START=false", "LANTERN_DRAIN_DELAY_SECONDS=0")
		if output, err := securityProvisioningCommand(t, binary, env, "init-security"); err != nil {
			t.Fatalf("fixed cohort initialization failed: %v\n%s", err, output)
		}
		environments = append(environments, env)
		origins = append(origins, "http://"+address)
	}
	startGroup := func() []func() {
		var waits, stops []func()
		for i, env := range environments {
			wait, stop := securityProvisioningLaunch(t, binary, env, origins[i])
			waits = append(waits, wait)
			stops = append(stops, stop)
		}
		for _, wait := range waits {
			wait()
		}
		return stops
	}
	checkGroup := func() {
		for _, origin := range origins {
			client := graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, origin)
			if _, err := client.GetCurrentPrincipal(t.Context(), securityWireRequest(issuer.token(t, "admin", nil), &pb.GetCurrentPrincipalRequest{})); err != nil {
				t.Fatal("fixed cohort lost original administrator over Connect", err)
			}
		}
	}
	stops := startGroup()
	checkGroup()
	// One bounded real operator interval proves that both signed streams renew
	// under a live generated quorum. Only test-local files and synthetic UTC
	// are observed; no external source probing or shortened security lifetime.
	var targets []string
	previous := map[string][]byte{}
	for i := 1; i <= 3; i++ {
		for _, kind := range []string{"control", "data"} {
			target := filepath.Join(root, "trust", "nodes", fmt.Sprintf("node-%d", i), "membership", kind+".json")
			value, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			targets = append(targets, target)
			previous[target] = value
		}
	}
	renewed := false
	for deadline := time.Now().Add(75 * time.Second); time.Now().Before(deadline); {
		checkPublisher()
		renewed = true
		for _, target := range targets {
			value, err := os.ReadFile(target)
			if err != nil || bytes.Equal(value, previous[target]) {
				renewed = false
				break
			}
		}
		if renewed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !renewed {
		t.Fatal("generated publisher did not renew every fixed control/data target within its bounded interval")
	}
	// Workers independently admit successors; the publisher's successful write
	// itself is not evidence that the current authority accepted the renewal.
	time.Sleep(1200 * time.Millisecond)
	checkGroup()
	for _, stop := range stops {
		stop()
	}
	stopPublisher()
	before := securityProvisioningSnapshot(t, root)
	renewal := filepath.Join(root, "renewal")
	if output, err := securityProvisioningCommand(t, binary, securityProvisioningEnvironment(""), "renew-security", "--operator-directory", operator, "--output", renewal, "--valid-for", "744h"); err != nil {
		t.Fatalf("fixed cohort renewal preparation failed: %v\n%s", err, output)
	}
	afterPreparation := securityProvisioningSnapshot(t, root)
	for path, digest := range before {
		if after := afterPreparation[path]; after != digest {
			t.Fatal("renewal preparation changed existing serving/operator state")
		}
	}
	var publication struct {
		PreviousConfig, NextConfig, PublisherState string
		Files                                      []struct{ Source, Target, SHA256 string }
	}
	plan, err := os.ReadFile(filepath.Join(renewal, "publication.json"))
	if err != nil || json.Unmarshal(plan, &publication) != nil {
		t.Fatal("renewal publication plan missing", err)
	}
	complete, err := os.ReadFile(filepath.Join(renewal, "COMPLETE"))
	if err != nil || strings.TrimSpace(string(complete)) != fmt.Sprintf("%x", sha256.Sum256(plan)) || publication.PreviousConfig != publisherConfig || publication.PublisherState != publisherState {
		t.Fatal("renewal publication completion mismatch", err)
	}
	for _, file := range publication.Files {
		if !strings.HasPrefix(file.Source, renewal+string(filepath.Separator)) || !strings.HasPrefix(file.Target, root+string(filepath.Separator)) {
			t.Fatal("renewal installation escaped isolated test state")
		}
		value, err := os.ReadFile(file.Source)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(value)) != file.SHA256 {
			t.Fatal("renewal candidate digest mismatch", err)
		}
		candidate := file.Target + ".test-candidate"
		if err := os.WriteFile(candidate, value, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(candidate, file.Target); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := securityProvisioningCommand(t, publisherBinary, securityProvisioningEnvironment(""), "--config", publisherConfig, "--state", publisherState, "--mode", "advance-window", "--next-config", publication.NextConfig); err != nil {
		t.Fatalf("retained publisher window did not advance: %v\n%s", err, output)
	}
	checkPublisher, stopPublisher = securityProvisioningPublisher(t, publisherBinary, publication.NextConfig, publisherState, "resume")
	stops = startGroup()
	checkPublisher()
	checkGroup()
	for _, stop := range stops {
		stop()
	}
	stopPublisher()
}

func securityProvisioningPublisher(t *testing.T, binary, config, state, mode string) (func(), func()) {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary, "--config", config, "--state", state, "--mode", mode)
	command.Env = securityProvisioningEnvironment("")
	var logs bytes.Buffer
	command.Stdout, command.Stderr = &logs, &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	joined := false
	check := func() {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			t.Fatalf("operator publisher stopped before maintenance: %v\n%s", err, logs.Bytes())
		default:
		}
	}
	stop := func() {
		t.Helper()
		if joined {
			return
		}
		_ = command.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			joined = true
			if err != nil {
				t.Fatalf("operator publisher failed clean stop: %v\n%s", err, logs.Bytes())
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			joined = true
			t.Fatal("operator publisher failed to join")
		}
	}
	t.Cleanup(stop)
	return check, stop
}

func securityProvisioningBrowserControl(t *testing.T, issuer *oidcControlWireFixture) graphv1connect.LanternSecurityServiceClient {
	t.Helper()
	start := issuer.browserLoginStart(t, nil)
	response := wireBrowserDo(t, issuer.browserRequest(t, http.MethodGet, issuer.browserCallbackPath(t, start, "admin", false), start.Cookies(), ""))
	if response.StatusCode != http.StatusSeeOther {
		t.Fatal("browser bootstrap login failed", response.StatusCode)
	}
	cookies := response.Cookies()
	csrf := browserCookieValue(cookies, "__Host-lantern-csrf")
	return graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: authWireRoundTripper{next: http.DefaultTransport, cookies: cookies, csrf: csrf}}, issuer.server.URL+"/browser")
}

func securityProvisioningBinary(t *testing.T, controlled bool) string {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native provisioning supports Linux and Darwin")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "lantern")
	args := []string{"build", "-o", binary, "./server/cmd"}
	if controlled {
		args = []string{"test", "-c", "-tags=lantern_test_time", "-o", binary, "./server/cmd"}
	}
	command := exec.CommandContext(t.Context(), "go", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("same-source CLI build failed: %v\n%s", err, output)
	}
	return binary
}

func securityProvisioningDirectory(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func securityProvisioningAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func securityProvisioningIntent(t *testing.T, root, issuer string, voters int, browser, api bool) map[string]any {
	t.Helper()
	input, err := securityProvisioningIntentWithAllocator(root, issuer, voters, browser, api, net.Listen)
	if err != nil {
		t.Fatal("reserve independent provisioning topology", err)
	}
	return input
}

func securityProvisioningIntentWithAllocator(root, issuer string, voters int, browser, api bool, listen func(string, string) (net.Listener, error)) (input map[string]any, err error) {
	origins, release, err := reserveSecurityWireOrigins(2*voters, listen)
	if err != nil {
		return nil, err
	}
	// Hold both endpoint families together until the complete intent exists,
	// then release them before the CLI or serving process can bind them.
	defer func() { err = errors.Join(err, release()) }()
	profile := map[string]any{"issuer": issuer, "algorithms": []string{"EdDSA"}}
	if browser {
		profile["client_id"] = "admin"
	}
	if api {
		profile["api_audience"], profile["human_subject_namespace_qualified"] = "api", true
	}
	var nodes []map[string]string
	for i := 1; i <= voters; i++ {
		control, data := origins[2*(i-1)], origins[2*(i-1)+1]
		nodes = append(nodes, map[string]string{"name": fmt.Sprintf("node-%d", i), "control_origin": control, "control_listen": strings.TrimPrefix(control, "https://"), "data_origin": data, "data_listen": strings.TrimPrefix(data, "https://")})
	}
	return map[string]any{"material_root": filepath.Join(root, "trust"), "node": "node-1", "public_origin": "https://admin.example", "persistence_root": filepath.Join(root, "state"), "target_platform": runtime.GOOS, "tls_termination": "proxy", "trusted_proxy_ips": []string{"127.0.0.1"}, "initial_admin": map[string]string{"issuer": issuer, "subject": "admin"}, "nodes": nodes, "issuers": []map[string]any{profile}}, nil
}

func securityProvisioningDiagnostic(input map[string]any) string {
	nodes, ok := input["nodes"].([]map[string]string)
	if !ok {
		return "phase=provision-security-cli topology=unavailable"
	}
	controlOrigins, dataOrigins, origins := make(map[string]struct{}), make(map[string]struct{}), make(map[string]struct{})
	for _, node := range nodes {
		controlOrigins[node["control_origin"]] = struct{}{}
		dataOrigins[node["data_origin"]] = struct{}{}
		origins[node["control_origin"]], origins[node["data_origin"]] = struct{}{}, struct{}{}
	}
	// Only counts leave the helper; malformed origins or unrelated secret-bearing
	// input fields are never rendered, and cross-family duplicates stay visible.
	return fmt.Sprintf("phase=provision-security-cli nodes=%d distinct_control_origins=%d distinct_data_origins=%d distinct_origins=%d", len(nodes), len(controlOrigins), len(dataOrigins), len(origins))
}

func TestSecurityProvisioningIntentTopology(t *testing.T) {
	allocator := new(securityWirePortAllocator)
	input, err := securityProvisioningIntentWithAllocator("private-root", "https://idp.example", 3, true, true, allocator.listen)
	if err != nil {
		t.Fatal(err)
	}
	nodes := input["nodes"].([]map[string]string)
	if len(nodes) != 3 || allocator.calls != 6 || len(allocator.active) != 0 {
		t.Fatal("whole control/data topology was not allocated and released before returning")
	}
	for i, node := range nodes {
		for j, family := range []string{"control", "data"} {
			want := fmt.Sprintf("127.0.0.1:%d", 31000+2*i+j)
			if node[family+"_origin"] != "https://"+want || node[family+"_listen"] != want {
				t.Fatalf("node %d %s endpoint reused an earlier reservation", i+1, family)
			}
		}
	}
	for _, listener := range allocator.listeners {
		if listener.closeCalls != 1 {
			t.Fatalf("Close called %d times, want once", listener.closeCalls)
		}
	}
}

func TestSecurityProvisioningDiagnostic(t *testing.T) {
	secretOrigin := "https://secret-user:secret-password@127.0.0.1:31000/secret-path?secret-query#secret-fragment"
	input := map[string]any{
		"material_root": "secret-material", "issuers": "secret-credentials",
		"nodes": []map[string]string{
			{"name": "secret-node", "control_origin": secretOrigin, "data_origin": "%secret-malformed"},
			{"control_origin": "%secret-malformed", "data_origin": secretOrigin},
		},
	}
	got := securityProvisioningDiagnostic(input)
	want := "phase=provision-security-cli nodes=2 distinct_control_origins=2 distinct_data_origins=2 distinct_origins=2"
	if got != want {
		t.Fatalf("diagnostic = %q, want %q", got, want)
	}
}

func securityProvisioningEnvironment(raw string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "LANTERN_") {
			env = append(env, entry)
		}
	}
	return append(env, "LANTERN_AUTH_MODE=oidc", "LANTERN_SECURITY_CONFIG_JSON="+raw)
}

func securityProvisioningCommand(t *testing.T, binary string, env []string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = env
	return command.CombinedOutput()
}

func securityProvisioningNodeInput(input map[string]any, node string) map[string]any {
	selected := make(map[string]any, len(input))
	for key, value := range input {
		selected[key] = value
	}
	selected["node"] = node
	return selected
}

func securityProvisioningLoad(t *testing.T, input map[string]any, node string) provider.SecurityConfig {
	t.Helper()
	raw, err := json.Marshal(securityProvisioningNodeInput(input, node))
	if err != nil {
		t.Fatal(err)
	}
	config, err := provider.LoadSecurityConfigJSON(string(raw))
	if err != nil {
		t.Fatal("current supplied configuration or immutable material rejected", err)
	}
	return config
}

func securityProvisioningSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("nonprivate generated entry %s mode=%o", relative, info.Mode().Perm())
		}
		if entry.IsDir() {
			result[relative] = "directory"
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = fmt.Sprintf("%x", sha256.Sum256(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func securityProvisioningStart(t *testing.T, binary string, env []string, origin string) func() {
	t.Helper()
	wait, stop := securityProvisioningLaunch(t, binary, env, origin)
	wait()
	return stop
}

// Launch the complete fixed cohort before waiting for quorum. Startup is
// intentionally unavailable while fewer than the original quorum are serving.
func securityProvisioningLaunch(t *testing.T, binary string, env []string, origin string) (func(), func()) {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary)
	command.Env = env
	var logs bytes.Buffer
	command.Stdout, command.Stderr = &logs, &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	joined := false
	stop := func() {
		t.Helper()
		if joined {
			return
		}
		_ = command.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			joined = true
			if err != nil {
				t.Fatalf("controlled server failed clean shutdown: %v\n%s", err, logs.Bytes())
			}
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-done
			joined = true
			t.Fatal("controlled server failed to join")
		}
	}
	t.Cleanup(stop)
	wait := func() {
		client := &http.Client{Timeout: time.Second}
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			select {
			case err := <-done:
				joined = true
				t.Fatalf("controlled server exited before readiness: %v\n%s", err, logs.Bytes())
			default:
			}
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, origin+graphv1connect.LanternSecurityServiceGetAuthCapabilitiesProcedure, strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Connect-Protocol-Version", "1")
			response, err := client.Do(request)
			if err == nil {
				var body struct {
					Ready bool `json:"ready"`
				}
				_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&body)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK && body.Ready {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		stop()
		t.Fatal("controlled server never became ready")
	}
	return wait, stop
}
