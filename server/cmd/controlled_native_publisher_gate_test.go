//go:build lantern_test_time && (linux || darwin)

package main

// Cross-cutting test-only owner-window composition; no ordinary serving path.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

type controlledNativePublisherInput struct {
	Schema          int                          `json:"schema"`
	Profile         string                       `json:"profile"`
	Revision        string                       `json:"revision"`
	Nonce           string                       `json:"nonce"`
	ConfigPath      string                       `json:"config_path"`
	ConfigSHA256    string                       `json:"config_sha256"`
	StatePath       string                       `json:"state_path"`
	CapacityPath    string                       `json:"capacity_path"`
	PlannedStop     time.Time                    `json:"planned_stop_at"`
	CleanupBy       time.Time                    `json:"cleanup_by"`
	OwnerWindow     *controlledNativeOwnerWindow `json:"owner_window,omitempty"`
	LifetimeSeconds int                          `json:"lifetime_seconds,omitempty"`
	EventsPath      string                       `json:"events_path,omitempty"`
}

func controlledNativePublisherWindow(input controlledNativePublisherInput, now time.Time) bool {
	maximum := controlledNativeOwnerSeconds
	if input.OwnerWindow == nil {
		if input.LifetimeSeconds != 0 || input.EventsPath != "" {
			return false
		}
	} else {
		if !controlledNativeRenewalWindowShape(input.OwnerWindow, input.LifetimeSeconds) {
			return false
		}
		maximum = input.LifetimeSeconds
		maintenance := input.OwnerWindow.Mode == "unattended-maintenance-renewal"
		if maintenance && input.EventsPath != "/publisher-journal/events.jsonl" || !maintenance && input.EventsPath != "" {
			return false
		}
	}
	remaining := input.PlannedStop.Sub(now)
	return remaining >= 15*time.Second && remaining <= time.Duration(maximum)*time.Second && input.CleanupBy.Sub(input.PlannedStop) == time.Duration(controlledNativeCleanupSeconds(input.OwnerWindow))*time.Second
}

type controlledNativePublisherEventFile interface {
	io.Writer
	Sync() error
}

type controlledNativePublisherEvents struct {
	file    controlledNativePublisherEventFile
	limits  controlledNativeTranscriptLimits
	records int
	bytes   int64
}

func (e *controlledNativePublisherEvents) append(raw []byte) error {
	if !e.limits.valid() || len(raw) == 0 || raw[len(raw)-1] != '\n' || len(raw) > e.limits.RowBytes || e.records >= e.limits.Records || e.bytes+int64(len(raw)) > e.limits.Bytes {
		return errors.New("maintenance publisher event bound refused")
	}
	if n, err := e.file.Write(raw); err != nil || n != len(raw) {
		return errors.New("maintenance publisher event write failed")
	}
	if err := e.file.Sync(); err != nil {
		return errors.New("maintenance publisher event sync failed")
	}
	e.records++
	e.bytes += int64(len(raw))
	return nil
}

