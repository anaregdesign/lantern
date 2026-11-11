//go:build lantern_test_time && (linux || darwin)

package main

// The real-provider component has its own explicit child. It reuses the real
// App and the compile-time local time factory; it never links a fake provider.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
)

const controlledGoogleProfile = "controlled-real-google-component"

// Supplied by the exact-source build ceremony, not by a serving request.
var controlledGoogleRevision = "unprepared"

func TestControlledGoogleBuildRevision(t *testing.T) {
	expected := os.Getenv("CONTROLLED_GOOGLE_EXPECTED_REVISION")
	if expected == "" {
		t.Skip("explicit pure tagged link check only")
	}
	if !controlledGoogleHex(expected, 20) || controlledGoogleRevision != expected {
		t.Fatal("controlled Google qualified test-package linker binding failed")
	}
}

type controlledGoogleChildInput struct {
	Schema          int       `json:"schema"`
	Profile         string    `json:"profile"`
	Mode            string    `json:"mode"`
	Nonce           string    `json:"nonce"`
	ConfigSHA256    string    `json:"config_sha256"`
	SeedSHA256      string    `json:"seed_identity_sha256"`
	RolesSHA256     string    `json:"roles_sha256"`
	ServiceOrigin   string    `json:"service_origin"`
	WebClientID     string    `json:"web_client_id"`
	AndroidClientID string    `json:"android_client_id"`
	IOSClientID     string    `json:"ios_client_id"`
	ProfileIDs      []string  `json:"profile_ids"`
	LifetimeSeconds int       `json:"lifetime_seconds"`
	PlannedStop     time.Time `json:"planned_stop_at"`
	CleanupBy       time.Time `json:"cleanup_by"`
}

func controlledGoogleDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func controlledGoogleHex(value string, size int) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == size && hex.EncodeToString(raw) == value
}

// The ordinary provisioning default is reviewed explicitly for this profile.
// This small test-profile declaration is a refusal fence: a caller cannot
// authorize different Role rules merely by supplying their matching digest.
func controlledGoogleDefaultRoles() []security.Role {
	empty := ""
	return []security.Role{
		{ID: "security_admin", Name: "Security administrator", Rules: []security.PermissionRule{
			{ID: "security-read", Permission: security.Read, Resource: security.SecurityResource},
			{ID: "security-write", Permission: security.Write, Resource: security.SecurityResource},
			{ID: "settings-read", Permission: security.Read, Resource: security.SettingsResource},
			{ID: "settings-write", Permission: security.Write, Resource: security.SettingsResource},
		}},
		{ID: "data_admin", Name: "Data administrator", Rules: []security.PermissionRule{
			{ID: "data-read", Permission: security.Read, Resource: security.DataResource, Prefix: &empty},
			{ID: "data-write", Permission: security.Write, Resource: security.DataResource, Prefix: &empty},
		}},
		security.PersonalUserRole(),
	}
}

func controlledGoogleDefaultRolesDigest() string {
	raw, _ := json.Marshal(controlledGoogleDefaultRoles())
	return controlledGoogleDigest(raw)
}

func decodeControlledGoogleInput(raw []byte) (controlledGoogleChildInput, error) {
	var input controlledGoogleChildInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 16<<10 || oidc.ValidateJSON(raw) != nil || decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF ||
		input.Schema != 1 || input.Profile != controlledGoogleProfile || input.Mode != "initialize" && input.Mode != "resume" ||
		!controlledGoogleHex(input.Nonce, 32) || !controlledGoogleHex(input.ConfigSHA256, 32) ||
		!controlledGoogleHex(input.SeedSHA256, 32) || !controlledGoogleHex(input.RolesSHA256, 32) ||
		input.LifetimeSeconds < 15 || input.LifetimeSeconds > 540 || len(input.ProfileIDs) != 2 ||
		input.ProfileIDs[0] == input.ProfileIDs[1] || input.ServiceOrigin == "" || input.PlannedStop.IsZero() ||
		input.CleanupBy.Sub(input.PlannedStop) != 20*time.Second {
		return input, errors.New("controlled Google child input refused")
	}
	for _, client := range []string{input.WebClientID, input.AndroidClientID, input.IOSClientID} {
		if len(client) < 32 || len(client) > 512 || !strings.HasSuffix(client, ".apps.googleusercontent.com") || strings.ContainsAny(client, " /\t\r\n") {
			return input, errors.New("controlled Google client binding refused")
		}
	}
	if input.WebClientID == input.AndroidClientID || input.WebClientID == input.IOSClientID || input.AndroidClientID == input.IOSClientID {
		return input, errors.New("controlled Google clients must be distinct")
	}
	return input, nil
}

