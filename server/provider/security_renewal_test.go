package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func renewalFixture(t *testing.T, count int) (SecurityRenewOptions, membershippublisher.Config, string, string) {
	t.Helper()
	clearSecurityEnvironment(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	var input securitySetupJSON
	if err := json.Unmarshal([]byte(inlineSecurityExample()), &input); err != nil {
		t.Fatal(err)
	}
	input.PersistenceRoot, input.TargetPlatform = state, runtime.GOOS
	input.Nodes = nil
	for i := 1; i <= count; i++ {
		input.Nodes = append(input.Nodes, securityNodeJSON{Name: fmt.Sprintf("node-%d", i), ControlOrigin: fmt.Sprintf("https://node-%d:6381", i), DataOrigin: fmt.Sprintf("https://node-%d:6382", i), ControlListen: ":6381", DataListen: ":6382"})
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANTERN_AUTH_MODE", "oidc")
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", string(raw))
	provisioned, operator := filepath.Join(root, "provisioned"), filepath.Join(root, "operator")
	if err := ProvisionSecurity(t.Context(), SecurityProvisionOptions{OutputDirectory: provisioned, OperatorDirectory: operator, ValidFor: time.Hour}); err != nil {
		t.Fatal("provision synthetic fixture", err)
	}
	var config membershippublisher.Config
	if err := json.Unmarshal(renewalRead(t, filepath.Join(operator, "publisher.json")), &config); err != nil {
		t.Fatal(err)
	}
	return SecurityRenewOptions{OperatorDirectory: operator, OutputDirectory: filepath.Join(root, "renewal"), ValidFor: 30 * 24 * time.Hour}, config, provisioned, state
}

func renewalRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func renewalSnapshot(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		files[relative] = raw
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func renewalCertificate(t *testing.T, raw []byte) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatal("invalid leaf PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func TestRenewSecurityPreparesSameIdentityAfterOldLeafExpiry(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			options, previous, provisioned, state := renewalFixture(t, count)
			oldProvisioned, oldOperator := renewalSnapshot(t, provisioned), renewalSnapshot(t, options.OperatorDirectory)
			oldState := renewalSnapshot(t, state)
			now := previous.CleanupBy.Add(48 * time.Hour)
			if err := renewSecurityAt(t.Context(), options, now); err != nil {
				t.Fatal("prepare later same-trust period", err)
			}
			if !reflect.DeepEqual(oldProvisioned, renewalSnapshot(t, provisioned)) || !reflect.DeepEqual(oldOperator, renewalSnapshot(t, options.OperatorDirectory)) || !reflect.DeepEqual(oldState, renewalSnapshot(t, state)) {
				t.Fatal("preparation mutated existing trust, operator custody or runtime state")
			}
			var next membershippublisher.Config
			if err := json.Unmarshal(renewalRead(t, filepath.Join(options.OutputDirectory, "publisher.json")), &next); err != nil {
				t.Fatal(err)
			}
			unchanged := next
			unchanged.End, unchanged.CleanupBy = previous.End, previous.CleanupBy
			if !reflect.DeepEqual(unchanged, previous) || !next.End.After(now) || next.CleanupBy.Sub(next.End) != previous.CleanupBy.Sub(previous.End) {
				t.Fatal("prepared window changed fixed publisher configuration")
			}
			var plan securityRenewalPublication
			planRaw := renewalRead(t, filepath.Join(options.OutputDirectory, "publication.json"))
			if err := json.Unmarshal(planRaw, &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Version != 1 || len(plan.Files) != 4*count || plan.PreviousConfig != filepath.Join(options.OperatorDirectory, "publisher.json") || plan.NextConfig != filepath.Join(options.OutputDirectory, "publisher.json") || plan.PublisherState != filepath.Join(options.OperatorDirectory, "state.json") {
				t.Fatal("incomplete publication plan")
			}
			oldConfigDigest := sha256.Sum256(renewalRead(t, plan.PreviousConfig))
			nextConfigDigest := sha256.Sum256(renewalRead(t, plan.NextConfig))
			if plan.PreviousConfigSHA256 != hex.EncodeToString(oldConfigDigest[:]) || plan.NextConfigSHA256 != hex.EncodeToString(nextConfigDigest[:]) {
				t.Fatal("plan did not bind exact old and next operator configurations")
			}
			digest := sha256.Sum256(planRaw)
			if string(renewalRead(t, filepath.Join(options.OutputDirectory, "COMPLETE"))) != hex.EncodeToString(digest[:])+"\n" {
				t.Fatal("candidate committed a different publication plan")
			}
			for _, file := range plan.Files {
				actual := sha256.Sum256(renewalRead(t, file.Source))
				if file.SHA256 != hex.EncodeToString(actual[:]) {
					t.Fatal("publication source hash mismatch")
				}
				if filepath.Base(file.Target) == "COMPLETE" {
					continue
				}
				if filepath.Base(file.Target) != "security.json" && filepath.Base(file.Target) != "workload.pem" && filepath.Dir(file.Target) != filepath.Join(options.OperatorDirectory, "certificates") {
					t.Fatal("plan attempted a trust key, manifest or custody replacement")
				}
			}
			for i := 1; i <= count; i++ {
				name := fmt.Sprintf("node-%d", i)
				sourceDir := filepath.Join(provisioned, "nodes", name)
				candidateDir := filepath.Join(options.OutputDirectory, "nodes", name)
				oldLeaf := renewalCertificate(t, renewalRead(t, filepath.Join(sourceDir, "workload.pem")))
				newLeaf := renewalCertificate(t, renewalRead(t, filepath.Join(candidateDir, "workload.pem")))
				if !bytes.Equal(oldLeaf.RawSubjectPublicKeyInfo, newLeaf.RawSubjectPublicKeyInfo) || !reflect.DeepEqual(oldLeaf.URIs, newLeaf.URIs) || !reflect.DeepEqual(oldLeaf.DNSNames, newLeaf.DNSNames) || !reflect.DeepEqual(oldLeaf.IPAddresses, newLeaf.IPAddresses) || !newLeaf.NotAfter.After(oldLeaf.NotAfter) || oldLeaf.SerialNumber.Cmp(newLeaf.SerialNumber) == 0 {
					t.Fatal("renewal changed identity or failed to extend leaf")
				}
				if !now.After(oldLeaf.NotAfter) {
					t.Fatal("case did not test expired original leaf")
				}
				var oldDoc, newDoc provisionedSecurityDocument
				if json.Unmarshal(renewalRead(t, filepath.Join(sourceDir, "security.json")), &oldDoc) != nil || json.Unmarshal(renewalRead(t, filepath.Join(candidateDir, "security.json")), &newDoc) != nil {
					t.Fatal("invalid node document")
				}
				var oldAuthority, newAuthority map[string]any
				if json.Unmarshal(oldDoc.Authority, &oldAuthority) != nil || json.Unmarshal(newDoc.Authority, &newAuthority) != nil {
					t.Fatal("invalid authority")
				}
				oldIdentity := oldAuthority["identity"].(map[string]any)
				newIdentity := newAuthority["identity"].(map[string]any)
				newIdentity["certificate_pem"] = oldIdentity["certificate_pem"]
				if !reflect.DeepEqual(oldAuthority, newAuthority) {
					t.Fatal("renewal changed authority beyond leaf certificate")
				}
				newDoc.Authority = oldDoc.Authority
				if !reflect.DeepEqual(oldDoc, newDoc) {
					t.Fatal("renewal changed setup, paths, cursor keys or peer identity")
				}
				candidateRaw := renewalRead(t, filepath.Join(candidateDir, "security.json"))
				var signedDoc provisionedSecurityDocument
				if json.Unmarshal(candidateRaw, &signedDoc) != nil || verifySecurityCompletion(candidateDir, candidateRaw, signedDoc.Authority) != nil {
					t.Fatal("candidate node completion is not authenticated")
				}
			}
			for _, name := range []string{"ca-signing-key.pem", "control-signing-key.pem", "data-signing-key.pem", "state.json"} {
				if _, err := os.Lstat(filepath.Join(options.OutputDirectory, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("operator secret or journal entered candidate", name)
				}
			}
			retained := renewalSnapshot(t, options.OutputDirectory)
			if err := renewSecurityAt(t.Context(), options, now); err == nil {
				t.Fatal("existing candidate was overwritten")
			}
			if !reflect.DeepEqual(retained, renewalSnapshot(t, options.OutputDirectory)) {
				t.Fatal("refused replay changed candidate")
			}
		})
	}
}

func TestRenewSecurityRejectsInvalidRetainedInputsBeforeOutput(t *testing.T) {
	for _, name := range []string{"ca signer", "membership signer", "publisher shape", "short window", "outdated window", "explicit missing config", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			options, previous, _, _ := renewalFixture(t, 1)
			now := previous.CleanupBy.Add(48 * time.Hour)
			switch name {
			case "ca signer":
				if err := os.WriteFile(filepath.Join(options.OperatorDirectory, "ca-signing-key.pem"), []byte("private-sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			case "membership signer":
				if err := os.WriteFile(filepath.Join(options.OperatorDirectory, "control-signing-key.pem"), []byte("private-sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			case "publisher shape":
				previous.Streams[1].Targets = nil
				raw, _ := json.Marshal(previous)
				if err := os.WriteFile(filepath.Join(options.OperatorDirectory, "publisher.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "short window":
				options.ValidFor = time.Minute
			case "outdated window":
				now = previous.End.Add(-2 * time.Hour)
				options.ValidFor = time.Hour
			case "explicit missing config":
				options.PublisherConfig = filepath.Join(options.OperatorDirectory, "missing.json")
			}
			before := renewalSnapshot(t, options.OperatorDirectory)
			ctx := t.Context()
			if name == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			err := renewSecurityAt(ctx, options, now)
			if err == nil || bytes.Contains([]byte(err.Error()), []byte("private-sentinel")) {
				t.Fatal("invalid input accepted or disclosed", err)
			}
			if _, err := os.Lstat(options.OutputDirectory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid retained input created output")
			}
			if !reflect.DeepEqual(before, renewalSnapshot(t, options.OperatorDirectory)) {
				t.Fatal("invalid retained input mutated operator custody")
			}
		})
	}
}

type renewalCancelledAfter struct {
	context.Context
	calls, allowed int
}

func (c *renewalCancelledAfter) Err() error {
	c.calls++
	if c.calls > c.allowed {
		return context.Canceled
	}
	return nil
}

func TestRenewSecurityRetainsInterruptedCandidateWithoutCommit(t *testing.T) {
	options, previous, provisioned, _ := renewalFixture(t, 3)
	oldProvisioned, oldOperator := renewalSnapshot(t, provisioned), renewalSnapshot(t, options.OperatorDirectory)
	ctx := &renewalCancelledAfter{Context: t.Context(), allowed: 6}
	if err := renewSecurityAt(ctx, options, previous.CleanupBy.Add(48*time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatal("expected interruption", err)
	}
	if _, err := os.Lstat(filepath.Join(options.OutputDirectory, "COMPLETE")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted preparation committed")
	}
	if _, err := os.Lstat(filepath.Join(options.OutputDirectory, "nodes", "node-1", "COMPLETE")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted preparation signed completion before complete candidate operator material")
	}
	if _, err := os.Lstat(filepath.Join(options.OutputDirectory, "nodes", "node-1", "security.json")); err != nil {
		t.Fatal("case did not preserve partial candidate", err)
	}
	if !reflect.DeepEqual(oldProvisioned, renewalSnapshot(t, provisioned)) || !reflect.DeepEqual(oldOperator, renewalSnapshot(t, options.OperatorDirectory)) {
		t.Fatal("interruption changed existing trust or operator state")
	}
	before := renewalSnapshot(t, options.OutputDirectory)
	if err := renewSecurityAt(t.Context(), options, previous.CleanupBy.Add(48*time.Hour)); err == nil {
		t.Fatal("partial candidate implicitly repaired")
	}
	if !reflect.DeepEqual(before, renewalSnapshot(t, options.OutputDirectory)) {
		t.Fatal("partial candidate was changed by refused replay")
	}
}

func TestRenewSecurityUsesExplicitCurrentPublisherConfig(t *testing.T) {
	options, previous, _, _ := renewalFixture(t, 1)
	current := previous
	current.End = current.End.Add(24 * time.Hour)
	current.CleanupBy = current.CleanupBy.Add(24 * time.Hour)
	raw, _ := json.Marshal(current)
	selected := filepath.Join(filepath.Dir(options.OutputDirectory), "current-publisher.json")
	if err := os.WriteFile(selected, raw, 0600); err != nil {
		t.Fatal(err)
	}
	options.PublisherConfig = selected
	if err := renewSecurityAt(t.Context(), options, current.CleanupBy.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var plan securityRenewalPublication
	if json.Unmarshal(renewalRead(t, filepath.Join(options.OutputDirectory, "publication.json")), &plan) != nil || plan.PreviousConfig != selected {
		t.Fatal("selected active config was ignored")
	}
	if !bytes.Equal(raw, renewalRead(t, selected)) {
		t.Fatal("selected immutable config changed")
	}
}

func TestRenewSecurityGeneratedPublisherMaintenanceLifecycle(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			options, previous, provisioned, state := renewalFixture(t, count)
			oldConfig := filepath.Join(options.OperatorDirectory, "publisher.json")
			publisherState := filepath.Join(options.OperatorDirectory, "state.json")
			controlPath := filepath.Join(provisioned, "nodes", "node-1", "membership", "control.json")
			dataPath := filepath.Join(provisioned, "nodes", "node-1", "membership", "data.json")
			readControl := func() peerauth.ControlManifest {
				var envelope struct {
					Payload peerauth.ControlManifest `json:"payload"`
				}
				if json.Unmarshal(renewalRead(t, controlPath), &envelope) != nil {
					t.Fatal("invalid control envelope")
				}
				return envelope.Payload
			}
			readData := func() peerauth.Manifest {
				var envelope struct {
					Payload peerauth.Manifest `json:"payload"`
				}
				if json.Unmarshal(renewalRead(t, dataPath), &envelope) != nil {
					t.Fatal("invalid data envelope")
				}
				return envelope.Payload
			}
			initialControl, initialData := readControl(), readData()
			publisher, err := membershippublisher.Open(oldConfig, publisherState, "init")
			if err != nil {
				t.Fatal("initial publisher", err)
			}
			if err := publisher.Step(initialControl.IssuedAt.Add(previous.Interval)); err != nil {
				t.Fatal("first generated renewal", err)
			}
			if err := publisher.Close(); err != nil {
				t.Fatal(err)
			}
			firstControl, firstData := readControl(), readData()
			if firstControl.Version != initialControl.Version+1 || firstData.Version != initialData.Version+1 {
				t.Fatal("initial publication did not advance both streams")
			}
			nativeBefore, journalBefore := renewalSnapshot(t, state), renewalRead(t, publisherState)
			if err := RenewSecurity(t.Context(), options); err != nil {
				t.Fatal("prepare current-time later window", err)
			}
			if !bytes.Equal(journalBefore, renewalRead(t, publisherState)) || !reflect.DeepEqual(nativeBefore, renewalSnapshot(t, state)) {
				t.Fatal("preparation altered publisher or native custody")
			}
			var plan securityRenewalPublication
			if json.Unmarshal(renewalRead(t, filepath.Join(options.OutputDirectory, "publication.json")), &plan) != nil {
				t.Fatal("invalid plan")
			}
			// This applies only this test's synthetic private files, with all owners
			// closed. Production renewal preparation deliberately does not install.
			for _, file := range plan.Files {
				if err := os.WriteFile(file.Target, renewalRead(t, file.Source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := membershippublisher.AdvanceWindow(oldConfig, plan.NextConfig, publisherState); err != nil {
				t.Fatal("advance existing journal", err)
			}
			publisher, err = membershippublisher.Open(plan.NextConfig, publisherState, "resume")
			if err != nil {
				t.Fatal("resume later window", err)
			}
			secondAt := initialControl.IssuedAt.Add(2 * previous.Interval)
			if err := publisher.Step(secondAt); err != nil {
				t.Fatal("renew after certificate adoption", err)
			}
			secondControl, secondData := readControl(), readData()
			if secondControl.Version != firstControl.Version+1 || secondData.Version != firstData.Version+1 || secondControl.Profile.Digest() != initialControl.Profile.Digest() || secondData.Domain != initialData.Domain || !reflect.DeepEqual(secondData.Members, initialData.Members) {
				t.Fatal("maintenance changed fixed trust or revision continuity")
			}
			// Expiry is not a reinitialization request. The intact external operator
			// may publish a higher revision after the former signed interval ended.
			if err := publisher.Step(secondControl.ExpiresAt.Add(time.Minute)); err != nil {
				t.Fatal("intact operator renewal after expired interval", err)
			}
			afterExpiryControl, afterExpiryData := readControl(), readData()
			if afterExpiryControl.Version != secondControl.Version+1 || afterExpiryData.Version != secondData.Version+1 || !afterExpiryControl.IssuedAt.After(secondControl.ExpiresAt) || afterExpiryControl.Profile.Digest() != initialControl.Profile.Digest() || afterExpiryData.Domain != initialData.Domain {
				t.Fatal("expiry renewal reset revisions or trust")
			}
			if err := publisher.Close(); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= count; i++ {
				if _, err := loadProvisionedSecurity(filepath.Join(provisioned, "nodes", fmt.Sprintf("node-%d", i), "security.json")); err != nil {
					t.Fatal("renewed serving document cannot load", err)
				}
			}
			if !reflect.DeepEqual(nativeBefore, renewalSnapshot(t, state)) {
				t.Fatal("maintenance changed native state")
			}
			if stale, err := membershippublisher.Open(oldConfig, publisherState, "resume"); err == nil {
				stale.Close()
				t.Fatal("stale old window resumed after advancement")
			}
			resumed, err := membershippublisher.Open(plan.NextConfig, publisherState, "resume")
			if err != nil {
				t.Fatal("stale attempt damaged retained journal", err)
			}
			if err := resumed.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
