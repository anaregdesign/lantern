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
