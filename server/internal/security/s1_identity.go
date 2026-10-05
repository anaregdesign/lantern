package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
)

// S1Version is a new, unwired contract. It does not reinterpret LNSEC03 IDs.
const S1Version uint16 = 1

var ErrS1Contract = errors.New("invalid S1 contract")

// FullChangeID is scoped across restored origins and retry namespaces. S2 must
// establish namespace ownership/fencing; a nonce alone is never an ID.
type FullChangeID struct {
	Version   uint16
	Domain    [32]byte
	Cohort    [32]byte
	Namespace uint64
	Nonce     [16]byte
}

func (id FullChangeID) valid() bool {
	return id.Version == S1Version && id.Domain != [32]byte{} && id.Cohort != [32]byte{} && id.Namespace != 0 && id.Nonce != [16]byte{}
}

// SemanticCut binds the full typed image, lineage, independently verified
// authorization fences, generation and semantic ancestry. Control NOOPs,
// witness replacement and outcome bookkeeping do not change this cut.
type SemanticCut struct {
	Version    uint16
	Domain     [32]byte
	Cohort     [32]byte
	Generation [16]byte
	Sequence   uint64
	Previous   [32]byte
	Projection [32]byte
	Frontier   [32]byte
	Fences     [32]byte
}

func (c SemanticCut) valid() bool {
	return c.Version == S1Version && c.Domain != [32]byte{} && c.Cohort != [32]byte{} && c.Generation != [16]byte{} && c.Sequence != 0 && c.Projection != [32]byte{} && c.Frontier != [32]byte{} && c.Fences != [32]byte{}
}

const (
	S1Management    = "management"
	S1IssueSession  = "session.issue"
	S1RevokeSession = "session.revoke"
)

// S1Command contains only business meaning. Session times fix original expiry;
// they are not sampled at Apply. Generic step-up/purpose approval is not a
// session command. Unlisted command and Change variants fail closed.
type S1Command struct {
	Kind           string
	Changes        []Change
	Session        *Session
	ReplacesDigest string
	SessionDigest  string
	SessionLineage uint64
}

// OperationIdentity owns exact canonical bytes, rather than mutable request
// pointers. Actor, ordered contents/options and original observation all bind
// retries. Transport origin, credentials and replaceable witnesses are absent.
type OperationIdentity struct {
	actor     Identity
	reviewed  SemanticCut
	canonical string
	digest    [32]byte
}

func NewS1Operation(actor Identity, reviewed SemanticCut, command S1Command, limits PolicyLimits) (OperationIdentity, error) {
	if !actor.valid() || !reviewed.valid() {
		return OperationIdentity{}, ErrS1Contract
	}
	if err := validateS1Command(command, limits); err != nil {
		return OperationIdentity{}, err
	}
	encoded, err := json.Marshal(struct {
		Version  uint16
		Actor    Identity
		Reviewed SemanticCut
		Command  S1Command
	}{S1Version, actor, reviewed, command})
	if err != nil || len(encoded) > MaxImageBytes {
		return OperationIdentity{}, ErrS1Contract
	}
	encoded = append([]byte("lantern/security/s1/operation\x00"), encoded...)
	return OperationIdentity{actor, reviewed, string(encoded), sha256.Sum256(encoded)}, nil
}

func (o OperationIdentity) Actor() Identity       { return o.actor }
func (o OperationIdentity) Reviewed() SemanticCut { return o.reviewed }
func (o OperationIdentity) Digest() [32]byte      { return o.digest }
func (o OperationIdentity) Encode() []byte        { return []byte(o.canonical) }

func (o OperationIdentity) command() (S1Command, error) {
	prefix := []byte("lantern/security/s1/operation\x00")
	if !bytes.HasPrefix([]byte(o.canonical), prefix) || o.digest != sha256.Sum256([]byte(o.canonical)) {
		return S1Command{}, ErrS1Contract
	}
	var envelope struct {
		Version  uint16
		Actor    Identity
		Reviewed SemanticCut
		Command  S1Command
	}
	if err := json.Unmarshal([]byte(o.canonical)[len(prefix):], &envelope); err != nil || envelope.Version != S1Version || envelope.Actor != o.actor || envelope.Reviewed != o.reviewed {
		return S1Command{}, ErrS1Contract
	}
	return envelope.Command, nil
}

func validateS1Command(c S1Command, limits PolicyLimits) error {
	switch c.Kind {
	case S1Management:
		if len(c.Changes) == 0 || len(c.Changes) > MaxTransactionChanges || c.Session != nil || c.ReplacesDigest != "" || c.SessionDigest != "" || c.SessionLineage != 0 {
			return ErrS1Contract
		}
		roles := make([]Role, 0, len(c.Changes))
		seen := make(map[string]bool, len(c.Changes))
		for _, change := range c.Changes {
			if err := change.validate(); err != nil {
				return err
			}
			if change.Role != nil {
				roles = append(roles, *change.Role)
			}
			target := change.target()
			if seen[target] {
				return ErrS1Contract
			}
			seen[target] = true
		}
		return validateRoleBounds(roles, limits)
	case S1IssueSession:
		if len(c.Changes) != 0 || c.Session == nil || c.SessionDigest != "" || c.SessionLineage == 0 || c.Session.Revoked || c.Session.IssuerConfigRevision == 0 || !validHexDigest(c.Session.Digest) || !validHexDigest(c.Session.CSRFDigest) || c.Session.Identity.Kind != OIDCPrincipal || !c.Session.Identity.valid() || c.Session.CreatedAt.IsZero() || !c.Session.ExpiresAt.After(c.Session.CreatedAt) || c.Session.ExpiresAt.Sub(c.Session.CreatedAt) > MaxSessionLifetime || c.Session.AuthTime.After(c.Session.CreatedAt) || c.ReplacesDigest != "" && (!validHexDigest(c.ReplacesDigest) || c.ReplacesDigest == c.Session.Digest) {
			return ErrS1Contract
		}
	case S1RevokeSession:
		if len(c.Changes) != 0 || c.Session != nil || c.ReplacesDigest != "" || !validHexDigest(c.SessionDigest) || c.SessionLineage == 0 {
			return ErrS1Contract
		}
	default:
		return ErrS1Contract
	}
	return nil
}

func s1Digest(label string, value any) [32]byte {
	// Only validated bounded typed structures enter this helper; no maps or
	// floats occur in canonical material. Struct field order is versioned.
	encoded, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(append([]byte("lantern/security/s1/"+label+"\x00"), encoded...))
}