func controlledNativePublisherCapacity(input controlledNativePublisherInput, config membershippublisher.Config, status membershippublisher.Status) (controlledNativeRenewalCapacity, error) {
	var capacity controlledNativeRenewalCapacity
	if config.Version != 1 || config.Interval != time.Minute || config.Lifetime != peerauth.MaxMembershipLifetime || len(config.Streams) != 2 ||
		!config.End.Equal(input.CleanupBy.Add(5*time.Second)) || !config.CleanupBy.Equal(input.CleanupBy.Add(10*time.Second)) ||
		status.Failure != "none" || !status.TLSCapacityEnd.After(config.CleanupBy.Add(peerauth.ClockMargin)) {
		return capacity, errors.New("owner publisher whole interval refused")
	}
	var control peerauth.ControlManifest
	var data peerauth.Manifest
	var controlKey, dataKey [32]byte
	seen := map[string]bool{}
	for _, stream := range config.Streams {
		if seen[stream.Kind] || stream.Kind != "control" && stream.Kind != "data" || len(stream.Targets) != 3 || len(stream.Certificates) != 3 {
			return capacity, errors.New("owner exact publisher streams refused")
		}
		seen[stream.Kind] = true
		for i, target := range stream.Targets {
			if target != fmt.Sprintf("/targets/node-%d/%s.json", i+1, stream.Kind) {
				return capacity, errors.New("owner exact public membership targets refused")
			}
		}
		keyRaw, err := controlledNativeOwnerRead(stream.KeyFile, 16<<10)
		if err != nil {
			return capacity, err
		}
		block, rest := pem.Decode(keyRaw)
		if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
			return capacity, errors.New("owner publisher signing key encoding refused")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		key, ok := parsed.(ed25519.PrivateKey)
		if err != nil || !ok {
			return capacity, errors.New("owner publisher signing key refused")
		}
		public := key.Public().(ed25519.PublicKey)
		if stream.Kind == "control" {
			var envelope struct {
				Payload peerauth.ControlManifest `json:"payload"`
			}
			if json.Unmarshal(stream.Initial, &envelope) != nil {
				return capacity, errors.New("owner initial control manifest refused")
			}
			control, err = peerauth.VerifyControlManifest(stream.Initial, public, envelope.Payload.Profile.Lineage)
			controlKey = sha256.Sum256(public)
		} else {
			var envelope struct {
				Payload peerauth.Manifest `json:"payload"`
			}
			if json.Unmarshal(stream.Initial, &envelope) != nil {
				return capacity, errors.New("owner initial data manifest refused")
			}
			data, err = peerauth.VerifyManifest(stream.Initial, public, envelope.Payload.Domain)
			dataKey = sha256.Sum256(public)
		}
		if err != nil {
			return capacity, errors.New("owner genuine initial publisher signatures refused")
		}
	}
	if len(control.Profile.Voters) != 3 || len(data.Members) != 3 {
		return capacity, errors.New("owner fixed three-member publisher required")
	}
	profile, domain := control.Profile.Digest(), data.Domain.Digest()
	capacity = controlledNativeRenewalCapacity{Schema: 1, Kind: "controlled-native-provider-renewal-capacity", Revision: input.Revision,
		Nonce: input.Nonce, PlannedStop: input.PlannedStop, CleanupBy: input.CleanupBy, PublisherEnd: config.End,
		PublisherCleanupBy: config.CleanupBy, TLSCapacityEnd: status.TLSCapacityEnd, LeaseSeconds: 600, RefreshSeconds: 60,
		TargetCount: 3, ControlProfileSHA256: hex.EncodeToString(profile[:]), DataDomainSHA256: hex.EncodeToString(domain[:]),
		ControlOperatorSHA256: hex.EncodeToString(controlKey[:]), DataOperatorSHA256: hex.EncodeToString(dataKey[:])}
	if input.OwnerWindow != nil && input.OwnerWindow.Mode == "unattended-maintenance-renewal" {
		if !controlledNativeRenewalWindowShape(input.OwnerWindow, input.LifetimeSeconds) || input.EventsPath != "/publisher-journal/events.jsonl" {
			return capacity, errors.New("maintenance publisher mode refused")
		}
		window := *input.OwnerWindow
		capacity.OwnerWindow, capacity.LifetimeSeconds = &window, input.LifetimeSeconds
	}
	return capacity, nil
}

