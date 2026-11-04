package security

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestOperationalPolicySupportCertificateAllGroupsAndFixedParticipants(t *testing.T) {
	contracts := map[[32]byte]bool{}
	for _, group := range operationalpolicy.Groups() {
		contract := operationalPolicySupportContract(group)
		if contract == [32]byte{} || contracts[contract] {
			t.Fatal("group application contracts are not distinct")
		}
		contracts[contract] = true
		for _, count := range []int{1, 3, 31} {
			t.Run(fmt.Sprintf("%s/%d", group, count), func(t *testing.T) {
				f, h, profile, voters, origins := operationalPolicyCertificateFixture(t, group, count)
				raw, err := encodeOperationalPolicySupportCertificate(f.trust, profile, h, voters, origins)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := decodeOperationalPolicySupportCertificate(f.trust, profile, h, raw)
				if err != nil || decoded.statement.Group != group || decoded.statement.ApplicationVersion != operationalpolicy.Version || decoded.raw != string(raw) || len(decoded.voters) != count || len(decoded.origins) != count {
					t.Fatal("complete fixed cohort rejected", err)
				}
				bound, err := operationalPolicySupportCertificateBound(f.trust, profile, group)
				if err != nil || bound < uint64(len(raw)) {
					t.Fatal("complete certificate exceeded preparation bound", bound, len(raw), err)
				}
				retained := decoded.raw
				raw[0] ^= 1
				if decoded.raw != retained {
					t.Fatal("certificate retained caller storage")
				}
				for _, absent := range []operationalPolicySupportCertificateWire{{decoded.statement, voters[:count-1], origins}, {decoded.statement, voters, origins[:count-1]}} {
					if verifyOperationalPolicySupportCertificate(f.trust, decoded.statement, absent) {
						t.Fatal("a quorum substituted for all enrolled participants")
					}
				}
			})
		}
	}
	if operationalPolicySupportContract("unknown") != [32]byte{} {
		t.Fatal("unknown group acquired an application contract")
	}
}

func TestOperationalPolicySupportCertificateRejectsDifferentEvidence(t *testing.T) {
	f, h, profile, voters, origins := operationalPolicyCertificateFixture(t, operationalpolicy.GroupSearch, 3)
	statement, err := newOperationalPolicySupportStatement(f.trust, profile, h)
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*operationalPolicySupportCertificateWire){
		"version":        func(w *operationalPolicySupportCertificateWire) { w.Statement.Version++ },
		"application":    func(w *operationalPolicySupportCertificateWire) { w.Statement.ApplicationVersion++ },
		"group":          func(w *operationalPolicySupportCertificateWire) { w.Statement.Group = operationalpolicy.GroupRate },
		"unknown group":  func(w *operationalPolicySupportCertificateWire) { w.Statement.Group = "unknown" },
		"contract":       func(w *operationalPolicySupportCertificateWire) { w.Statement.Contract[0] ^= 1 },
		"original":       func(w *operationalPolicySupportCertificateWire) { w.Statement.Original[0] ^= 1 },
		"profile":        func(w *operationalPolicySupportCertificateWire) { w.Statement.Profile.Membership[0] ^= 1 },
		"missing voter":  func(w *operationalPolicySupportCertificateWire) { w.Voters = w.Voters[:2] },
		"missing origin": func(w *operationalPolicySupportCertificateWire) { w.Origins = w.Origins[:2] },
		"duplicate voter": func(w *operationalPolicySupportCertificateWire) {
			w.Voters[1] = w.Voters[0]
		},
		"duplicate origin": func(w *operationalPolicySupportCertificateWire) {
			w.Origins[1] = w.Origins[0]
		},
		"extra voter": func(w *operationalPolicySupportCertificateWire) { w.Voters = append(w.Voters, w.Voters[0]) },
		"wrong role":  func(w *operationalPolicySupportCertificateWire) { w.Voters[0].Role = operationalPolicySupportOrigin },
		"wrong signer": func(w *operationalPolicySupportCertificateWire) {
			w.Origins[0].Signer = 99
		},
		"reordered voters": func(w *operationalPolicySupportCertificateWire) {
			w.Voters[0], w.Voters[1] = w.Voters[1], w.Voters[0]
		},
		"unsigned voter": func(w *operationalPolicySupportCertificateWire) { w.Voters[0].Signature = nil },
		"bad signature":  func(w *operationalPolicySupportCertificateWire) { w.Voters[0].Signature[0] ^= 1 },
		"voter as origin": func(w *operationalPolicySupportCertificateWire) {
			w.Origins[0].Signature = w.Voters[0].Signature
		},
		"unfenced floor": func(w *operationalPolicySupportCertificateWire) { w.Voters[0].Floor.Index = 2 },
		"zero floor":     func(w *operationalPolicySupportCertificateWire) { w.Voters[0].Floor = s2cJournalFloor{} },
		"different signed origin floor": func(w *operationalPolicySupportCertificateWire) {
			w.Origins[0].Floor.Index++
			body, err := operationalPolicySupportAttestationBody(w.Statement, w.Origins[0])
			if err != nil {
				t.Fatal(err)
			}
			w.Origins[0].Signature = ed25519.Sign(authorityCertificateOriginKeys(t, 3)[1], body)
		},
	} {
		t.Run(name, func(t *testing.T) {
			wire := operationalPolicySupportCertificateWire{statement, cloneOperationalPolicyAttestations(voters), cloneOperationalPolicyAttestations(origins)}
			edit(&wire)
			if verifyOperationalPolicySupportCertificate(f.trust, statement, wire) {
				t.Fatal("different evidence accepted")
			}
			raw, _ := json.Marshal(wire)
			if _, err := decodeOperationalPolicySupportCertificate(f.trust, profile, h, raw); err == nil {
				t.Fatal("invalid evidence created verified certificate")
			}
		})
	}
}