// Native admission still verifies these windows at every use. This extra
// profile preflight reserves the one declared setup/serving/cleanup horizon;
// it never renews a signed document or uses host time as production authority.
func controlledGoogleCapacity(input controlledGoogleChildInput, config provider.SecurityConfig, now time.Time) error {
	if input.PlannedStop.Sub(now) < 15*time.Second || input.PlannedStop.Sub(now) > time.Duration(input.LifetimeSeconds)*time.Second || config.ProvisionedPeer == nil {
		return errors.New("controlled Google finite window unavailable")
	}
	low, high := now.Add(-peerauth.ClockMargin), input.CleanupBy.Add(peerauth.ClockMargin)
	var authority struct {
		Membership struct {
			ManifestFile string                  `json:"manifest_file"`
			PublicKey    []byte                  `json:"public_key"`
			Profile      peerauth.ControlProfile `json:"profile"`
		} `json:"membership"`
		Identity struct {
			Roots       string `json:"roots_pem"`
			Certificate string `json:"certificate_pem"`
		} `json:"identity"`
	}
	if json.Unmarshal(config.AuthorityJSON, &authority) != nil {
		return errors.New("controlled Google signed capacity unavailable")
	}
	read := func(path string) ([]byte, error) {
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
			return nil, errors.New("bounded capacity input required")
		}
		return io.ReadAll(io.LimitReader(f, (1<<20)+1))
	}
	controlRaw, err := read(authority.Membership.ManifestFile)
	if err != nil {
		return errors.New("controlled Google control capacity unavailable")
	}
	control, err := peerauth.VerifyControlManifest(controlRaw, ed25519.PublicKey(authority.Membership.PublicKey), authority.Membership.Profile.Lineage)
	if err != nil || !low.After(control.IssuedAt) || !high.Before(control.ExpiresAt) {
		return errors.New("controlled Google control window insufficient")
	}
	peer := config.ProvisionedPeer.Identity
	dataRaw, err := read(peer.ManifestFile)
	if err != nil {
		return errors.New("controlled Google data capacity unavailable")
	}
	data, err := peerauth.VerifyManifest(dataRaw, ed25519.PublicKey(peer.ExpectedOperatorKey[:]), peer.ExpectedDomain)
	if err != nil || !low.After(data.IssuedAt) || !high.Before(data.ExpiresAt) {
		return errors.New("controlled Google data window insufficient")
	}
	certificates := []string{authority.Identity.Roots, authority.Identity.Certificate, config.TLS.CertPEM}
	for _, path := range []string{peer.CAFile, peer.CertFile} {
		raw, err := read(path)
		if err != nil {
			return errors.New("controlled Google workload certificate unavailable")
		}
		certificates = append(certificates, string(raw))
	}
	for _, raw := range certificates {
		count := 0
		for rest := []byte(raw); len(bytes.TrimSpace(rest)) != 0; {
			block, remaining := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return errors.New("controlled Google certificate encoding refused")
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil || low.Before(cert.NotBefore) || !high.Before(cert.NotAfter) {
				return errors.New("controlled Google certificate window insufficient")
			}
			count++
			rest = remaining
		}
		if count == 0 {
			return errors.New("controlled Google certificate chain unavailable")
		}
	}
	return nil
}