func TestControlledNativeProviderPublisherChild(t *testing.T) {
	path := os.Getenv("CONTROLLED_NATIVE_PROVIDER_PUBLISHER_INPUT")
	if path == "" {
		t.Skip("explicit controlled native publisher child only")
	}
	expected := os.Getenv("CONTROLLED_NATIVE_PROVIDER_PUBLISHER_INPUT_SHA256")
	raw, err := controlledNativeOwnerRead(path, 16<<10)
	var input controlledNativePublisherInput
	if !controlledGoogleHex(controlledNativeProviderRevision, 20) || !controlledGoogleHex(expected, 32) || err != nil || controlledGoogleDigest(raw) != expected ||
		controlledNativeOwnerDecode(raw, &input) != nil || input.Schema != 1 || input.Profile != controlledNativeProviderProfile ||
		input.Revision != controlledNativeProviderRevision || !controlledGoogleHex(input.Nonce, 32) || !controlledGoogleHex(input.ConfigSHA256, 32) ||
		input.ConfigPath != "/publisher-config.json" || input.StatePath != "/publisher-journal/state.json" || input.CapacityPath != "/publisher-public/capacity.json" ||
		!controlledNativePublisherWindow(input, time.Now().UTC()) {
		t.Fatal("controlled native publisher exact-source input refused")
	}
	configRaw, err := controlledNativeOwnerRead(input.ConfigPath, 64<<10)
	var config membershippublisher.Config
	if err != nil || controlledGoogleDigest(configRaw) != input.ConfigSHA256 || controlledNativeOwnerDecode(configRaw, &config) != nil {
		t.Fatal("controlled native publisher exact configuration refused")
	}
	if controlledNativeOwnerControlDirectory(controlledNativeOwnerControl, false) != nil {
		t.Fatal("controlled native publisher premature stop metadata refused")
	}
	publisher, err := membershippublisher.Open(input.ConfigPath, input.StatePath, "init")
	if err != nil {
		t.Fatal("controlled native publisher ordinary open refused")
	}
	defer publisher.Close()
	if err := publisher.Step(time.Now().UTC()); err != nil {
		t.Fatal("controlled native publisher initial delivery refused")
	}
	status := publisher.Status(time.Now().UTC(), time.Minute)
	capacity, err := controlledNativePublisherCapacity(input, config, status)
	if err != nil {
		t.Fatal("controlled native publisher verified capacity refused")
	}
	capacityRaw, err := json.Marshal(capacity)
	if err != nil {
		t.Fatal("controlled native publisher capacity encoding failed")
	}
	capacityFile, err := os.OpenFile(input.CapacityPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		t.Fatal("controlled native publisher create-only capacity unavailable")
	}
	_, writeErr := capacityFile.Write(capacityRaw)
	if errors.Join(writeErr, capacityFile.Sync(), capacityFile.Close()) != nil {
		t.Fatal("controlled native publisher capacity publication failed")
	}
	var mirror *controlledNativePublisherEvents
	var mirrorFile *os.File
	if input.OwnerWindow != nil && input.OwnerWindow.Mode == "unattended-maintenance-renewal" {
		limits, limitErr := controlledNativeTranscriptLimitsFor(controlledNativeProviderInput{OwnerWindow: input.OwnerWindow, LifetimeSeconds: input.LifetimeSeconds})
		if limitErr != nil {
			t.Fatal("maintenance publisher event horizon refused")
		}
		events, openErr := os.OpenFile(input.EventsPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if openErr != nil {
			t.Fatal("maintenance publisher event destination refused")
		}
		defer events.Close()
		mirrorFile = events
		mirror = &controlledNativePublisherEvents{file: events, limits: limits}
	}
	ownerStopSHA256 := ""
	emit := func(event string, status membershippublisher.Status) error {
		value := map[string]any{"schema": 1, "event": event, "profile": controlledNativeProviderProfile,
			"revision": input.Revision, "nonce": input.Nonce, "pid": os.Getpid(), "capacity_sha256": controlledGoogleDigest(capacityRaw), "status": status}
		if event == "publisher_joined" {
			value["ok"] = true
			value["stopped_at"] = time.Now().UTC()
			value["owner_stop_sha256"] = ownerStopSHA256
		}
		if mirror == nil {
			return json.NewEncoder(os.Stdout).Encode(value)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return errors.New("maintenance publisher event encoding failed")
		}
		raw = append(raw, '\n')
		if err = mirror.append(raw); err != nil {
			return err
		}
		n, err := os.Stdout.Write(raw)
		if err != nil || n != len(raw) {
			return errors.New("maintenance publisher event output failed")
		}
		return nil
	}
	if err := emit("publisher_ready", status); err != nil {
		t.Fatal("controlled native publisher ready output failed")
	}
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithDeadline(ctx, input.CleanupBy)
	defer cancel()
	previous := ""
	err = publisher.RunWithStatus(ctx, func(status membershippublisher.Status) error {
		// Ordinary cancellation is represented only by the explicit joined
		// event below, after publication ownership has actually closed.
		if status.Failure == "stopped" {
			return nil
		}
		versions := status.Failure
		for _, stream := range status.Streams {
			versions += fmt.Sprintf("/%s/%d/%t", stream.Kind, stream.JournalVersion, stream.Pending)
		}
		if versions == previous {
			return nil
		}
		previous = versions
		emitErr := emit("publisher_status", status)
		if status.Failure != "none" {
			return errors.Join(errors.New("controlled native publisher failed closed"), emitErr)
		}
		return emitErr
	}, time.Minute)
	if ctx.Err() == nil || err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("controlled native publisher ended outside owned normal stop")
	}
	// Read and hash the original owner's request while publication ownership
	// is still live. Close ALWAYS runs, including missing/wrong stop metadata.
	ownerStopSHA256, err = controlledNativeOwnerStopAcknowledgement(controlledNativeOwnerControl, input.Revision, input.Nonce, input.PlannedStop, input.CleanupBy)
	closeErr := publisher.Close()
	if err != nil || closeErr != nil {
		t.Fatal("controlled native publisher causal stop or close refused")
	}
	if emit("publisher_joined", publisher.Status(time.Now().UTC(), time.Minute)) != nil {
		t.Fatal("controlled native publisher normal join failed")
	}
	if mirrorFile != nil && mirrorFile.Close() != nil {
		t.Fatal("maintenance publisher events close failed")
	}
}

