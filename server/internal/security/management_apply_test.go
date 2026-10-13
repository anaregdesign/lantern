package security

import "testing"

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
