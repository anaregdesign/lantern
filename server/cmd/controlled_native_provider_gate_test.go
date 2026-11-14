//go:build lantern_test_time && (linux || darwin)

package main

// This sibling composes the ordinary App with real Google/Apple profiles. It
// never enables registration, inserts a user, or substitutes a provider Fetcher.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
	"io"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const controlledNativeProviderProfile = "controlled-real-native-providers"

// These bounds belong only to the explicitly linked test profile. In
// particular, owner-window renewal never changes peerauth's ten-minute lease.
const controlledNativeOwnerSeconds = 30 * 60

type controlledNativeOwnerWindow struct {
	Mode           string `json:"mode"`
	LeaseSeconds   int    `json:"lease_seconds"`
	RefreshSeconds int    `json:"refresh_seconds"`
}

type controlledNativeOwnerCapacityRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Qualified test-package linker value; no serving input can set it.
var controlledNativeProviderRevision = "unprepared"

type controlledNativeProviderInput struct {
	Schema                int                               `json:"schema"`
	Profile               string                            `json:"profile"`
	Mode                  string                            `json:"mode"`
	Revision              string                            `json:"revision"`
	Nonce                 string                            `json:"nonce"`
	ConfigSHA256          string                            `json:"config_sha256"`
	SeedSHA256            string                            `json:"seed_identity_sha256"`
	RolesSHA256           string                            `json:"roles_sha256"`
	ServiceOrigin         string                            `json:"service_origin"`
	GoogleBrowserClientID string                            `json:"browser_client_id"`
	GoogleNativeClientID  string                            `json:"native_client_id"`
	GoogleAndroidClientID string                            `json:"android_client_id"`
	GoogleIOSClientID     string                            `json:"ios_client_id"`
	AppleAndroidClientID  string                            `json:"apple_android_client_id"`
	AppleIOSClientID      string                            `json:"apple_ios_client_id"`
	AndroidPackage        string                            `json:"android_package"`
	ProfileIDs            []string                          `json:"profile_ids"`
	BrowserSecretRef      string                            `json:"browser_secret_ref"`
	AppleAndroidSecretRef string                            `json:"apple_android_secret_ref"`
	AppleIOSSecretRef     string                            `json:"apple_ios_secret_ref"`
	SecretValueSHA256     map[string]string                 `json:"secret_value_sha256"`
	LifetimeSeconds       int                               `json:"lifetime_seconds"`
	PlannedStop           time.Time                         `json:"planned_stop_at"`
	CleanupBy             time.Time                         `json:"cleanup_by"`
	OwnerWindow           *controlledNativeOwnerWindow      `json:"owner_window,omitempty"`
	OwnerCapacity         *controlledNativeOwnerCapacityRef `json:"owner_capacity,omitempty"`
}

func controlledNativeProviderWindowShape(input controlledNativeProviderInput) bool {
	if input.OwnerWindow == nil {
		return input.OwnerCapacity == nil && input.LifetimeSeconds >= 15 && input.LifetimeSeconds <= 540
	}
	window := input.OwnerWindow
	return window.Mode == "membership-renewal" && window.LeaseSeconds == int(peerauth.MaxMembershipLifetime/time.Second) &&
		window.RefreshSeconds == 60 && input.LifetimeSeconds == controlledNativeOwnerSeconds && input.OwnerCapacity != nil &&
		input.OwnerCapacity.Path == "/owner-capacity.json" && controlledGoogleHex(input.OwnerCapacity.SHA256, 32)
}

