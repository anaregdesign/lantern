package security

import (
	"errors"
	"reflect"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestManagementApplyRetainsDeletionAndInvalidatesSessions(t *testing.T) {
	image := testImage()
	identity := testIdentity()
	identity.Subject = "registered"
	image.Principals = append(image.Principals, Principal{Identity: identity, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	session := testSession()
	session.Identity = identity
	image.Sessions = []Session{session}
	if err := applyManagementChanges(&image, []Change{{Kind: DeletePrincipal, Identity: &identity}}); err != nil {
		t.Fatal(err)
	}
	deleted := image.Principals[len(image.Principals)-1]
	if deleted.State != Deleted || len(deleted.Assignments) != 0 || !image.Sessions[0].Revoked {
		t.Fatal("deletion dropped its marker or retained a session")
	}
	if err := applyManagementChanges(&image, []Change{{Kind: PutPrincipal, Identity: &identity, State: Active}}); err == nil {
		t.Fatal("registration silently resurrected deleted subject")
	}
	image = testImage()
	image.Issuers[0].EnvOwned = true
	if err := applyManagementChanges(&image, []Change{{Kind: DeleteIssuer, IssuerURL: image.Issuers[0].URL}}); err != ErrBootstrapLocked {
		t.Fatal("env Issuer was mutable", err)
	}
}

func TestManagementApplyPreservesWriteOnlyIssuerBinding(t *testing.T) {
	image := testImage()
	issuer := image.Issuers[0]
	issuer.URL = "https://second.example"
	issuer.ConfigRevision = 2
	issuer.SecretRef = "operator_binding"
	image.Issuers = append(image.Issuers, issuer)
	replacement := issuer
	replacement.ConfigRevision = 0
	replacement.SecretRef = ""
	replacement.APIAudience = "changed"
	if err := applyManagementChanges(&image, []Change{{Kind: PutIssuer, Issuer: &replacement, PreserveSecret: true}}); err != nil {
		t.Fatal(err)
	}
	if image.Issuers[1].SecretRef != "operator_binding" || image.Issuers[1].ConfigRevision != 3 {
		t.Fatal("omitted write-only binding was erased")
	}
	if err := applyManagementChanges(&image, []Change{{Kind: PutIssuer, Issuer: &replacement}}); err != nil {
		t.Fatal(err)
	}
	if image.Issuers[1].SecretRef != "" {
		t.Fatal("explicit clear did not remove binding")
	}
}

func TestManagementApplyNativeSecretIntentAndSnapshotIsolation(t *testing.T) {
	image := testImage()
	issuer := Issuer{URL: AppleIssuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{{ID: "ios", Kind: AppleNative, ClientID: "com.example.app", ServiceOrigin: "https://lantern.example", SecretRef: "apple-binding"}}}
	image.Issuers = append(image.Issuers, issuer)
	replacement := issuer.clone()
	replacement.ConfigRevision = 0
	replacement.NativeClients[0].SecretRef = ""
	change := Change{Kind: PutIssuer, Issuer: &replacement, PreserveNativeSecrets: []string{"ios"}}
	if err := change.validate(); err != nil {
		t.Fatal("valid omission intent", err)
	}
	if err := applyManagementChanges(&image, []Change{change}); err != nil {
		t.Fatal(err)
	}
	if image.Issuers[1].ConfigRevision != 2 || image.Issuers[1].NativeClients[0].SecretRef != "apple-binding" {
		t.Fatal("native secret lost")
	}
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	image.Issuers[1].NativeClients[0].ClientID = "mutated"
	read, ok := snapshot.Issuer(AppleIssuer)
	if !ok || read.NativeClients[0].ClientID != "com.example.app" {
		t.Fatal("image aliases committed native profile")
	}
	read.NativeClients[0].ClientID = "mutated-read"
	read, _ = snapshot.Issuer(AppleIssuer)
	if read.NativeClients[0].ClientID != "com.example.app" {
		t.Fatal("read aliases committed profile")
	}
	change.PreserveNativeSecrets = []string{"unknown"}
	if change.validate() == nil {
		t.Fatal("unknown preserved profile accepted")
	}
}

func TestManagementApplyLogPolicyGenerationAndRollback(t *testing.T) {
	image := testImage()
	change := &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelDebug}
	for n, level := range []LogLevel{LogLevelDebug, LogLevelError, LogLevelDebug, LogLevelDebug} {
		change.Level = level
		if err := applyManagementChanges(&image, []Change{{Kind: PutLogPolicy, LogPolicy: change}}); err != nil {
			t.Fatal(err)
		}
		if policy := image.LogPolicy; policy == nil || policy.Generation != uint64(n+1) || policy.Level != level || policy.Version != LogPolicyVersion {
			t.Fatal("generation was not derived from the predecessor")
		}
	}
	change.Level = LogLevelWarn
	if image.LogPolicy.Level != LogLevelDebug {
		t.Fatal("request aliases committed policy")
	}
	prior := *image.LogPolicy
	identity := testIdentity()
	if err := applyManagementChanges(&image, []Change{{Kind: RevokeSessions, Identity: &identity}}); err != nil || *image.LogPolicy != prior {
		t.Fatal("unrelated security operation changed log policy", err)
	}
	image.LogPolicy.Generation = ^uint64(0)
	if err := applyManagementChanges(&image, []Change{{Kind: PutLogPolicy, LogPolicy: change}}); err == nil || image.LogPolicy.Generation != ^uint64(0) || image.LogPolicy.Level != LogLevelDebug {
		t.Fatal("generation overflow changed committed policy", err)
	}
}

func TestManagementApplyOperationalGenerationCASAndRollback(t *testing.T) {
	for _, group := range operationalpolicy.Groups() {
		t.Run(string(group), func(t *testing.T) {
			image := testImage()
			command := operationalPolicyRuntimeCommand(group)
			change := command.Changes[0].OperationalPolicy
			for generation := uint64(1); generation <= 3; generation++ {
				change.ExpectedGeneration = generation - 1
				if err := applyManagementChanges(&image, command.Changes); err != nil {
					t.Fatal(err)
				}
				policy := image.OperationalPolicies.Get(group)
				if policy.Generation != generation || !reflect.DeepEqual(policy.Values, change.Values) {
					t.Fatal("generation or desired meaning changed")
				}
			}
			before := image.OperationalPolicies.Clone()
			change.ExpectedGeneration = 1
			if err := applyManagementChanges(&image, command.Changes); !errors.Is(err, ErrRevisionConflict) || !reflect.DeepEqual(before, *image.OperationalPolicies) {
				t.Fatal("stale per-group CAS changed image", err)
			}
			change.ExpectedGeneration = ^uint64(0)
			if err := applyManagementChanges(&image, command.Changes); err == nil {
				t.Fatal("overflow generation accepted")
			}
			copy := image.OperationalPolicies.Get(group)
			copy.Generation = 100
			if image.OperationalPolicies.Get(group).Generation != 3 {
				t.Fatal("read aliases committed generation")
			}
		})
	}
}

func TestManagementApplyBackupRetentionAcknowledgementBoundToIntent(t *testing.T) {
	image := testImage()
	command := operationalPolicyRuntimeCommand(operationalpolicy.GroupBackup)
	change := command.Changes[0].OperationalPolicy
	change.RetentionReductionAcknowledged = false
	if err := applyManagementChanges(&image, command.Changes); err == nil || image.OperationalPolicies != nil {
		t.Fatal("first finite retention did not require explicit acknowledgement")
	}
	change.Values.Backup.Retain = 0
	if err := applyManagementChanges(&image, command.Changes); err != nil {
		t.Fatal("keep-all adoption refused", err)
	}
	change.ExpectedGeneration = 1
	change.Values.Backup.Retain = 3
	if err := applyManagementChanges(&image, command.Changes); err == nil || image.OperationalPolicies.Backup.Generation != 1 {
		t.Fatal("keep-all to finite reduction lacked acknowledgement")
	}
	change.RetentionReductionAcknowledged = true
	if err := applyManagementChanges(&image, command.Changes); err != nil {
		t.Fatal(err)
	}
	change.ExpectedGeneration = 2
	change.Values.Backup.Retain = 2
	change.RetentionReductionAcknowledged = false
	if err := applyManagementChanges(&image, command.Changes); err == nil {
		t.Fatal("finite retention reduction lacked acknowledgement")
	}
	change.Values.Backup.Retain = 4
	if err := applyManagementChanges(&image, command.Changes); err != nil {
		t.Fatal("non-destructive increase refused", err)
	}
}