// This is an additional test-profile restriction, never a replacement for the
// production parser, signature/custody checks or runtime authorization.
func validateControlledGoogleConfig(input controlledGoogleChildInput, config provider.SecurityConfig) error {
	if config.Mode != "oidc" || config.BrowserOrigin != input.ServiceOrigin || config.RootCAPEM != "" ||
		len(config.PrivateOrigins) != 0 || len(config.SecretBindings) != 0 || len(config.TrustedProxyIPs) != 0 ||
		config.TLS.CertPEM == "" || config.TLS.KeyPEM == "" || config.TLS.ClientCAPEM != "" || len(config.Issuers) != 1 {
		return errors.New("controlled Google public trust boundary refused")
	}
	issuer := config.Issuers[0]
	if issuer.URL != security.GoogleIssuer || !issuer.Enabled || issuer.ClientID != input.WebClientID || issuer.SecretRef != "" ||
		issuer.APIEnabled() || issuer.HumanSubjectNamespaceQualified || !slices.Equal(issuer.Algorithms, []string{"RS256"}) || len(issuer.NativeClients) != 2 {
		return errors.New("controlled Google issuer boundary refused")
	}
	for index, presenter := range []string{input.AndroidClientID, input.IOSClientID} {
		client, found := issuer.NativeClient(input.ProfileIDs[index])
		if !found || client.Kind != security.GoogleIDToken || client.ServiceOrigin != input.ServiceOrigin ||
			client.ClientID != input.WebClientID || !slices.Equal(client.Presenters, []string{presenter}) ||
			client.SecretRef != "" || client.RedirectURI != "" || client.AndroidPackage != "" {
			return errors.New("controlled Google native profile refused")
		}
	}
	image := config.HistoricalImage
	if image == nil || image.BootstrapRevision != 1 || len(image.Principals) != 1 || len(image.Sessions) != 0 || len(image.MachineCredentials) != 0 ||
		image.RegistrationPolicy == nil || image.RegistrationPolicy.Enabled || len(image.RegistrationPolicy.Profiles) != 0 ||
		image.RegistrationPolicy.RoleID != security.PersonalUserRoleID {
		return errors.New("controlled Google original genesis refused")
	}
	seed := image.Principals[0]
	assignments := []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}, {RoleID: "data_admin"}}
	if seed.Identity.Kind != security.OIDCPrincipal || seed.Identity.Issuer != security.GoogleIssuer || seed.State != security.Active ||
		seed.HumanIssuerConfigRevision != 1 || seed.UserUID != "" || seed.VerifiedEmail != "" || !slices.Equal(seed.Assignments, assignments) ||
		controlledGoogleDigest([]byte(seed.Identity.Issuer+"\x00"+seed.Identity.Subject)) != input.SeedSHA256 {
		return errors.New("controlled Google verified seed binding refused")
	}
	roles, err := json.Marshal(image.Roles)
	if err != nil || input.RolesSHA256 != controlledGoogleDefaultRolesDigest() || controlledGoogleDigest(roles) != input.RolesSHA256 {
		return errors.New("controlled Google reviewed Role genesis refused")
	}
	return nil
}

// Passive same-binary build metadata only; no material/config/provider is read.
// The launcher binds this calculated default digest into its build receipt.
func TestControlledGoogleProfileDescription(t *testing.T) {
	if os.Getenv("CONTROLLED_GOOGLE_DESCRIBE") != "1" {
		t.Skip("explicit controlled Google build description only")
	}
	if !controlledGoogleHex(controlledGoogleRevision, 20) {
		t.Fatal("controlled Google exact-source build required")
	}
	publicDigest := func(path string, maximum int64) string {
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			t.Fatal("controlled Google public build input unavailable")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
			t.Fatal("controlled Google bounded public input required")
		}
		hash := sha256.New()
		count, err := io.Copy(hash, io.LimitReader(file, maximum+1))
		if err != nil || count != info.Size() {
			t.Fatal("controlled Google public input changed")
		}
		return hex.EncodeToString(hash.Sum(nil))
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("controlled Google executable identity unavailable")
	}
	if json.NewEncoder(os.Stdout).Encode(map[string]any{"schema": 1, "kind": "controlled-real-google-profile-description",
		"revision": controlledGoogleRevision, "build_tags": []string{"lantern_test_time"},
		"roles_sha256": controlledGoogleDefaultRolesDigest(), "provider_source": "real-google", "synthetic_utc": true,
		"binary_sha256": publicDigest(binary, 512<<20), "public_roots_sha256": publicDigest("/etc/ssl/certs/ca-certificates.crt", 2<<20)}) != nil {
		t.Fatal("controlled Google description output failed")
	}
}