func decodeControlledNativeProviderInput(raw []byte) (controlledNativeProviderInput, error) {
	var input controlledNativeProviderInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 16<<10 || oidc.ValidateJSON(raw) != nil || decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF ||
		input.Schema != 1 || input.Profile != controlledNativeProviderProfile || input.Mode != "initialize" && input.Mode != "resume" ||
		!controlledGoogleHex(input.Revision, 20) || !controlledGoogleHex(input.Nonce, 32) || !controlledGoogleHex(input.ConfigSHA256, 32) ||
		!controlledGoogleHex(input.SeedSHA256, 32) || !controlledGoogleHex(input.RolesSHA256, 32) ||
		!controlledNativeProviderWindowShape(input) || input.PlannedStop.IsZero() || input.CleanupBy.Sub(input.PlannedStop) != 20*time.Second || len(input.ProfileIDs) != 4 {
		return input, errors.New("controlled native provider input refused")
	}
	identifier := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	unique := map[string]bool{}
	for _, value := range input.ProfileIDs {
		if !identifier.MatchString(value) || unique[value] {
			return input, errors.New("controlled native provider profiles refused")
		}
		unique[value] = true
	}
	unique = map[string]bool{}
	for _, value := range []string{input.BrowserSecretRef, input.AppleAndroidSecretRef, input.AppleIOSSecretRef} {
		if !identifier.MatchString(value) || unique[value] {
			return input, errors.New("controlled native provider secret destinations refused")
		}
		unique[value] = true
	}
	if len(input.SecretValueSHA256) != 3 {
		return input, errors.New("controlled native provider secret hashes refused")
	}
	for ref := range unique {
		if !controlledGoogleHex(input.SecretValueSHA256[ref], 32) {
			return input, errors.New("controlled native provider secret hashes refused")
		}
	}
	googleID := regexp.MustCompile(`^[0-9]{6,20}-[a-z0-9]+\.apps\.googleusercontent\.com$`)
	for _, value := range []string{input.GoogleBrowserClientID, input.GoogleNativeClientID, input.GoogleAndroidClientID, input.GoogleIOSClientID} {
		if !googleID.MatchString(value) {
			return input, errors.New("controlled native provider Google clients refused")
		}
	}
	if input.GoogleNativeClientID == input.GoogleAndroidClientID || input.GoogleNativeClientID == input.GoogleIOSClientID || input.GoogleAndroidClientID == input.GoogleIOSClientID {
		return input, errors.New("controlled native provider presenters must be distinct")
	}
	nativeProject, _, _ := strings.Cut(input.GoogleNativeClientID, "-")
	for _, value := range []string{input.GoogleAndroidClientID, input.GoogleIOSClientID} {
		project, _, _ := strings.Cut(value, "-")
		if project != nativeProject {
			return input, errors.New("controlled native provider native project mismatch")
		}
	}
	origin, err := url.Parse(input.ServiceOrigin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.Host != strings.ToLower(origin.Host) || origin.String() != input.ServiceOrigin {
		return input, errors.New("controlled native provider exact origin refused")
	}
	appleID := regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+$`)
	if !appleID.MatchString(input.AppleAndroidClientID) || !appleID.MatchString(input.AppleIOSClientID) || input.AppleAndroidClientID == input.AppleIOSClientID || !appleID.MatchString(input.AndroidPackage) {
		return input, errors.New("controlled native provider Apple clients refused")
	}
	return input, nil
}

func controlledNativeProviderWindow(input controlledNativeProviderInput) controlledGoogleChildInput {
	return controlledGoogleChildInput{ServiceOrigin: input.ServiceOrigin, LifetimeSeconds: input.LifetimeSeconds,
		PlannedStop: input.PlannedStop, CleanupBy: input.CleanupBy}
}

// The old Google helper is an immutable whole-window verifier, not a provider
// selector. This sibling additionally requires exactly three original members.
func controlledNativeProviderCohort(config provider.SecurityConfig) bool {
	if config.ProvisionedPeer == nil || len(config.ProvisionedPeer.Identity.FixedMembers) != 3 {
		return false
	}
	var authority struct {
		Membership struct {
			Profile struct {
				Voters []json.RawMessage `json:"voters"`
			} `json:"profile"`
		} `json:"membership"`
	}
	return json.Unmarshal(config.AuthorityJSON, &authority) == nil && len(authority.Membership.Profile.Voters) == 3
}

func validateControlledNativeProviderConfig(input controlledNativeProviderInput, config provider.SecurityConfig) error {
	if config.Mode != "oidc" || config.BrowserOrigin != input.ServiceOrigin || config.RootCAPEM != "" ||
		len(config.PrivateOrigins) != 0 || len(config.TrustedProxyIPs) != 0 || config.TLS.CertPEM == "" || config.TLS.KeyPEM == "" || config.TLS.ClientCAPEM != "" ||
		len(config.Issuers) != 2 || len(config.SecretBindings) != 3 {
		return errors.New("controlled native provider public trust boundary refused")
	}
	issuers := map[string]security.Issuer{}
	for _, issuer := range config.Issuers {
		if _, duplicate := issuers[issuer.URL]; duplicate || !issuer.Enabled || issuer.APIEnabled() || issuer.HumanSubjectNamespaceQualified ||
			!slices.Equal(issuer.Algorithms, []string{"RS256"}) || security.ValidateIssuerConfiguration(issuer) != nil {
			return errors.New("controlled native provider Issuer boundary refused")
		}
		issuers[issuer.URL] = issuer
	}
	google, googleFound := issuers[security.GoogleIssuer]
	apple, appleFound := issuers[security.AppleIssuer]
	if !googleFound || !appleFound || google.ClientID != input.GoogleBrowserClientID || google.RedirectURI != input.ServiceOrigin+oidc.CallbackPath(security.GoogleIssuer) ||
		google.SecretRef != input.BrowserSecretRef || len(google.NativeClients) != 2 || apple.BrowserEnabled() || len(apple.NativeClients) != 2 {
		return errors.New("controlled native provider browser/Issuer separation refused")
	}
	for index, presenter := range []string{input.GoogleAndroidClientID, input.GoogleIOSClientID} {
		client, found := google.NativeClient(input.ProfileIDs[index])
		if !found || client.Kind != security.GoogleIDToken || client.ClientID != input.GoogleNativeClientID || client.ServiceOrigin != input.ServiceOrigin ||
			!slices.Equal(client.Presenters, []string{presenter}) || client.SecretRef != "" || client.RedirectURI != "" || client.AndroidPackage != "" {
			return errors.New("controlled native provider Google native profile refused")
		}
	}
	for index, kind := range []string{security.AppleAndroidWeb, security.AppleNative} {
		client, found := apple.NativeClient(input.ProfileIDs[index+2])
		wantClient, wantSecret, wantRedirect, wantPackage := input.AppleAndroidClientID, input.AppleAndroidSecretRef, input.ServiceOrigin+"/auth/native/apple/callback/"+input.ProfileIDs[2], input.AndroidPackage
		if index == 1 {
			wantClient, wantSecret, wantRedirect, wantPackage = input.AppleIOSClientID, input.AppleIOSSecretRef, "", ""
		}
		if !found || client.Kind != kind || client.ClientID != wantClient || client.SecretRef != wantSecret || client.ServiceOrigin != input.ServiceOrigin ||
			client.RedirectURI != wantRedirect || client.AndroidPackage != wantPackage || len(client.Presenters) != 0 {
			return errors.New("controlled native provider Apple native profile refused")
		}
	}
	for _, expected := range []struct{ ref, issuer, client, origin string }{
		{input.BrowserSecretRef, security.GoogleIssuer, input.GoogleBrowserClientID, "https://oauth2.googleapis.com"},
		{input.AppleAndroidSecretRef, security.AppleIssuer, input.AppleAndroidClientID, security.AppleIssuer},
		{input.AppleIOSSecretRef, security.AppleIssuer, input.AppleIOSClientID, security.AppleIssuer},
	} {
		binding, found := config.SecretBindings[expected.ref]
		if !found || binding.Issuer != expected.issuer || binding.ClientID != expected.client || binding.TokenOrigin != expected.origin || binding.Path != "" || binding.Value == "" || controlledGoogleDigest([]byte(binding.Value)) != input.SecretValueSHA256[expected.ref] {
			return errors.New("controlled native provider exact secret destination refused")
		}
	}
	if _, err := oidc.NewSecretRegistry(config.SecretBindings); err != nil {
		return errors.New("controlled native provider secret binding refused")
	}
	image := config.HistoricalImage
	if image == nil || image.BootstrapRevision != 1 || len(image.Principals) != 1 || len(image.Sessions) != 0 || len(image.MachineCredentials) != 0 ||
		image.RegistrationPolicy == nil || image.RegistrationPolicy.Version != security.RegistrationPolicyVersion || image.RegistrationPolicy.Enabled || len(image.RegistrationPolicy.Profiles) != 0 || image.RegistrationPolicy.RoleID != security.PersonalUserRoleID {
		return errors.New("controlled native provider original genesis refused")
	}
	seed := image.Principals[0]
	assignments := []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}, {RoleID: "data_admin"}}
	if seed.Identity.Kind != security.OIDCPrincipal || seed.Identity.Issuer != security.GoogleIssuer || seed.State != security.Active ||
		seed.HumanIssuerConfigRevision != 1 || seed.UserUID != "" || seed.VerifiedEmail != "" || !slices.Equal(seed.Assignments, assignments) ||
		controlledGoogleDigest([]byte(seed.Identity.Issuer+"\x00"+seed.Identity.Subject)) != input.SeedSHA256 {
		return errors.New("controlled native provider verified seed refused")
	}
	roles, err := json.Marshal(image.Roles)
	if err != nil || input.RolesSHA256 != controlledGoogleDefaultRolesDigest() || controlledGoogleDigest(roles) != input.RolesSHA256 {
		return errors.New("controlled native provider exact default Roles refused")
	}
	return nil
}

// Closed diagnostic flags are not authority; true means that check failed.
type controlledNativeTerminalDiagnostics struct {
	appRun       bool
	awaitReady   bool
	leaseFields  bool
	counters     bool
	emit         bool
	monitorStage controlledNativeLeaseFailureStage
}

func (d controlledNativeTerminalDiagnostics) fields() map[string]any {
	return map[string]any{"app_run_failed": d.appRun, "await_ready_failed": d.awaitReady,
		"lease_fields_failed": d.leaseFields, "counters_failed": d.counters,
		"emit_failed": d.emit, "monitor_stage": d.monitorStage.String()}
}

func controlledNativeProviderCaptured(run func() error, output io.Writer) error {
	return runFunctionalMain(true, func() (result error) {
		defer func() {
			if recover() != nil {
				result = errors.New("controlled native provider lifecycle panicked")
			}
		}()
		return run()
	}, output)
}

func controlledNativeProviderRunApp(run func() error) (result error) {
	defer func() {
		if recover() != nil {
			result = errors.New("controlled native provider App panicked")
		}
	}()
	return run()
}

func TestControlledNativeProviderBuildRevision(t *testing.T) {
	expected := os.Getenv("CONTROLLED_NATIVE_PROVIDER_EXPECTED_REVISION")
	if expected == "" {
		t.Skip("explicit pure tagged link check only")
	}
	if !controlledGoogleHex(expected, 20) || controlledNativeProviderRevision != expected {
		t.Fatal("controlled native provider qualified linker binding failed")
	}
}

func TestControlledNativeProviderProfileDescription(t *testing.T) {
	if os.Getenv("CONTROLLED_NATIVE_PROVIDER_DESCRIBE") != "1" {
		t.Skip("explicit controlled native provider build description only")
	}
	if !controlledGoogleHex(controlledNativeProviderRevision, 20) {
		t.Fatal("controlled native provider exact-source build required")
	}
	publicDigest := func(path string, maximum int64) string {
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			t.Fatal("controlled native provider public build input unavailable")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
			t.Fatal("controlled native provider bounded public input required")
		}
		hash := sha256.New()
		count, err := io.Copy(hash, io.LimitReader(file, maximum+1))
		if err != nil || count != info.Size() {
			t.Fatal("controlled native provider public input changed")
		}
		return hex.EncodeToString(hash.Sum(nil))
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("controlled native provider executable identity unavailable")
	}
	description := map[string]any{"schema": 1, "kind": "controlled-real-native-provider-profile-description",
		"revision": controlledNativeProviderRevision, "build_tags": []string{"lantern_test_time"},
		"roles_sha256": controlledGoogleDefaultRolesDigest(), "provider_source": "real-google-apple", "profile": controlledNativeProviderProfile, "native_profile_count": 4, "registration_at_genesis": false, "synthetic_utc": true,
		"binary_sha256": publicDigest(binary, 512<<20), "public_roots_sha256": publicDigest("/etc/ssl/certs/ca-certificates.crt", 2<<20)}
	if os.Getenv("CONTROLLED_NATIVE_PROVIDER_DESCRIBE_OWNER_WINDOW") == "1" {
		description["owner_window"] = controlledNativeOwnerWindow{Mode: "membership-renewal", LeaseSeconds: 600, RefreshSeconds: 60}
		description["owner_window_lifetime_seconds"] = controlledNativeOwnerSeconds
	}
	if json.NewEncoder(os.Stdout).Encode(description) != nil {
		t.Fatal("controlled native provider description output failed")
	}
}

func TestControlledNativeProviderChild(t *testing.T) {
	path := os.Getenv("CONTROLLED_NATIVE_PROVIDER_CHILD_INPUT")
	if path == "" {
		t.Skip("explicit controlled real native provider child only")
	}
	expectedInput := os.Getenv("CONTROLLED_NATIVE_PROVIDER_CHILD_INPUT_SHA256")
	if !controlledGoogleHex(controlledNativeProviderRevision, 20) || !controlledGoogleHex(expectedInput, 32) || !filepath.IsAbs(path) {
		t.Fatal("controlled native provider exact-source build required")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal("controlled native provider private input unavailable")
	}
	info, statErr := file.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	closeErr := file.Close()
	if statErr != nil || readErr != nil || closeErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("controlled native provider private input refused")
	}
	identity, nativeInfo := info.Sys().(*syscall.Stat_t)
	if !nativeInfo || identity.Uid != uint32(os.Getuid()) || identity.Nlink != 1 || controlledGoogleDigest(raw) != expectedInput {
		t.Fatal("controlled native provider private input identity refused")
	}
	input, err := decodeControlledNativeProviderInput(raw)
	if err != nil || input.Revision != controlledNativeProviderRevision || controlledGoogleDigest([]byte(os.Getenv("LANTERN_SECURITY_CONFIG_JSON"))) != input.ConfigSHA256 {
		t.Fatal("controlled native provider exact input/config binding refused")
	}
	config, err := provider.LoadSecurityConfig()
	if err != nil || validateControlledNativeProviderConfig(input, config) != nil {
		t.Fatal("controlled native provider production configuration admission refused")
	}
	var leaseAuthority controlledNativeLeaseAuthority
	var firstLease controlledNativeLeaseCut
	if !controlledNativeProviderCohort(config) {
		t.Fatal("controlled native provider signed whole-window capacity refused")
	}
	if input.OwnerWindow == nil {
		if controlledGoogleCapacity(controlledNativeProviderWindow(input), config, time.Now().UTC()) != nil {
			t.Fatal("controlled native provider signed whole-window capacity refused")
		}
	} else {
		leaseAuthority, firstLease, err = controlledNativeRenewalAdmission(input, config, time.Now().UTC())
		if err != nil {
			t.Fatal("controlled native provider whole renewal interval refused")
		}
	}
	emit := func(event string, fields map[string]any) error {
		fields["event"], fields["profile"], fields["nonce"], fields["pid"] = event, controlledNativeProviderProfile, input.Nonce, os.Getpid()
		fields["revision"], fields["mode"] = controlledNativeProviderRevision, input.Mode
		fields["node"], fields["config_sha256"] = config.Node, input.ConfigSHA256
		fields["authority_profile"] = config.ProvisionedPeer.Identity.ExpectedDomain.CurrentProfile
		if json.NewEncoder(os.Stdout).Encode(fields) != nil {
			return errors.New("controlled native provider diagnostic output failed")
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
		var monitor *controlledNativeLeaseMonitor
		if input.OwnerWindow != nil {
			monitor, err = controlledNativeMonitorLeases(ctx, cancel, input, leaseAuthority, firstLease, nil)
			if err != nil {
				t.Fatal("controlled native provider initialization lease monitor refused")
			}
			defer monitor.close()
		}
		before := time.Now().UTC()
		counts, err := provider.InitializeSecurity(ctx, config)
		fields := map[string]any{"ok": err == nil && controlledGoogleTerminal(counts),
			"host_seed_interval_from": before, "host_seed_interval_until": time.Now().UTC(), "counters": controlledGoogleCounterFields(counts)}
		if monitor != nil {
			leaseFields, leaseErr := monitor.fields(input)
			err = errors.Join(err, leaseErr)
			for key, value := range leaseFields {
				fields[key] = value
			}
			fields["ok"] = err == nil && controlledGoogleTerminal(counts)
		}
		emitErr := emit("initialized", fields)
		if err != nil || emitErr != nil || !controlledGoogleTerminal(counts) {
			t.Fatal("controlled native provider original initialization failed")
		}
		return
	}
	// The parent owns the fixed static or explicit owner ready window and stop.
	// This independent constructor+serving watchdog cannot extend that window.
	ctx, cancel := context.WithDeadline(ctx, input.CleanupBy)
	defer cancel()
	// Reuse the maintained bounded, typed/redacted decision capture. All errors
	// inside this ownership cut return normally so its terminal Drain runs even
	// when construction or serving fails; never t.Fatal/Goexit in this callback.
	terminal := controlledNativeTerminalDiagnostics{}
	err = controlledNativeProviderCaptured(func() error {
		var monitor *controlledNativeLeaseMonitor
		beforeUTC := time.Now().UTC()
		app, cleanup, err := initializeAppWithListeners(ctx, nil)
		if err != nil {
			return errors.New("controlled native provider ordinary resume failed")
		}
		defer cleanup()
		runtime, ok := app.security.(*provider.SecurityRuntime)
		if !ok || runtime.Mode() != "oidc" {
			return errors.New("controlled native provider owned App required")
		}
		if input.OwnerWindow != nil {
			// Parent cancellation starts ordinary App drain; the borrowed clock
			// remains available until the wrapper's final pre-Shutdown witness.
			// This context is bounded by the ORIGINAL cleanup deadline, never a
			// new timeout, clock owner or authority beyond the serving lifecycle.
			nativeCtx, nativeCancel := context.WithDeadline(context.Background(), input.CleanupBy)
			defer nativeCancel()
			monitor, err = controlledNativeMonitorLeases(ctx, cancel, input, leaseAuthority, firstLease, func() (time.Time, time.Time, error) {
				return controlledNativeCertifiedBounds(config, func() (time.Time, time.Time, error) { return runtime.ControlledNativeWindowTimeBounds(nativeCtx) })
			})
			if err != nil {
				return err
			}
			defer monitor.close()
			app.security = controlledNativeObservedLifecycle{securityLifecycle: runtime, monitor: monitor, stop: func() (string, error) {
				return controlledNativeOwnerStopAcknowledgement(controlledNativeOwnerControl, input.Revision, input.Nonce, input.PlannedStop, input.CleanupBy)
			}}
		}
		before := runtime.TimeAcquisitionCounts()
		if err := emit("constructed", map[string]any{"host_seed_interval_from": beforeUTC, "host_seed_interval_until": time.Now().UTC(),
			"counters": controlledGoogleCounterFields(before)}); err != nil {
			terminal.emit = true
			return err
		}
		app.securityWorkers.ObserveReadiness(os.Stdout)
		done := make(chan error, 1)
		go func() {
			runErr := controlledNativeProviderRunApp(func() error { return app.Run(ctx) })
			if runErr != nil {
				cancel()
			}
			done <- runErr
		}()
		readyErr := controlledGoogleAwaitReady(ctx, controlledNativeProviderWindow(input), app.cfg.Net.Port)
		terminal.awaitReady = readyErr != nil
		if readyErr == nil {
			readyErr = emit("ready", map[string]any{"ready_at": time.Now().UTC(), "planned_stop_at": input.PlannedStop})
			terminal.emit = readyErr != nil
		}
		if readyErr != nil {
			cancel()
		}
		err = <-done // App joins workers and closes native authority before return.
		terminal.appRun = err != nil
		cleanup()
		leaseFields := map[string]any{}
		if monitor != nil {
			var leaseErr error
			leaseFields, leaseErr = monitor.fields(input)
			terminal.leaseFields = leaseErr != nil
			terminal.monitorStage = controlledNativeLeaseFailureStage(monitor.failureStage.Load())
			err = errors.Join(err, leaseErr)
		}
		after, repeat := runtime.TimeAcquisitionCounts(), runtime.TimeAcquisitionCounts()
		countersOK := controlledGoogleJoined(before, after, repeat)
		terminal.counters = !countersOK
		accepted := err == nil && readyErr == nil && countersOK
		fields := map[string]any{"ok": accepted, "stopped_at": time.Now().UTC(),
			"counters": controlledGoogleCounterFields(after), "repeat": controlledGoogleCounterFields(repeat)}
		for key, value := range leaseFields {
			fields[key] = value
		}
		fields["failure_flags"] = terminal.fields()
		emitErr := emit("joined", fields)
		terminal.emit = terminal.emit || emitErr != nil
		if !accepted {
			return errors.Join(errors.New("controlled native provider owned App did not join normally"), emitErr)
		}
		return emitErr
	}, os.Stdout)
	if err != nil {
		diagnostic, _ := json.Marshal(terminal.fields())
		t.Fatalf("controlled native provider captured owned lifecycle failed: %s", diagnostic)
	}
}

// Entirely typed UNIT data: no loader, provider, native owner, key or listener.
func unitControlledNativeProvider() (controlledNativeProviderInput, provider.SecurityConfig) {
	stop := time.Date(2030, 1, 1, 0, 5, 0, 0, time.UTC)
	input := controlledNativeProviderInput{Schema: 1, Profile: controlledNativeProviderProfile, Mode: "resume", Revision: strings.Repeat("a", 40),
		Nonce: strings.Repeat("b", 64), ConfigSHA256: strings.Repeat("c", 64), RolesSHA256: controlledGoogleDefaultRolesDigest(),
		ServiceOrigin: "https://unit.invalid:24443", GoogleBrowserClientID: "222222222222-browser.apps.googleusercontent.com",
		GoogleNativeClientID: "111111111111-backend.apps.googleusercontent.com", GoogleAndroidClientID: "111111111111-android.apps.googleusercontent.com",
		GoogleIOSClientID: "111111111111-ios.apps.googleusercontent.com", AppleAndroidClientID: "com.unit.service", AppleIOSClientID: "com.unit.app",
		AndroidPackage: "com.unit.app", ProfileIDs: []string{"google_android", "google_ios", "apple_android", "apple_ios"},
		BrowserSecretRef: "browser", AppleAndroidSecretRef: "apple_android", AppleIOSSecretRef: "apple_ios",
		LifetimeSeconds: 300, PlannedStop: stop, CleanupBy: stop.Add(20 * time.Second)}
	google := security.Issuer{URL: security.GoogleIssuer, Enabled: true, ClientID: input.GoogleBrowserClientID,
		RedirectURI: input.ServiceOrigin + oidc.CallbackPath(security.GoogleIssuer), SecretRef: input.BrowserSecretRef, Algorithms: []string{"RS256"}}
	for index, presenter := range []string{input.GoogleAndroidClientID, input.GoogleIOSClientID} {
		google.NativeClients = append(google.NativeClients, security.NativeClient{ID: input.ProfileIDs[index], Kind: security.GoogleIDToken,
			ClientID: input.GoogleNativeClientID, Presenters: []string{presenter}, ServiceOrigin: input.ServiceOrigin})
	}
	apple := security.Issuer{URL: security.AppleIssuer, Enabled: true, Algorithms: []string{"RS256"}, NativeClients: []security.NativeClient{
		{ID: input.ProfileIDs[2], Kind: security.AppleAndroidWeb, ClientID: input.AppleAndroidClientID, ServiceOrigin: input.ServiceOrigin,
			SecretRef: input.AppleAndroidSecretRef, RedirectURI: input.ServiceOrigin + "/auth/native/apple/callback/" + input.ProfileIDs[2], AndroidPackage: input.AndroidPackage},
		{ID: input.ProfileIDs[3], Kind: security.AppleNative, ClientID: input.AppleIOSClientID, ServiceOrigin: input.ServiceOrigin, SecretRef: input.AppleIOSSecretRef},
	}}
	subject := "UNIT_ONLY_NOT_VERIFIED_BY_PROVIDER"
	input.SeedSHA256 = controlledGoogleDigest([]byte(security.GoogleIssuer + "\x00" + subject))
	bindings := map[string]oidc.SecretBinding{
		input.BrowserSecretRef:      {Issuer: security.GoogleIssuer, ClientID: input.GoogleBrowserClientID, TokenOrigin: "https://oauth2.googleapis.com", Value: "UNIT_BROWSER_SECRET"},
		input.AppleAndroidSecretRef: {Issuer: security.AppleIssuer, ClientID: input.AppleAndroidClientID, TokenOrigin: security.AppleIssuer, Value: "UNIT_APPLE_ANDROID_SECRET"},
		input.AppleIOSSecretRef:     {Issuer: security.AppleIssuer, ClientID: input.AppleIOSClientID, TokenOrigin: security.AppleIssuer, Value: "UNIT_APPLE_IOS_SECRET"},
	}
	input.SecretValueSHA256 = map[string]string{}
	for ref, value := range bindings {
		input.SecretValueSHA256[ref] = controlledGoogleDigest([]byte(value.Value))
	}
	config := provider.SecurityConfig{Mode: "oidc", BrowserOrigin: input.ServiceOrigin, TLS: provider.SecurityTLSConfig{CertPEM: "UNIT_CERT", KeyPEM: "UNIT_KEY"},
		Issuers: []security.Issuer{google, apple}, SecretBindings: bindings,
		HistoricalImage: &security.Image{BootstrapRevision: 1, Roles: controlledGoogleDefaultRoles(),
			RegistrationPolicy: &security.RegistrationPolicy{Version: security.RegistrationPolicyVersion, RoleID: security.PersonalUserRoleID},
			Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: security.GoogleIssuer, Subject: subject},
				State: security.Active, HumanIssuerConfigRevision: 1, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}, {RoleID: "data_admin"}}}}}}
	return input, config
}

func TestControlledNativeProviderContracts(t *testing.T) {
	t.Run("Python producer fixture uses the actual Go decoder", func(t *testing.T) {
		raw, err := os.ReadFile("testdata/controlled-native-provider-input.unit.json")
		if err != nil {
			t.Fatal(err)
		}
		input, err := decodeControlledNativeProviderInput(raw)
		if err != nil {
			t.Fatal("UNIT shared wire fixture refused", err)
		}
		if input.GoogleBrowserClientID != "987654321000-unitgooglebrowser.apps.googleusercontent.com" ||
			input.GoogleNativeClientID != "123456789000-unitgooglenative.apps.googleusercontent.com" ||
			input.GoogleAndroidClientID != "123456789000-unitgoogleandroid.apps.googleusercontent.com" ||
			input.GoogleIOSClientID != "123456789000-unitgoogleios.apps.googleusercontent.com" ||
			!slices.Equal(input.ProfileIDs, []string{"google_android", "google_ios", "apple_android", "apple_ios"}) {
			t.Fatal("UNIT shared Python/Go client projection changed")
		}
	})
	t.Run("exact independent browser/native audiences and four profiles", func(t *testing.T) {
		input, config := unitControlledNativeProvider()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeControlledNativeProviderInput(raw)
		if err != nil || validateControlledNativeProviderConfig(decoded, config) != nil {
			t.Fatal("UNIT exact profile refused", err)
		}
		// Neither list order nor Browser project aliases select native authority.
		config.Issuers[0], config.Issuers[1] = config.Issuers[1], config.Issuers[0]
		if validateControlledNativeProviderConfig(decoded, config) != nil {
			t.Fatal("UNIT issuer order selected authority")
		}
	})
	mutations := []struct {
		name   string
		change func(*controlledNativeProviderInput, *provider.SecurityConfig)
	}{
		{"missing Apple", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) { c.Issuers = c.Issuers[:1] }},
		{"duplicate Issuer", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) { c.Issuers[1] = c.Issuers[0] }},
		{"browser audience used for native", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[0].NativeClients[0].ClientID = i.GoogleBrowserClientID
		}},
		{"wrong presenter", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[0].NativeClients[0].Presenters = []string{i.GoogleIOSClientID}
		}},
		{"Google native secret", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[0].NativeClients[0].SecretRef = i.BrowserSecretRef
		}},
		{"Apple kinds swapped", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[1].NativeClients[0].Kind = security.AppleNative
		}},
		{"Apple redirect changed", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[1].NativeClients[0].RedirectURI = "https://other.invalid/callback"
		}},
		{"Android package changed", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[1].NativeClients[0].AndroidPackage = "com.other.app"
		}},
		{"Apple browser enabled", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[1].ClientID = i.AppleAndroidClientID
			c.Issuers[1].RedirectURI = i.ServiceOrigin + "/auth/callback"
		}},
		{"extra profile", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[0].NativeClients = append(c.Issuers[0].NativeClients, c.Issuers[0].NativeClients[0])
		}},
		{"API audience", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.Issuers[0].APIAudience = "UNIT_API"
		}},
		{"private provider CA", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) { c.RootCAPEM = "UNIT_PRIVATE_CA" }},
		{"private provider origin", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.PrivateOrigins = map[string][]netip.Prefix{"https://accounts.google.com": {netip.MustParsePrefix("127.0.0.0/8")}}
		}},
		{"trusted proxy override", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.TrustedProxyIPs = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
		}},
		{"public origin changed", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.BrowserOrigin = "https://other.invalid"
		}},
		{"extra secret destination", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.SecretBindings["extra"] = c.SecretBindings["browser"]
		}},
		{"secret issuer changed", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			b := c.SecretBindings[i.BrowserSecretRef]
			b.Issuer = security.AppleIssuer
			c.SecretBindings[i.BrowserSecretRef] = b
		}},
		{"secret audience changed", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			b := c.SecretBindings[i.BrowserSecretRef]
			b.ClientID = i.GoogleNativeClientID
			c.SecretBindings[i.BrowserSecretRef] = b
		}},
		{"secret destination changed", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			b := c.SecretBindings[i.AppleIOSSecretRef]
			b.TokenOrigin = "https://other.invalid"
			c.SecretBindings[i.AppleIOSSecretRef] = b
		}},
		{"secret values swapped", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			a, b := c.SecretBindings[i.AppleIOSSecretRef], c.SecretBindings[i.AppleAndroidSecretRef]
			a.Value, b.Value = b.Value, a.Value
			c.SecretBindings[i.AppleIOSSecretRef], c.SecretBindings[i.AppleAndroidSecretRef] = a, b
		}},
		{"secret path fallback", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			b := c.SecretBindings[i.BrowserSecretRef]
			b.Path = "/UNIT_ONLY/secret"
			b.Value = ""
			c.SecretBindings[i.BrowserSecretRef] = b
		}},
		{"registration enabled at genesis", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.RegistrationPolicy.Enabled = true
		}},
		{"wrong registration schema", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.RegistrationPolicy.Version--
		}},
		{"registration profiles at genesis", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.RegistrationPolicy.Profiles = []security.RegistrationProfile{{Issuer: security.GoogleIssuer, Kind: security.NativeRegistrationProfile, ClientProfileID: "google_android"}}
		}},
		{"seed UID fabricated", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals[0].UserUID = strings.Repeat("1", 32)
		}},
		{"seed role missing", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals[0].Assignments = c.HistoricalImage.Principals[0].Assignments[:1]
		}},
		{"seed env-owned lock removed", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals[0].Assignments[0].EnvOwned = false
		}},
		{"extra principal", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Principals = append(c.HistoricalImage.Principals, c.HistoricalImage.Principals[0])
		}},
		{"fresh revision changed", func(_ *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.BootstrapRevision++
		}},
		{"arbitrary matching Role hash", func(i *controlledNativeProviderInput, c *provider.SecurityConfig) {
			c.HistoricalImage.Roles[1].Rules = c.HistoricalImage.Roles[1].Rules[:1]
			r, _ := json.Marshal(c.HistoricalImage.Roles)
			i.RolesSHA256 = controlledGoogleDigest(r)
		}},
	}
	for _, mutate := range mutations {
		t.Run(mutate.name, func(t *testing.T) {
			i, c := unitControlledNativeProvider()
			mutate.change(&i, &c)
			if validateControlledNativeProviderConfig(i, c) == nil {
				t.Fatal("UNIT modified authority accepted")
			}
		})
	}
}

func TestControlledNativeProviderInputBoundaries(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*controlledNativeProviderInput)
	}{
		{"old Google profile", func(i *controlledNativeProviderInput) { i.Profile = controlledGoogleProfile }},
		{"unprepared revision", func(i *controlledNativeProviderInput) { i.Revision = "unprepared" }},
		{"third mode", func(i *controlledNativeProviderInput) { i.Mode = "register" }},
		{"duplicate profile", func(i *controlledNativeProviderInput) { i.ProfileIDs[3] = i.ProfileIDs[0] }},
		{"two profiles", func(i *controlledNativeProviderInput) { i.ProfileIDs = i.ProfileIDs[:2] }},
		{"duplicate secret ref", func(i *controlledNativeProviderInput) { i.AppleIOSSecretRef = i.BrowserSecretRef }},
		{"extra secret hash", func(i *controlledNativeProviderInput) { i.SecretValueSHA256["extra"] = strings.Repeat("1", 64) }},
		{"bad secret hash", func(i *controlledNativeProviderInput) { i.SecretValueSHA256[i.AppleIOSSecretRef] = "PENDING" }},
		{"wrong native project", func(i *controlledNativeProviderInput) {
			i.GoogleIOSClientID = "333333333333-ios.apps.googleusercontent.com"
		}},
		{"insecure origin", func(i *controlledNativeProviderInput) { i.ServiceOrigin = "http://unit.invalid" }},
		{"origin path", func(i *controlledNativeProviderInput) { i.ServiceOrigin += "/path" }},
		{"short lifetime", func(i *controlledNativeProviderInput) { i.LifetimeSeconds = 14 }},
		{"extended lifetime", func(i *controlledNativeProviderInput) { i.LifetimeSeconds = 541 }},
		{"cleanup extension", func(i *controlledNativeProviderInput) { i.CleanupBy = i.CleanupBy.Add(time.Second) }},
	}
	for _, mutate := range mutations {
		t.Run(mutate.name, func(t *testing.T) {
			i, _ := unitControlledNativeProvider()
			mutate.change(&i)
			raw, _ := json.Marshal(i)
			if _, err := decodeControlledNativeProviderInput(raw); err == nil {
				t.Fatal("UNIT malformed input accepted")
			}
		})
	}
	for _, raw := range []string{"", `{}`, `{"schema":1,"schema":2}`, `{"unknown":"UNIT_PRIVATE_SENTINEL"}`} {
		_, err := decodeControlledNativeProviderInput([]byte(raw))
		if err == nil || strings.Contains(fmt.Sprint(err), "UNIT_PRIVATE_SENTINEL") {
			t.Fatal("UNIT ambiguous input or private error accepted")
		}
	}
}

func TestControlledNativeProviderCohortContracts(t *testing.T) {
	_, config := unitControlledNativeProvider()
	config.ProvisionedPeer = &provider.PeerPlaneConfig{}
	// Count-only preflight; cryptographic/whole-window verification remains the
	// unchanged capacity verifier and ordinary native constructor's responsibility.
	config.ProvisionedPeer.Identity.FixedMembers = make([]peerauth.Member, 3)
	config.AuthorityJSON = json.RawMessage(`{"membership":{"profile":{"voters":[{},{},{}]}}}`)
	if !controlledNativeProviderCohort(config) {
		t.Fatal("UNIT three-member shape refused")
	}
	config.ProvisionedPeer.Identity.FixedMembers = config.ProvisionedPeer.Identity.FixedMembers[:2]
	if controlledNativeProviderCohort(config) {
		t.Fatal("UNIT two data members accepted")
	}
	config.ProvisionedPeer.Identity.FixedMembers = make([]peerauth.Member, 3)
	config.AuthorityJSON = json.RawMessage(`{"membership":{"profile":{"voters":[{},{}]}}}`)
	if controlledNativeProviderCohort(config) {
		t.Fatal("UNIT two control members accepted")
	}
}

func TestControlledNativeProviderCaptureFailureFinalizes(t *testing.T) {
	for _, panics := range []bool{false, true} {
		var output bytes.Buffer
		primary := errors.New("UNIT fixed lifecycle failure")
		err := controlledNativeProviderCaptured(func() error {
			decisiontrace.Emit(decisiontrace.Event{Kind: "public_refusal", Stage: "admit", Reason: "cut"})
			return controlledNativeProviderRunApp(func() error {
				if panics {
					panic("UNIT_PRIVATE_PANIC")
				}
				return primary
			})
		}, &output)
		if err == nil || !panics && !errors.Is(err, primary) || strings.Contains(output.String(), "UNIT_PRIVATE_PANIC") || decisiontrace.Enabled() {
			t.Fatal("UNIT diagnostic lifecycle or redaction failed")
		}
		var trace struct {
			Kind    string                `json:"kind"`
			Events  []decisiontrace.Event `json:"events"`
			Dropped uint64                `json:"dropped"`
		}
		if json.Unmarshal(output.Bytes(), &trace) != nil || trace.Kind != "controlled_public_trace" || len(trace.Events) != 1 || trace.Dropped != 0 {
			t.Fatal("UNIT terminal trace lost")
		}
	}
	t.Run("closed-terminal-diagnostics", func(t *testing.T) {
		for _, stage := range []controlledNativeLeaseFailureStage{controlledNativeLeaseFailureNativeBounds, 999} {
			diagnostic := controlledNativeTerminalDiagnostics{appRun: true, awaitReady: true, leaseFields: true,
				counters: true, emit: true, monitorStage: stage}
			fields := diagnostic.fields()
			_, err := json.Marshal(fields)
			want := "native_bounds"
			if stage == 999 {
				want = "redacted"
			}
			if err != nil || len(fields) != 6 || fields["monitor_stage"] != want {
				t.Fatal("closed terminal diagnostic redaction failed")
			}
			for _, key := range []string{"app_run_failed", "await_ready_failed", "lease_fields_failed", "counters_failed", "emit_failed"} {
				if fields[key] != true {
					t.Fatal("terminal failure flag lost")
				}
			}
		}
	})
}

func TestControlledNativeProviderWindowContracts(t *testing.T) {
	input, config := unitControlledNativeProvider()
	config.ProvisionedPeer = &provider.PeerPlaneConfig{}
	// The valid time case reaches the signed-capacity parser and stops there:
	// malformed in-memory authority JSON prevents any file/native/provider work.
	config.AuthorityJSON = json.RawMessage(`not-json`)
	now := input.PlannedStop.Add(-300 * time.Second)
	if err := controlledGoogleCapacity(controlledNativeProviderWindow(input), config, now); err == nil || err.Error() != "controlled Google signed capacity unavailable" {
		t.Fatal("UNIT declared interval did not reach signature prerequisite")
	}
	for _, remaining := range []time.Duration{14 * time.Second, 301 * time.Second} {
		input.PlannedStop = now.Add(remaining)
		input.CleanupBy = input.PlannedStop.Add(20 * time.Second)
		if err := controlledGoogleCapacity(controlledNativeProviderWindow(input), config, now); err == nil || err.Error() != "controlled Google finite window unavailable" {
			t.Fatal("UNIT expired/extended interval reached capacity inputs")
		}
	}
}
