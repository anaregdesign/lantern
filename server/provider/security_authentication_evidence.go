package provider

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// These tagged, request-owned facts do not establish future current admission.
// Token holds the actual opaque producer result. Session never invents JWT
// history. The native cut and admission deadline stay separate from credentials.
type requestAuthenticationEvidence struct {
	kind                                    string
	token                                   oidc.VerifiedIdentity
	session                                 security.Session
	machineIdentity                         security.Identity
	machineCredential                       [32]byte
	machineExpiry                           time.Time
	generation                              [16]byte
	revision                                uint64
	revisionDigest                          [32]byte
	admissionExpiry                         time.Time
	classification                          security.ActorClass
	enrolledIdentity                        security.Identity
	humanNamespaceQualified                 bool
	issuerConfigRevision                    uint64
	issuerConfiguration                     [32]byte
	origin                                  [32]byte
	exactOriginChecked, mutationCSRFChecked bool
}
type authenticationEvidenceKey struct{}

func (requestAuthenticationEvidence) String() string {
	return "[redacted request authentication evidence]"
}
func requestEvidenceCut(revision *security.Revision, expiry time.Time) requestAuthenticationEvidence {
	return requestAuthenticationEvidence{generation: revision.Generation(), revision: revision.Sequence(), revisionDigest: revision.Digest(), admissionExpiry: expiry}
}
func withRequestAuthenticationEvidence(ctx context.Context, evidence requestAuthenticationEvidence) context.Context {
	return context.WithValue(ctx, authenticationEvidenceKey{}, evidence)
}
func authenticationEvidenceFromContext(ctx context.Context) (requestAuthenticationEvidence, bool) {
	evidence, ok := ctx.Value(authenticationEvidenceKey{}).(requestAuthenticationEvidence)
	return evidence, ok
}
func requestCredentialCommitment(domain, raw string) [32]byte {
	return sha256.Sum256([]byte("lantern/authentication/" + domain + "/v1\x00" + raw))
}

// completeOperationAuthentication is the sole production adapter into the
// purpose owner. Arbitrary factual structs must never substitute this producer.
func (r *SecurityRuntime) completeOperationAuthentication(ctx context.Context, id [32]byte, current *security.Revision, now time.Time) error {
	evidence, ok := authenticationEvidenceFromContext(ctx)
	if !ok || evidence.kind != "token" {
		return security.ErrOperationAuthorization
	}
	event := evidence.token.Evidence()
	if event.Mode != "code" || event.Code.Flow != "operation" || event.Code.AuthorizationID != id {
		return security.ErrOperationAuthorization
	}
	return r.authorizations.Complete(id, event, current, now)
}