func controlledGoogleCounterFields(c decisiontrace.AcquisitionCounts) map[string]any {
	return map[string]any{"attempts": c.Attempts, "writes": c.Writes, "installations": c.Installations,
		"in_flight": c.InFlight, "failed": c.Failed, "denied": c.Denied}
}

func controlledGoogleTerminal(c decisiontrace.AcquisitionCounts) bool {
	return c.Attempts > 0 && c.Writes > 0 && c.Installations > 0 && c.Installations <= c.Writes && c.Writes <= c.Attempts &&
		!c.InFlight && !c.Failed && !c.Denied
}

func controlledGoogleJoined(before, after, repeated decisiontrace.AcquisitionCounts) bool {
	return controlledGoogleTerminal(before) && controlledGoogleTerminal(after) && after == repeated &&
		after.Attempts >= before.Attempts && after.Writes >= before.Writes && after.Installations >= before.Installations
}

func controlledGoogleCaptured(run func() error, output io.Writer) error {
	return runFunctionalMain(true, func() (result error) {
		// Preserve bounded fixed diagnostics even for an unexpected fixture
		// panic; neither the panic value nor arbitrary provider data is logged.
		defer func() {
			if recover() != nil {
				result = errors.New("controlled Google lifecycle panicked")
			}
		}()
		return run()
	}, output)
}

func controlledGoogleRunApp(run func() error) (result error) {
	// App.Run owns another goroutine while readiness is observed. Retain the
	// same fixed panic boundary there, so the enclosing diagnostic Drain can
	// finalize a failing lifecycle without exposing a panic value.
	defer func() {
		if recover() != nil {
			result = errors.New("controlled Google App panicked")
		}
	}()
	return run()
}

// Only this node's ordinary auth-exempt Health route is observed here. The
// local backend dial never replaces the production provider Fetcher/DNS/TLS.
func controlledGoogleAwaitReady(ctx context.Context, input controlledGoogleChildInput, port int) error {
	origin, err := url.Parse(input.ServiceOrigin)
	if err != nil || origin.Hostname() == "" {
		return errors.New("controlled Google backend origin refused")
	}
	deadline := time.Now().Add(time.Minute)
	if last := input.PlannedStop.Add(-15 * time.Second); last.Before(deadline) {
		deadline = last
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: origin.Hostname()},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("backend redirect refused") }}
	for ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, input.ServiceOrigin+"/readyz", nil)
		if err != nil {
			return errors.New("controlled Google backend request refused")
		}
		response, err := client.Do(request)
		if err == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("controlled Google native readiness unavailable")
}

