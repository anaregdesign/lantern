package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// AuditRecord contains no tokens, secret handles, URLs or raw user identities.
// Target and actor digests permit correlation within authorized audit access.
// The committed record is part of the same signed image as the policy change.
type AuditRecord struct {
	Revision          uint64    `json:"revision"`
	ChangeID          string    `json:"change_id"`
	IntentDigest      string    `json:"intent_digest"`
	ActorDigest       string    `json:"actor_digest"`
	OccurredAt        time.Time `json:"occurred_at"`
	Operation         string    `json:"operation"`
	TargetDigests     []string  `json:"target_digests"`
	AdditionalTargets int       `json:"additional_targets,omitempty"`
	Outcome           string    `json:"outcome"`
}

func redactedDigest(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateAudit(records []AuditRecord) error {
	var previous uint64
	var previousTime time.Time
	for _, record := range records {
		id, err := hex.DecodeString(record.ChangeID)
		if err != nil || len(id) != 16 || record.ChangeID != hex.EncodeToString(id) || string(id) == string(make([]byte, 16)) ||
			record.Revision == 0 || record.Revision <= previous || record.OccurredAt.IsZero() || record.OccurredAt.Before(previousTime) ||
			!validHexDigest(record.IntentDigest) || !validHexDigest(record.ActorDigest) ||
			(record.Operation != "security.update" && record.Operation != "session.issue" && record.Operation != "session.revoke") || record.Outcome != "committed" || len(record.TargetDigests) > 16 || record.AdditionalTargets < 0 || record.AdditionalTargets > MaxTransactionChanges {
			return ErrInvalidImage
		}
		for _, target := range record.TargetDigests {
			if !validHexDigest(target) {
				return ErrInvalidImage
			}
		}
		previous, previousTime = record.Revision, record.OccurredAt
	}
	return nil
}
