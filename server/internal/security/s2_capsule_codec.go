package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
)

// This type carries untrusted data only. It has no state accessor, certificate,
// authorization, verifier callback or serialized trust bit.
type s2DecodedCandidate struct {
	canonical string
}

type s2CapsuleReader struct{ remaining []byte }

func (r *s2CapsuleReader) word() (uint32, error) {
	if len(r.remaining) < 4 {
		return 0, errS2Capsule
	}
	n := binary.BigEndian.Uint32(r.remaining)
	r.remaining = r.remaining[4:]
	return n, nil
}

func (r *s2CapsuleReader) record() ([]byte, error) {
	n, err := r.word()
	if err != nil || n == 0 || uint64(n) > uint64(len(r.remaining)) {
		return nil, errS2Capsule
	}
	row := r.remaining[:int(n)]
	r.remaining = r.remaining[int(n):]
	return row, nil
}

func (r *s2CapsuleReader) count(limit uint32) (uint32, error) {
	n, err := r.word()
	// Each row needs at least a length word and two JSON bytes. Check remaining
	// space and configured count BEFORE allocating keyed storage or decoding.
	if err != nil || n > limit || uint64(n) > uint64(len(r.remaining))/6 {
		return 0, errS2Capsule
	}
	return n, nil
}

// Token preflight bounds nested arrays/objects before typed json allocation.
// Canonical re-encoding below rejects aliases, alternate escapes/numbers,
// omitted fields, whitespace and fixed-array length drift as well.
func s2JSONPreflight(encoded []byte, policy PolicyLimits) error {
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.UseNumber()
	var value func(string, int) error
	value = func(field string, depth int) error {
		if depth > 24 {
			return errS2Capsule
		}
		token, err := d.Token()
		if err != nil {
			return errS2Capsule
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || len(name) > 64 || seen[name] || len(seen) >= 32 {
					return errS2Capsule
				}
				seen[name] = true
				if err := value(name, depth+1); err != nil {
					return err
				}
			}
		case '[':
			limit := 32 // Digests and nonces are fixed arrays.
			switch field {
			case "issuers":
				limit = MaxIssuers
			case "roles":
				limit = policy.MaxRoles
			case "rules":
				limit = policy.MaxRules
			case "principals":
				limit = MaxPrincipals
			case "sessions":
				limit = MaxSessions
			case "machine_credentials":
				limit = MaxMachineCredentials
			case "audit":
				limit = retainedChanges
			case "assignments":
				limit = policy.MaxAssignments
			case "algorithms":
				limit = 4
			case "target_digests":
				limit = 16
			case "Items", "Changes":
				limit = MaxTransactionChanges
			}
			for n := 0; d.More(); n++ {
				if n >= limit {
					return errS2Capsule
				}
				if err := value("", depth+1); err != nil {
					return err
				}
			}
		default:
			return errS2Capsule
		}
		end, err := d.Token()
		if err != nil || delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
			return errS2Capsule
		}
		return nil
	}
	if err := value("", 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errS2Capsule
	}
	return nil
}

func s2StrictJSON(encoded []byte, target any, policy PolicyLimits) error {
	if err := s2JSONPreflight(encoded, policy); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errS2Capsule
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(encoded, canonical) {
		return errS2Capsule
	}
	return nil
}

func s2DecodeOperation(o s2CapsuleOperation) (S1Command, error) {
	const prefix = "lantern/security/s1/operation\x00"
	if len(o.Canonical) <= len(prefix) || len(o.Canonical) > MaxImageBytes+len(prefix) || !bytes.HasPrefix([]byte(o.Canonical), []byte(prefix)) {
		return S1Command{}, errS2Capsule
	}
	var envelope struct {
		Version  uint16
		Actor    Identity
		Reviewed SemanticCut
		Command  S1Command
	}
	// Original rejected-CAS intent can have a different historical policy.
	// Hard parser/command ceilings bound it without substituting current policy
	// for its original review. Authenticity/ancestry comes only from replay.
	if err := s2StrictJSON([]byte(o.Canonical)[len(prefix):], &envelope, DefaultPolicyLimits()); err != nil || envelope.Version != S1Version || !o.Actor.valid() || !o.Reviewed.valid() || envelope.Actor != o.Actor || envelope.Reviewed != o.Reviewed || validateS1Command(envelope.Command, DefaultPolicyLimits()) != nil {
		return S1Command{}, errS2Capsule
	}
	if sha256.Sum256([]byte(o.Canonical)) != o.Digest {
		return S1Command{}, errS2Capsule
	}
	return envelope.Command, nil
}