// Invoked only by the separate reviewed profile supervisor. No provider or
// native owner is opened until the exact private input and config pass.
func TestControlledGoogleNativeChild(t *testing.T) {
	path := os.Getenv("CONTROLLED_GOOGLE_CHILD_INPUT")
	if path == "" {
		t.Skip("explicit controlled real-Google component child only")
	}
	expectedInput := os.Getenv("CONTROLLED_GOOGLE_CHILD_INPUT_SHA256")
	if !controlledGoogleHex(controlledGoogleRevision, 20) || !controlledGoogleHex(expectedInput, 32) || !filepath.IsAbs(path) {
		t.Fatal("controlled Google exact-source build required")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal("controlled Google private input unavailable")
	}
	info, statErr := file.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	closeErr := file.Close()
	if statErr != nil || readErr != nil || closeErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("controlled Google private input refused")
	}
	identity, nativeInfo := info.Sys().(*syscall.Stat_t)
	if !nativeInfo || identity.Uid != uint32(os.Getuid()) || identity.Nlink != 1 || controlledGoogleDigest(raw) != expectedInput {
		t.Fatal("controlled Google private input identity refused")
	}
	input, err := decodeControlledGoogleInput(raw)
	if err != nil || controlledGoogleDigest([]byte(os.Getenv("LANTERN_SECURITY_CONFIG_JSON"))) != input.ConfigSHA256 {
		t.Fatal("controlled Google exact input/config binding refused")
	}
	config, err := provider.LoadSecurityConfig()
	if err != nil || validateControlledGoogleConfig(input, config) != nil {
		t.Fatal("controlled Google production configuration admission refused")
	}
	if controlledGoogleCapacity(input, config, time.Now().UTC()) != nil {
		t.Fatal("controlled Google signed whole-window capacity refused")
	}
	emit := func(event string, fields map[string]any) error {
		fields["event"], fields["profile"], fields["nonce"], fields["pid"] = event, controlledGoogleProfile, input.Nonce, os.Getpid()
		fields["revision"], fields["mode"] = controlledGoogleRevision, input.Mode
		fields["node"], fields["config_sha256"] = config.Node, input.ConfigSHA256
		fields["authority_profile"] = config.ProvisionedPeer.Identity.ExpectedDomain.CurrentProfile
		if json.NewEncoder(os.Stdout).Encode(fields) != nil {
			return errors.New("controlled Google diagnostic output failed")
		}
		return nil
	}
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if input.Mode == "initialize" {
		deadline := time.Now().Add(time.Minute)
		if last := input.PlannedStop.Add(-15 * time.Second); last.Before(deadline) {
			deadline = last
		}
		ctx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		before := time.Now().UTC()
		counts, err := provider.InitializeSecurity(ctx, config)
		emitErr := emit("initialized", map[string]any{"ok": err == nil && controlledGoogleTerminal(counts),
			"host_seed_interval_from": before, "host_seed_interval_until": time.Now().UTC(), "counters": controlledGoogleCounterFields(counts)})
		if err != nil || emitErr != nil || !controlledGoogleTerminal(counts) {
			t.Fatal("controlled Google original initialization failed")
		}
		return
	}
	// The parent owns the actual 15..540-second ready window and normal stop.
	// This independent constructor+serving watchdog cannot extend that window.
	ctx, cancel := context.WithDeadline(ctx, input.CleanupBy)
	defer cancel()
	// Reuse the maintained bounded, typed/redacted decision capture. All errors
	// inside this ownership cut return normally so its terminal Drain runs even
	// when construction or serving fails; never t.Fatal/Goexit in this callback.
	err = controlledGoogleCaptured(func() error {
		beforeUTC := time.Now().UTC()
		app, cleanup, err := initializeAppWithListeners(ctx, nil)
		if err != nil {
			return errors.New("controlled Google ordinary resume failed")
		}
		defer cleanup()
		runtime, ok := app.security.(*provider.SecurityRuntime)
		if !ok || runtime.Mode() != "oidc" {
			return errors.New("controlled Google owned App required")
		}
		before := runtime.TimeAcquisitionCounts()
		if err := emit("constructed", map[string]any{"host_seed_interval_from": beforeUTC, "host_seed_interval_until": time.Now().UTC(),
			"counters": controlledGoogleCounterFields(before)}); err != nil {
			return err
		}
		app.securityWorkers.ObserveReadiness(os.Stdout)
		done := make(chan error, 1)
		go func() {
			runErr := controlledGoogleRunApp(func() error { return app.Run(ctx) })
			if runErr != nil {
				cancel()
			}
			done <- runErr
		}()
		readyErr := controlledGoogleAwaitReady(ctx, input, app.cfg.Net.Port)
		if readyErr == nil {
			readyErr = emit("ready", map[string]any{"ready_at": time.Now().UTC(), "planned_stop_at": input.PlannedStop})
		}
		if readyErr != nil {
			cancel()
		}
		err = <-done // App joins workers and closes native authority before return.
		cleanup()
		after, repeat := runtime.TimeAcquisitionCounts(), runtime.TimeAcquisitionCounts()
		accepted := err == nil && readyErr == nil && controlledGoogleJoined(before, after, repeat)
		emitErr := emit("joined", map[string]any{"ok": accepted, "stopped_at": time.Now().UTC(),
			"counters": controlledGoogleCounterFields(after), "repeat": controlledGoogleCounterFields(repeat)})
		if !accepted {
			return errors.Join(errors.New("controlled Google owned App did not join normally"), emitErr)
		}
		return emitErr
	}, os.Stdout)
	if err != nil {
		t.Fatal("controlled Google captured owned lifecycle failed")
	}
}

