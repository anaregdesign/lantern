package security

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAuditRedactionAndBounds(t *testing.T) {
	record := AuditRecord{Revision: 2, ChangeID: strings.Repeat("a", 32), IntentDigest: strings.Repeat("b", 64), ActorDigest: redactedDigest(testIdentity()), OccurredAt: time.Now().UTC(), Operation: "security.update", TargetDigests: []string{redactedDigest("issuer:https://private.example/realm")}, Outcome: "committed"}
	if err := validateAudit([]AuditRecord{record}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil || strings.Contains(string(encoded), "private.example") || strings.Contains(string(encoded), "idp.example") || strings.Contains(string(encoded), "admin") {
		t.Fatal("audit retained unredacted identity or destination", err)
	}
	for _, mutate := range []func(*AuditRecord){func(r *AuditRecord) { r.Revision = 0 }, func(r *AuditRecord) { r.ChangeID = "bad" }, func(r *AuditRecord) { r.IntentDigest = "bad" }, func(r *AuditRecord) { r.Operation = "unbounded" }, func(r *AuditRecord) { r.Outcome = "token:secret" }, func(r *AuditRecord) { r.TargetDigests = make([]string, 17) }} {
		invalid := record
		mutate(&invalid)
		if err := validateAudit([]AuditRecord{invalid}); err == nil {
			t.Fatal("invalid audit accepted")
		}
	}
	if err := validateAudit([]AuditRecord{record, record}); err == nil {
		t.Fatal("audit revision order was ambiguous")
	}
}