func s2DecodeCapsule(encoded []byte, limits s2CapsuleLimits) (*s2DecodedCandidate, error) {
	if !limits.valid() || uint64(len(encoded)) > limits.Bytes || !bytes.HasPrefix(encoded, []byte(s2CapsuleMagic)) {
		return nil, errS2Capsule
	}
	r := s2CapsuleReader{encoded[len(s2CapsuleMagic):]}
	row, err := r.record()
	if err != nil {
		return nil, err
	}
	var h s2CapsuleHeader
	// Header contains no policy-sized arrays; defaults here are parser HARD
	// ceilings only. All image/operation decoding uses the actual stored config.
	if err := s2StrictJSON(row, &h, DefaultPolicyLimits()); err != nil || !h.valid() {
		return nil, errS2Capsule
	}
	imageBytes, err := r.record()
	if err != nil || len(imageBytes) > int(h.Configuration.Capacity.ImageBytes) || len(imageBytes) > MaxImageBytes {
		return nil, errS2Capsule
	}
	var image Image
	if err := s2StrictJSON(imageBytes, &image, h.Configuration.Policy); err != nil {
		return nil, err
	}
	snapshot, err := CompileImage(image, h.Configuration.Policy)
	if err != nil || !bytes.Equal(imageBytes, snapshot.image) {
		return nil, errS2Capsule
	}
	n, err := r.count(limits.LineageEntries)
	if err != nil {
		return nil, err
	}
	lineage := make(map[Identity]uint64, int(n))
	var previous Identity
	for i := uint32(0); i < n; i++ {
		row, err := r.record()
		var entry s2CapsuleLineage
		if err != nil || s2StrictJSON(row, &entry, h.Configuration.Policy) != nil || !entry.Identity.valid() || entry.Identity.Kind != OIDCPrincipal || entry.Epoch == 0 || i != 0 && !s2LineageLess(previous, entry.Identity) {
			return nil, errS2Capsule
		}
		lineage[entry.Identity] = entry.Epoch
		previous = entry.Identity
	}
	for id := range snapshot.principals {
		if id.Kind == OIDCPrincipal && lineage[id] == 0 {
			return nil, errS2Capsule
		}
	}
	// This temporary projection only recomputes a commitment. It never escapes
	// and is never an S1ApplyState, trusted genesis or verified input.
	p := S1Projection{snapshot, lineage, h.Cut}
	if p.projectionDigest() != h.Cut.Projection {
		return nil, errS2Capsule
	}
	ledgerLimit := min(limits.LedgerEntries, h.Configuration.Capacity.LedgerEntries)
	n, err = r.count(ledgerLimit)
	if err != nil {
		return nil, err
	}
	var previousID FullChangeID
	for i := uint32(0); i < n; i++ {
		row, err := r.record()
		var outcome s2CapsuleOutcome
		if err != nil || s2StrictJSON(row, &outcome, h.Configuration.Policy) != nil || s2ValidateOutcome(outcome, h) != nil || i != 0 && !s2IDLess(previousID, outcome.ID) {
			return nil, errS2Capsule
		}
		previousID = outcome.ID
	}
	if len(r.remaining) != 0 {
		return nil, errS2Capsule
	}
	return &s2DecodedCandidate{string(encoded)}, nil // Detached immutable bytes.
}