func TestControlledGoogleCounters(t *testing.T) {
	good := decisiontrace.AcquisitionCounts{Attempts: 1, Writes: 1, Installations: 1}
	if !controlledGoogleTerminal(good) || !controlledGoogleJoined(good, good, good) {
		t.Fatal("actual unchanged terminal counter shape refused")
	}
	for _, bad := range []decisiontrace.AcquisitionCounts{{}, {Attempts: 1, Writes: 2, Installations: 1},
		{Attempts: 1, Writes: 1, Installations: 1, InFlight: true}, {Attempts: 1, Writes: 1, Installations: 1, Failed: true},
		{Attempts: 1, Writes: 1, Installations: 1, Denied: true}} {
		if controlledGoogleTerminal(bad) || controlledGoogleJoined(good, bad, bad) {
			t.Fatal("unproven/failed terminal counters accepted")
		}
	}
	changed := good
	changed.Attempts++
	if controlledGoogleJoined(good, good, changed) {
		t.Fatal("unstable post-join counts accepted")
	}
	for _, before := range []decisiontrace.AcquisitionCounts{
		{Attempts: 1, Writes: 1, Installations: 1, InFlight: true},
		{Attempts: 1, Writes: 1, Installations: 1, Failed: true},
		{Attempts: 1, Writes: 1, Installations: 1, Denied: true},
	} {
		if controlledGoogleJoined(before, good, good) {
			t.Fatal("unsettled constructor counters accepted as settled evidence")
		}
	}
}

func TestControlledGoogleCaptureFailureFinalizes(t *testing.T) {
	primary := errors.New("UNIT primary lifecycle failure")
	for _, panics := range []bool{false, true} {
		var output bytes.Buffer
		err := controlledGoogleCaptured(func() error {
			decisiontrace.Emit(decisiontrace.Event{Kind: "public_refusal", Stage: "admit", Reason: "cut"})
			return controlledGoogleRunApp(func() error {
				if panics {
					panic("UNIT private panic sentinel")
				}
				return primary
			})
		}, &output)
		if err == nil || !panics && !errors.Is(err, primary) || strings.Contains(output.String(), "private panic sentinel") || decisiontrace.Enabled() {
			t.Fatal("UNIT failure lost diagnostic ownership or exposed panic content")
		}
		var record struct {
			Kind    string                `json:"kind"`
			Events  []decisiontrace.Event `json:"events"`
			Dropped uint64                `json:"dropped"`
		}
		if json.Unmarshal(output.Bytes(), &record) != nil || record.Kind != "controlled_public_trace" || len(record.Events) != 1 || record.Dropped != 0 {
			t.Fatal("UNIT failing lifecycle lost its terminal bounded trace")
		}
	}
}