type controlledNativeUnitEventFile struct {
	bytes.Buffer
	writeErr, syncErr error
	short             bool
}

func (f *controlledNativeUnitEventFile) Write(raw []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.short {
		return len(raw) - 1, nil
	}
	return f.Buffer.Write(raw)
}
func (f *controlledNativeUnitEventFile) Sync() error { return f.syncErr }

func TestControlledNativeProviderPublisherRenewal(t *testing.T) {

	t.Run("explicit maintenance input and bounded event mirror", func(t *testing.T) {
		now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		input := controlledNativePublisherInput{PlannedStop: now.Add(30 * time.Minute), CleanupBy: now.Add(30*time.Minute + 20*time.Second)}
		if !controlledNativePublisherWindow(input, now) {
			t.Fatal("legacy publisher window changed")
		}
		input.PlannedStop = now.Add(7 * 24 * time.Hour)
		input.CleanupBy = input.PlannedStop.Add(20 * time.Second)
		if controlledNativePublisherWindow(input, now) {
			t.Fatal("legacy input silently widened")
		}
		input.OwnerWindow = &controlledNativeOwnerWindow{Mode: "unattended-maintenance-renewal", LeaseSeconds: 600, RefreshSeconds: 60}
		input.LifetimeSeconds = 604800
		input.CleanupBy = input.PlannedStop.Add(120 * time.Second)
		input.EventsPath = "/publisher-journal/events.jsonl"
		if !controlledNativePublisherWindow(input, now) {
			t.Fatal("explicit maintenance publisher refused")
		}
		for name, mutate := range map[string]func(*controlledNativePublisherInput){
			"missing horizon":    func(v *controlledNativePublisherInput) { v.LifetimeSeconds = 0 },
			"larger horizon":     func(v *controlledNativePublisherInput) { v.LifetimeSeconds = 604801 },
			"unknown mode":       func(v *controlledNativePublisherInput) { v.OwnerWindow.Mode = "fallback" },
			"other event path":   func(v *controlledNativePublisherInput) { v.EventsPath = "/targets/events.jsonl" },
			"missing event path": func(v *controlledNativePublisherInput) { v.EventsPath = "" },
			"extended stop": func(v *controlledNativePublisherInput) {
				v.PlannedStop = v.PlannedStop.Add(time.Second)
				v.CleanupBy = v.PlannedStop.Add(120 * time.Second)
			},
			"legacy cleanup":   func(v *controlledNativePublisherInput) { v.CleanupBy = v.PlannedStop.Add(20 * time.Second) },
			"extended cleanup": func(v *controlledNativePublisherInput) { v.CleanupBy = v.PlannedStop.Add(121 * time.Second) },
		} {
			t.Run(name, func(t *testing.T) {
				bad := input
				window := *input.OwnerWindow
				bad.OwnerWindow = &window
				mutate(&bad)
				if controlledNativePublisherWindow(bad, now) {
					t.Fatal("unsafe maintenance input accepted")
				}
			})
		}
		row := []byte("{\"event\":\"publisher_joined\",\"owner_stop_sha256\":\"" + strings.Repeat("a", 64) + "\"}\n")
		limits, _ := controlledNativeTranscriptLimitsFor(controlledNativeProviderInput{OwnerWindow: input.OwnerWindow, LifetimeSeconds: 604800})
		file := &controlledNativeUnitEventFile{}
		mirror := controlledNativePublisherEvents{file: file, limits: limits}
		if err := mirror.append(row); err != nil || !bytes.Equal(file.Bytes(), row) || mirror.records != 1 {
			t.Fatal("exact terminal mirror", err)
		}
		for name, broken := range map[string]*controlledNativeUnitEventFile{"write": {writeErr: errors.New("private UNIT")}, "sync": {syncErr: errors.New("private UNIT")}, "short": {short: true}} {
			t.Run(name, func(t *testing.T) {
				m := controlledNativePublisherEvents{file: broken, limits: limits}
				err := m.append(row)
				if err == nil || strings.Contains(err.Error(), "private UNIT") || m.records != 0 {
					t.Fatal("unsafe event failure accepted/disclosed", err)
				}
			})
		}
		for name, bound := range map[string]controlledNativeTranscriptLimits{"count": {Records: 1, RowBytes: 32 << 10, Bytes: 64 << 10}, "bytes": {Records: 2, RowBytes: 32 << 10, Bytes: int64(len(row))}} {
			t.Run(name, func(t *testing.T) {
				m := controlledNativePublisherEvents{file: &controlledNativeUnitEventFile{}, limits: bound}
				if m.append(row) != nil || m.append(row) == nil {
					t.Fatal("finite mirror quota ignored")
				}
			})
		}
		if mirror.append(append(bytes.Repeat([]byte{'x'}, 32<<10), '\n')) == nil {
			t.Fatal("oversized mirror event admitted")
		}
	})

	synctest.Test(t, func(t *testing.T) {
		// Provisioning deliberately rejects pre-2020 dates. Advance only the
		// bubble's virtual calendar; no system clock or source is consulted.
		time.Sleep(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Sub(time.Now().UTC()))
		now := time.Now().UTC()
		input, original := unitControlledNativeProvider()
		input.PlannedStop, input.CleanupBy = now.Add(30*time.Minute), now.Add(30*time.Minute+20*time.Second)
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		write := func(name string, raw []byte) string {
			t.Helper()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			return path
		}
		image := *original.HistoricalImage
		image.Version = security.ImageVersion
		image.Issuers = slices.Clone(original.Issuers)
		for i := range image.Issuers {
			image.Issuers[i].ConfigRevision = 1
		}
		options := security.ProvisioningOptions{Target: "linux", Now: now, End: now.Add(time.Hour), CleanupBy: now.Add(time.Hour + 20*time.Second), Image: image}
		for i := 1; i <= 3; i++ {
			options.Nodes = append(options.Nodes, security.ProvisioningNode{Name: fmt.Sprintf("node-%d", i), ControlOrigin: fmt.Sprintf("https://127.0.0.1:%d", 17380+i),
				DataOrigin: fmt.Sprintf("https://127.0.0.1:%d", 18380+i), ControlListen: fmt.Sprintf("127.0.0.1:%d", 17380+i),
				StateDir: filepath.Join(dir, fmt.Sprintf("state-%d", i)), RenewableDir: filepath.Join(dir, fmt.Sprintf("leases-%d", i))})
		}
		bundle, err := security.GenerateProvisioning(options)
		if err != nil {
			t.Fatal("ordinary provisioning", err)
		}
		roots := write("roots.pem", bundle.Operator.RootsPEM)
		config := membershippublisher.Config{Version: 1, End: input.CleanupBy.Add(5 * time.Second), CleanupBy: input.CleanupBy.Add(10 * time.Second), Interval: time.Minute, Lifetime: peerauth.MaxMembershipLifetime}
		for _, kind := range []string{"control", "data"} {
			raw, key := bundle.Operator.ControlManifest, bundle.Operator.ControlKeyPEM
			if kind == "data" {
				raw, key = bundle.Operator.DataManifest, bundle.Operator.DataKeyPEM
			}
			stream := membershippublisher.Stream{Kind: kind, KeyFile: write(kind+".key", key), RootsFile: roots, Initial: raw}
			for i, node := range bundle.Nodes {
				stream.Targets = append(stream.Targets, write(fmt.Sprintf("%s-%d.json", kind, i+1), raw))
				stream.Certificates = append(stream.Certificates, write(fmt.Sprintf("%s-cert-%d.pem", kind, i+1), node.CertificatePEM))
			}
			config.Streams = append(config.Streams, stream)
		}
		raw, _ := json.Marshal(config)
		publisher, err := membershippublisher.Open(write("publisher.json", raw), filepath.Join(dir, "journal.json"), "init")
		if err != nil {
			t.Fatal("ordinary publisher open", err)
		}
		defer publisher.Close()
		keyPublic := func(raw []byte) ed25519.PublicKey {
			t.Helper()
			block, _ := pem.Decode(raw)
			key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			return key.(ed25519.PrivateKey).Public().(ed25519.PublicKey)
		}
		var envelope struct {
			Payload peerauth.ControlManifest `json:"payload"`
		}
		if json.Unmarshal(bundle.Operator.ControlManifest, &envelope) != nil {
			t.Fatal("control fixture")
		}
		authority := controlledNativeLeaseAuthority{ControlPath: config.Streams[0].Targets[0], ControlKey: keyPublic(bundle.Operator.ControlKeyPEM), Profile: envelope.Payload.Profile,
			DataPath: config.Streams[1].Targets[0], DataKey: keyPublic(bundle.Operator.DataKeyPEM), Domain: bundle.Nodes[0].DataDomain}
		for _, node := range bundle.Nodes {
			authority.Members = append(authority.Members, node.DataSelf)
		}
		var transcript bytes.Buffer
		for minute := 0; minute <= 30; minute++ {
			at := now.Add(time.Duration(minute) * time.Minute)
			if err := publisher.Step(at); err != nil {
				t.Fatal("real bounded renewal", err)
			}
			cut, err := controlledNativeLeaseCurrent(authority, at)
			if err != nil {
				t.Fatal("real signed target admission", err)
			}
			if err := json.NewEncoder(&transcript).Encode(cut); err != nil {
				t.Fatal(err)
			}
			for _, stream := range config.Streams {
				first, err := os.ReadFile(stream.Targets[0])
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range stream.Targets[1:] {
					raw, err := os.ReadFile(target)
					if err != nil || !bytes.Equal(first, raw) {
						t.Fatal("partial publication", err)
					}
				}
			}
		}
		if count, err := controlledNativeLeaseTranscript(transcript.Bytes(), authority, now, input.PlannedStop); err != nil || count != 31 {
			t.Fatal("real publisher signed interval", count, err)
		}
		// The capacity wrapper admits only the fixed container target mapping;
		// Open/Step above exercise the same streams at owned temporary targets.
		projection := config
		projection.Streams = slices.Clone(config.Streams)
		for i := range projection.Streams {
			projection.Streams[i].Targets = nil
			for node := 1; node <= 3; node++ {
				projection.Streams[i].Targets = append(projection.Streams[i].Targets, fmt.Sprintf("/targets/node-%d/%s.json", node, projection.Streams[i].Kind))
			}
		}
		publisherInput := controlledNativePublisherInput{Revision: input.Revision, Nonce: input.Nonce, PlannedStop: input.PlannedStop, CleanupBy: input.CleanupBy}
		capacity, err := controlledNativePublisherCapacity(publisherInput, projection, publisher.Status(input.PlannedStop, time.Minute))
		if err != nil || controlledNativeCapacityBinding(input, capacity, authority) != nil {
			t.Fatal("actual publisher capacity", err)
		}
		if err := publisher.Step(config.End); !errors.Is(err, membershippublisher.ErrBudget) {
			t.Fatal("publisher authority exceeded fixed End", err)
		}
		// Genuine initial signatures retain their ten-minute cap: no static
		// manifest has been stretched to make the thirty-minute proof pass.
		if _, err := controlledNativeLeaseVerify(authority, input.PlannedStop, bundle.Operator.ControlManifest, bundle.Operator.DataManifest); err == nil {
			t.Fatal("expired original lease accepted")
		}
	})
}