func TestOperationalPolicySupportCertificateStrictAndBoundToOriginal(t *testing.T) {
	f, h, profile, voters, origins := operationalPolicyCertificateFixture(t, operationalpolicy.GroupSlowRPC, 1)
	raw, err := encodeOperationalPolicySupportCertificate(f.trust, profile, h, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	for name, malformed := range map[string][]byte{
		"empty": nil, "null": []byte("null"),
		"unknown":   bytes.Replace(raw, []byte(`"Statement":`), []byte(`"unknown":true,"Statement":`), 1),
		"duplicate": bytes.Replace(raw, []byte(`"Version":1`), []byte(`"Version":1,"Version":1`), 1),
		"nested":    bytes.Replace(raw, []byte(`"Floor":{`), []byte(`"Floor":{"unknown":true,`), 1),
		"decimal":   bytes.Replace(raw, []byte(`"Version":1`), []byte(`"Version":1.0`), 1),
		"space":     append([]byte(" "), raw...), "trailing": append(bytes.Clone(raw), []byte("{}")...),
		"oversized": bytes.Repeat([]byte(" "), operationalPolicySupportCertificateMax+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOperationalPolicySupportCertificate(f.trust, profile, h, malformed); err == nil {
				t.Fatal("noncanonical certificate accepted")
			}
		})
	}
	tight := *f.trust
	tight.bounds.ProofBytes = uint64(len(raw) - 1)
	if _, err := encodeOperationalPolicySupportCertificate(&tight, profile, h, voters, origins); err == nil {
		t.Fatal("encoding ignored configured bound")
	}
	if _, err := operationalPolicySupportCertificateBound(&tight, profile, operationalpolicy.GroupSlowRPC); err == nil {
		t.Fatal("preflight ignored configured bound")
	}
	if _, err := decodeOperationalPolicySupportCertificate(&tight, profile, h, raw); err == nil {
		t.Fatal("decoding ignored configured bound")
	}
	if _, err := decodeOperationalPolicySupportCertificate(nil, profile, h, raw); err == nil {
		t.Fatal("missing independent trust accepted")
	}
	other := profile
	other.Membership[0] ^= 1
	if _, err := newOperationalPolicySupportStatement(f.trust, other, h); err == nil {
		t.Fatal("caller-selected profile accepted")
	}
	_, unrelated, _, _, _ := logPolicyCertificateFixture(t, 1)
	if _, err := newOperationalPolicySupportStatement(f.trust, profile, unrelated); err == nil {
		t.Fatal("log policy original accepted as operational adoption")
	}
	changed := *h
	changed.raw += " "
	if _, err := encodeOperationalPolicySupportCertificate(f.trust, profile, &changed, voters, origins); err == nil {
		t.Fatal("unauthentic original accepted")
	}
	reader := s2CapsuleReader{[]byte(h.raw)[len(authorityHistoricalMagic):]}
	headerRaw, err := reader.record()
	if err != nil {
		t.Fatal(err)
	}
	var header authorityHistoricalHeader
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		t.Fatal(err)
	}
	header.ID.Nonce[0]++
	nextRaw := authorityTestSealHeader(t, f, authorityCertificateOriginKeys(t, 1)[1], header, h.handoff.operation)
	next, err := verifyAuthorityHistoricalH(f.trust, nextRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeOperationalPolicySupportCertificate(f.trust, profile, next, raw); err == nil {
		t.Fatal("certificate replayed against different authentic command ID")
	}
	command, err := h.handoff.operation.command()
	if err != nil {
		t.Fatal(err)
	}
	command.Changes[0].OperationalPolicy.Values.SlowRPC.ThresholdMS++
	changedIntent := s1Operation(t, f.genesis.state.projection, testIdentity(), command)
	header.OperationDigest = changedIntent.digest
	header.ID = h.handoff.id
	intentRaw := authorityTestSealHeader(t, f, authorityCertificateOriginKeys(t, 1)[1], header, changedIntent)
	intentOriginal, err := verifyAuthorityHistoricalH(f.trust, intentRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeOperationalPolicySupportCertificate(f.trust, profile, intentOriginal, raw); err == nil {
		t.Fatal("certificate replayed against different authentic intent")
	}
}