func TestControlledGoogleExactDefaultGenesis(t *testing.T) {
	// UNIT-only typed input. It is never a seed ownership or provider proof and
	// this test calls no config loader, native owner, listener or provider.
	newUnit := func() (controlledGoogleChildInput, provider.SecurityConfig) {
		input := controlledGoogleChildInput{ServiceOrigin: "https://unit.invalid:24445",
			WebClientID: "000000000000-unit-web.apps.googleusercontent.com", AndroidClientID: "000000000000-unit-android.apps.googleusercontent.com",
			IOSClientID: "000000000000-unit-ios.apps.googleusercontent.com", ProfileIDs: []string{"google_android", "google_ios"},
			RolesSHA256: controlledGoogleDefaultRolesDigest()}
		subject := "UNIT_ONLY_NOT_PROVIDER_VERIFIED"
		input.SeedSHA256 = controlledGoogleDigest([]byte(security.GoogleIssuer + "\x00" + subject))
		issuer := security.Issuer{URL: security.GoogleIssuer, Enabled: true, ClientID: input.WebClientID, Algorithms: []string{"RS256"}}
		for index, presenter := range []string{input.AndroidClientID, input.IOSClientID} {
			issuer.NativeClients = append(issuer.NativeClients, security.NativeClient{ID: input.ProfileIDs[index], Kind: security.GoogleIDToken,
				ClientID: input.WebClientID, ServiceOrigin: input.ServiceOrigin, Presenters: []string{presenter}})
		}
		config := provider.SecurityConfig{Mode: "oidc", BrowserOrigin: input.ServiceOrigin, TLS: provider.SecurityTLSConfig{CertPEM: "UNIT_CERT", KeyPEM: "UNIT_KEY"},
			Issuers: []security.Issuer{issuer}, HistoricalImage: &security.Image{BootstrapRevision: 1, Roles: controlledGoogleDefaultRoles(),
				RegistrationPolicy: &security.RegistrationPolicy{RoleID: security.PersonalUserRoleID}, Principals: []security.Principal{{
					Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: security.GoogleIssuer, Subject: subject}, State: security.Active,
					HumanIssuerConfigRevision: 1, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}, {RoleID: "data_admin"}},
				}}}}
		return input, config
	}
	input, config := newUnit()
	if err := validateControlledGoogleConfig(input, config); err != nil {
		t.Fatal("UNIT exact declared default refused", err)
	}
	for _, mutate := range []func(*controlledGoogleChildInput, *provider.SecurityConfig){
		func(_ *controlledGoogleChildInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals[0].Assignments = c.HistoricalImage.Principals[0].Assignments[:1]
		},
		func(_ *controlledGoogleChildInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals[0].Assignments[0].EnvOwned = false
		},
		func(_ *controlledGoogleChildInput, c *provider.SecurityConfig) { c.HistoricalImage.BootstrapRevision++ },
		func(_ *controlledGoogleChildInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals[0].HumanIssuerConfigRevision++
		},
		func(_ *controlledGoogleChildInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals[0].UserUID = strings.Repeat("1", 32)
		},
		func(_ *controlledGoogleChildInput, c *provider.SecurityConfig) {
			c.HistoricalImage.RegistrationPolicy.Enabled = true
		},
		func(i *controlledGoogleChildInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Roles[1].Rules = c.HistoricalImage.Roles[1].Rules[:1]
			raw, _ := json.Marshal(c.HistoricalImage.Roles)
			i.RolesSHA256 = controlledGoogleDigest(raw) // A matching arbitrary digest is insufficient.
		},
	} {
		input, config := newUnit()
		mutate(&input, &config)
		if validateControlledGoogleConfig(input, config) == nil {
			t.Fatal("UNIT changed default genesis accepted")
		}
	}
}

// Keep fixed profile failures from accidentally exposing private input values.
func TestControlledGoogleInputFailsBeforeComposition(t *testing.T) {
	for _, raw := range []string{"", `{}`, `{"schema":1,"profile":"controlled-native-serving"}`, `{"schema":1,"schema":2}`, `{"unknown":"secret-sentinel"}`} {
		_, err := decodeControlledGoogleInput([]byte(raw))
		if err == nil || strings.Contains(fmt.Sprint(err), "secret-sentinel") {
			t.Fatal("unbound profile input or private diagnostic leak accepted")
		}
	}
}
