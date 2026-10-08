package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

func s2CodecTestScope(g *s2TrustedGenesis) s2LocalScope {
	c := g.state.projection.cut
	return s2LocalScope{
		StoreIdentity: [32]byte{1}, JournalEpoch: [16]byte{2}, Domain: c.Domain, Cohort: c.Cohort,
		Generation: c.Generation, Membership: g.state.membership, Fences: c.Fences,
		Configuration: g.state.configuration, ConfigDigest: g.state.configuration.Digest(), Genesis: g.roots.Genesis,
		Floor:   s2CapsuleFloor{g.state.retiredThrough, c.Domain, c.Cohort, g.roots.Retirement},
		Storage: s2LocalStoragePolicy{s2TestLimits(), uint64(MaxSystemJournalBytes), 10000},
	}
}

func s2CodecTestGenesis(t *testing.T) (*s2TrustedGenesis, s2LocalScope, *s2LocalRecord) {
	t.Helper()
	g := s2TestGenesis(s1Fixture(t, s1Image()))
	s := s2CodecTestScope(g)
	b := s2TestEncode(t, g.state, g.roots)
	r := &s2LocalRecord{kind: s2LocalGenesis, scope: s, scopeDigest: s.digest(), localIndex: 1, capsuleDigest: s2LocalCapsuleDigest(b), capsule: string(b)}
	return g, s, r
}

func s2CodecTestEncode(t *testing.T, r *s2LocalRecord, s s2LocalScope) []byte {
	t.Helper()
	b, err := s2EncodeLocalRecord(r, s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func s2CodecTestParts(t *testing.T, b []byte) (byte, []byte, []byte) {
	t.Helper()
	r := s2CapsuleReader{b[len(s2LocalMagic)+1:]}
	h, err := r.record()
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.record()
	if err != nil || len(r.remaining) != 0 {
		t.Fatal("invalid fixture", err)
	}
	return b[len(s2LocalMagic)], bytes.Clone(h), bytes.Clone(c)
}

func s2CodecTestFrame(kind byte, header, capsule []byte) []byte {
	b := append([]byte(s2LocalMagic), kind)
	b = binary.BigEndian.AppendUint32(b, uint32(len(header)))
	b = append(b, header...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(capsule)))
	return append(b, capsule...)
}

func TestS2LocalCodecRoundTripCompleteHistory(t *testing.T) {
	g, final, replay, original := s2TestHistory(t)
	scope := s2CodecTestScope(g)
	state := g.state
	previous := [32]byte{}
	var firstCommit CommitRef
	for i := 0; i <= len(replay); i++ {
		r := &s2LocalRecord{scopeDigest: scope.digest(), localIndex: uint64(i + 1), previousCapsuleDigest: previous}
		if i == 0 {
			r.kind, r.scope = s2LocalGenesis, scope
		} else {
			next := replay[i-1]
			result, err := ApplyS1(state, next)
			if err != nil {
				t.Fatal(err)
			}
			state = result.State
			r.kind, r.commit = s2LocalApply, next.certificate.commit
			if i == 2 {
				firstCommit = result.Outcome.Commit()
			}
			if i == 4 && (result.Outcome.Commit() != firstCommit || r.commit == firstCommit) {
				t.Fatal("current control entry replaced the original outcome commit")
			}
		}
		capsule := s2TestEncode(t, state, g.roots)
		r.capsule, r.capsuleDigest = string(capsule), s2LocalCapsuleDigest(capsule)
		encoded := s2CodecTestEncode(t, r, scope)
		decoded, err := s2DecodeLocalRecord(encoded, scope)
		if err != nil || *decoded != *r {
			t.Fatalf("record %d round trip: %v", i, err)
		}
		_, header, raw := s2CodecTestParts(t, encoded)
		if !bytes.Equal(raw, capsule) || uint64(len(encoded)) != uint64(len(s2LocalMagic)+9+len(header)+len(capsule)) {
			t.Fatal("capsule was not stored as exact raw bytes")
		}
		encoded[len(encoded)-1] ^= 1
		if decoded.capsule != string(capsule) {
			t.Fatal("decoder retained a mutable caller slice")
		}
		previous = r.capsuleDigest
	}
	if !bytes.Equal(s2TestEncode(t, state, g.roots), s2TestEncode(t, final, g.roots)) {
		t.Fatal("complete replay changed final aggregate")
	}
	_, outcome := state.Lookup(original.id)
	if outcome.Commit() != firstCommit {
		t.Fatal("original result changed")
	}
}

func TestS2LocalCodecRejectsNoncanonicalHeaders(t *testing.T) {
	_, scope, genesis := s2CodecTestGenesis(t)
	encoded := s2CodecTestEncode(t, genesis, scope)
	kind, header, capsule := s2CodecTestParts(t, encoded)
	cases := map[string][]byte{
		"duplicate":        bytes.Replace(header, []byte(`"LocalIndex":1`), []byte(`"LocalIndex":1,"LocalIndex":1`), 1),
		"nested duplicate": bytes.Replace(header, []byte(`"JournalBytes":536870912`), []byte(`"JournalBytes":536870912,"JournalBytes":536870912`), 1),
		"alias":            bytes.Replace(header, []byte(`"LocalIndex"`), []byte(`"localindex"`), 1),
		"nested alias":     bytes.Replace(header, []byte(`"JournalBytes"`), []byte(`"journalbytes"`), 1),
		"unknown":          append(append(bytes.Clone(header[:len(header)-1]), []byte(`,"Unknown":0`)...), '}'),
		"escaped name":     bytes.Replace(header, []byte(`"LocalIndex"`), []byte(`"Local\u0049ndex"`), 1),
		"whitespace":       append([]byte(" "), header...),
		"exponent":         bytes.Replace(header, []byte(`"LocalIndex":1`), []byte(`"LocalIndex":1e0`), 1),
		"negative":         bytes.Replace(header, []byte(`"LocalIndex":1`), []byte(`"LocalIndex":-1`), 1),
		"overflow":         bytes.Replace(header, []byte(`"LocalIndex":1`), []byte(`"LocalIndex":18446744073709551616`), 1),
		"omitted":          bytes.Replace(header, []byte(`,"LocalIndex":1`), nil, 1),
		"array short":      bytes.Replace(header, []byte(`"JournalEpoch":[2,0`), []byte(`"JournalEpoch":[2`), 1),
		"array long":       bytes.Replace(header, []byte(`"JournalEpoch":[2,0`), []byte(`"JournalEpoch":[2,0,0`), 1),
		"array null":       bytes.Replace(header, s2TestJSON(t, scope.JournalEpoch), []byte(`null`), 1),
		"trailing JSON":    append(bytes.Clone(header), []byte(`{}`)...),
	}
	// Reordering otherwise equivalent fields is a different wire encoding.
	canonicalIndex := []byte(`,"LocalIndex":1`)
	reordered := bytes.Replace(header, canonicalIndex, nil, 1)
	cases["field order"] = append([]byte(`{"LocalIndex":1,`), reordered[1:]...)
	for name, changed := range cases {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(changed, header) {
				t.Fatal("mutation did not change fixture")
			}
			if r, err := s2DecodeLocalRecord(s2CodecTestFrame(kind, changed, capsule), scope); err == nil || r != nil {
				t.Fatal("noncanonical header accepted")
			}
		})
	}
}

func TestS2LocalCodecRejectsMalformedFraming(t *testing.T) {
	_, scope, genesis := s2CodecTestGenesis(t)
	b := s2CodecTestEncode(t, genesis, scope)
	_, header, capsule := s2CodecTestParts(t, b)
	headerAt := len(s2LocalMagic) + 1
	capsuleAt := headerAt + 4 + len(header)
	word := func(at int, value uint32) []byte {
		v := bytes.Clone(b)
		binary.BigEndian.PutUint32(v[at:], value)
		return v
	}
	cases := map[string][]byte{
		"old image": []byte(`LNSEC03`), "bare capsule": capsule,
		"empty header": word(headerAt, 0), "large header": word(headerAt, s2LocalMaxHeaderBytes+1),
		"short header": word(headerAt, uint32(len(header)-1)), "long header": word(headerAt, uint32(len(header)+1)),
		"overflow header": word(headerAt, math.MaxUint32),
		"empty capsule":   word(capsuleAt, 0), "short capsule": word(capsuleAt, uint32(len(capsule)-1)),
		"long capsule": word(capsuleAt, uint32(len(capsule)+1)), "overflow capsule": word(capsuleAt, math.MaxUint32),
		"hard capsule bound": word(capsuleAt, uint32(s2MaxCapsuleBytes+1)),
		"trailing":           append(bytes.Clone(b), 0), "payload bound": make([]byte, int(s2LocalMaxPayloadBytes)+1),
	}
	for _, kind := range []byte{0, 3, 255} {
		v := bytes.Clone(b)
		v[len(s2LocalMagic)] = kind
		cases["kind "+strconv.Itoa(int(kind))] = v
	}
	version := bytes.Clone(b)
	version[len(s2LocalMagic)-1]++
	cases["format version"] = version
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if r, err := s2DecodeLocalRecord(data, scope); err == nil || r != nil {
				t.Fatal("malformed framing accepted")
			}
		})
	}
	for i := range b {
		if r, err := s2DecodeLocalRecord(b[:i], scope); err == nil || r != nil {
			t.Fatalf("truncated record accepted at %d", i)
		}
	}
}

func TestS2LocalScopeIndependentExactBinding(t *testing.T) {
	g, scope, genesis := s2CodecTestGenesis(t)
	b := s2CodecTestEncode(t, genesis, scope)
	if err := scope.validateGenesis(g); err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*s2LocalScope){
		"store": func(s *s2LocalScope) { s.StoreIdentity[0]++ }, "epoch": func(s *s2LocalScope) { s.JournalEpoch[0]++ },
		"domain":     func(s *s2LocalScope) { s.Domain[0]++; s.Floor.Domain = s.Domain },
		"cohort":     func(s *s2LocalScope) { s.Cohort[0]++; s.Floor.Cohort = s.Cohort },
		"generation": func(s *s2LocalScope) { s.Generation[0]++ }, "membership": func(s *s2LocalScope) { s.Membership[0]++ },
		"fences": func(s *s2LocalScope) { s.Fences[0]++ }, "genesis": func(s *s2LocalScope) { s.Genesis[0]++ },
		"configuration": func(s *s2LocalScope) {
			s.Configuration.Capacity.LedgerEntries++
			s.ConfigDigest = s.Configuration.Digest()
		},
		"policy":        func(s *s2LocalScope) { s.Configuration.Policy.MaxRoles--; s.ConfigDigest = s.Configuration.Digest() },
		"floor":         func(s *s2LocalScope) { s.Floor.Through = 1; s.Floor.Root = [32]byte{1} },
		"capsule bytes": func(s *s2LocalScope) { s.Storage.Capsule.Bytes-- },
		"ledger bound":  func(s *s2LocalScope) { s.Storage.Capsule.LedgerEntries-- },
		"lineage bound": func(s *s2LocalScope) { s.Storage.Capsule.LineageEntries-- },
		"budget":        func(s *s2LocalScope) { s.Storage.JournalBytes-- },
		"replay bound":  func(s *s2LocalScope) { s.Storage.ReplayRecords-- },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			changed := scope
			change(&changed)
			if !changed.valid() || changed.digest() == scope.digest() || changed.tipBinding() == scope.tipBinding() {
				t.Fatal("full independent scope was not bound")
			}
			if r, err := s2DecodeLocalRecord(b, changed); err == nil || r != nil {
				t.Fatal("different independent expected scope accepted")
			}
			if name == "store" || name == "epoch" || strings.Contains(name, "bound") || name == "budget" || name == "capsule bytes" {
				return // Local family policy is independent of trusted S1 genesis.
			}
			if err := changed.validateGenesis(g); err == nil {
				t.Fatal("trusted genesis mismatch accepted")
			}
		})
	}
	if scope.validateGenesis(nil) == nil || scope.validateGenesis(&s2TrustedGenesis{}) == nil {
		t.Fatal("missing independently trusted genesis accepted")
	}
	copyState := *g.state
	copyState.slot = 1
	if scope.validateGenesis(&s2TrustedGenesis{&copyState, g.roots}) == nil {
		t.Fatal("later state accepted as genesis")
	}
	copyRoots := g.roots
	copyRoots.Genesis[0]++
	if scope.validateGenesis(&s2TrustedGenesis{g.state, copyRoots}) == nil {
		t.Fatal("different trusted root accepted")
	}
}

func TestS2LocalScopeRequiresExplicitBoundedPolicy(t *testing.T) {
	_, scope, _ := s2CodecTestGenesis(t)
	cases := map[string]func(*s2LocalScope){
		"store zero":          func(s *s2LocalScope) { s.StoreIdentity = [32]byte{} },
		"epoch zero":          func(s *s2LocalScope) { s.JournalEpoch = [16]byte{} },
		"digest zero":         func(s *s2LocalScope) { s.ConfigDigest = [32]byte{} },
		"configuration":       func(s *s2LocalScope) { s.Configuration.Version++ },
		"floor scope":         func(s *s2LocalScope) { s.Floor.Cohort[0]++ },
		"floor without root":  func(s *s2LocalScope) { s.Floor.Through = 1 },
		"root without floor":  func(s *s2LocalScope) { s.Floor.Root[0] = 1 },
		"budget zero":         func(s *s2LocalScope) { s.Storage.JournalBytes = 0 },
		"budget hard ceiling": func(s *s2LocalScope) { s.Storage.JournalBytes = uint64(MaxSystemJournalBytes) + 1 },
		"budget overflow":     func(s *s2LocalScope) { s.Storage.JournalBytes = math.MaxUint64 },
		"records zero":        func(s *s2LocalScope) { s.Storage.ReplayRecords = 0 },
		"records ceiling":     func(s *s2LocalScope) { s.Storage.ReplayRecords = s.Storage.JournalBytes/44 + 1 },
		"records overflow":    func(s *s2LocalScope) { s.Storage.ReplayRecords = math.MaxUint64 },
		"capsule zero":        func(s *s2LocalScope) { s.Storage.Capsule.Bytes = 0 },
		"capsule ceiling":     func(s *s2LocalScope) { s.Storage.Capsule.Bytes = s2MaxCapsuleBytes + 1 },
		"ledger zero":         func(s *s2LocalScope) { s.Storage.Capsule.LedgerEntries = 0 },
		"ledger ceiling":      func(s *s2LocalScope) { s.Storage.Capsule.LedgerEntries = 100001 },
		"lineage zero":        func(s *s2LocalScope) { s.Storage.Capsule.LineageEntries = 0 },
		"lineage ceiling":     func(s *s2LocalScope) { s.Storage.Capsule.LineageEntries = 100001 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := scope
			change(&s)
			if s.valid() || s.digest() != [32]byte{} || s.tipBinding() != [32]byte{} {
				t.Fatal("invalid scope has usable bindings")
			}
			if r, err := s2DecodeLocalRecord(nil, s); err == nil || r != nil {
				t.Fatal("invalid expected scope accepted")
			}
		})
	}
	for _, budget := range []uint64{44, 45, uint64(MaxSystemJournalBytes)} {
		s := scope
		s.Storage.JournalBytes, s.Storage.ReplayRecords = budget, budget/44
		if !s.valid() {
			t.Fatal("valid conservative bound rejected", budget)
		}
	}
	tooSmall := scope
	tooSmall.Storage.JournalBytes, tooSmall.Storage.ReplayRecords = 43, 1
	if tooSmall.valid() {
		t.Fatal("budget with no possible replay record accepted")
	}
}

func TestS2LocalCodecDigestDomainsAndCheckedSizes(t *testing.T) {
	_, scope, genesis := s2CodecTestGenesis(t)
	b, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(append([]byte("lantern/security/s2/local-scope\x00\x01"), b...))
	if scope.digest() != want || scope.tipBinding() == want {
		t.Fatal("scope and tip digest separation")
	}
	tip := sha256.Sum256(append([]byte("lantern/security/s2/local-tip-binding\x00\x01"), want[:]...))
	if scope.tipBinding() != tip {
		t.Fatal("tip does not bind complete scope")
	}
	capsule := sha256.Sum256(append([]byte("lantern/security/s2/local-capsule\x00\x01"), []byte(genesis.capsule)...))
	if genesis.capsuleDigest != capsule || capsule == sha256.Sum256([]byte(genesis.capsule)) {
		t.Fatal("whole capsule commitment")
	}
	for _, lengths := range [][2]uint64{{0, 1}, {1, 0}, {s2LocalMaxHeaderBytes + 1, 1}, {1, s2MaxCapsuleBytes + 1}, {math.MaxUint64, 1}, {1, math.MaxUint64}} {
		if _, err := s2LocalPayloadSize(lengths[0], lengths[1]); err == nil {
			t.Fatal("unrepresentable payload accepted", lengths)
		}
	}
	n, err := s2LocalPayloadSize(s2LocalMaxHeaderBytes, s2MaxCapsuleBytes)
	if err != nil || n != uint64(len(s2LocalMagic)+9+s2LocalMaxHeaderBytes)+s2MaxCapsuleBytes || n > s2LocalMaxPayloadBytes {
		t.Fatal("maximum supported capsule framing", n, err)
	}
	for _, slot := range []uint64{0, 1, math.MaxUint64 - 2} {
		index, err := s2LocalIndex(slot)
		if err != nil || index != slot+1 {
			t.Fatal("slot/index mapping", slot, index, err)
		}
	}
	for _, slot := range []uint64{math.MaxUint64 - 1, math.MaxUint64} {
		if _, err := s2LocalIndex(slot); err == nil {
			t.Fatal("uninstallable local index accepted", slot)
		}
	}
}

func TestS2LocalCodecRejectsCapsuleAndEntryMismatch(t *testing.T) {
	g, scope, genesis := s2CodecTestGenesis(t)
	next := s1Next(g.state, nil)
	result, err := ApplyS1(g.state, next)
	if err != nil {
		t.Fatal(err)
	}
	b := s2TestEncode(t, result.State, g.roots)
	apply := &s2LocalRecord{kind: s2LocalApply, scopeDigest: scope.digest(), localIndex: 2, previousCapsuleDigest: genesis.capsuleDigest, capsuleDigest: s2LocalCapsuleDigest(b), commit: next.certificate.commit, capsule: string(b)}
	s2CodecTestEncode(t, apply, scope)
	cases := map[string]func(*s2LocalRecord){
		"index gap":            func(r *s2LocalRecord) { r.localIndex++ },
		"index overflow":       func(r *s2LocalRecord) { r.localIndex = math.MaxUint64 },
		"scope digest":         func(r *s2LocalRecord) { r.scopeDigest[0]++ },
		"digest":               func(r *s2LocalRecord) { r.capsuleDigest[0]++ },
		"predecessor missing":  func(r *s2LocalRecord) { r.previousCapsuleDigest = [32]byte{} },
		"commit slot":          func(r *s2LocalRecord) { r.commit.Slot++ },
		"commit value":         func(r *s2LocalRecord) { r.commit.Value = [32]byte{} },
		"commit configuration": func(r *s2LocalRecord) { r.commit.Configuration[0]++ },
		"commit membership":    func(r *s2LocalRecord) { r.commit.Membership[0]++ },
		"commit domain":        func(r *s2LocalRecord) { r.commit.Domain[0]++ },
		"commit cohort":        func(r *s2LocalRecord) { r.commit.Cohort[0]++ },
		"commit version":       func(r *s2LocalRecord) { r.commit.Version++ },
		"apply genesis scope":  func(r *s2LocalRecord) { r.scope = scope },
		"second genesis": func(r *s2LocalRecord) {
			r.kind, r.scope, r.commit, r.previousCapsuleDigest = s2LocalGenesis, scope, CommitRef{}, [32]byte{}
		},
		"bare image": func(r *s2LocalRecord) {
			r.capsule = string(g.state.projection.snapshot.image)
			r.capsuleDigest = s2LocalCapsuleDigest([]byte(r.capsule))
		},
		"corrupt capsule": func(r *s2LocalRecord) { r.capsule += "x"; r.capsuleDigest = s2LocalCapsuleDigest([]byte(r.capsule)) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := *apply
			change(&r)
			if encoded, err := s2EncodeLocalRecord(&r, scope); err == nil || encoded != nil {
				t.Fatal("invalid typed record encoded")
			}
			// Bypass the encoder for every representable malformed wire header.
			var header []byte
			if r.kind == s2LocalGenesis {
				header = s2TestJSON(t, s2LocalGenesisHeader{r.scope, r.localIndex, r.capsuleDigest})
			} else {
				header = s2TestJSON(t, s2LocalApplyHeader{r.scopeDigest, r.localIndex, r.previousCapsuleDigest, r.capsuleDigest, r.commit})
			}
			if name != "apply genesis scope" {
				if decoded, err := s2DecodeLocalRecord(s2CodecTestFrame(r.kind, header, []byte(r.capsule)), scope); err == nil || decoded != nil {
					t.Fatal("invalid wire record accepted")
				}
			}
		})
	}
	for _, exact := range []bool{true, false} {
		s := scope
		s.Storage.Capsule.Bytes = uint64(len(genesis.capsule))
		if !exact {
			s.Storage.Capsule.Bytes--
		}
		r := *genesis
		r.scope, r.scopeDigest = s, s.digest()
		encoded, err := s2EncodeLocalRecord(&r, s)
		if exact && (err != nil || len(encoded) == 0) || !exact && (err == nil || encoded != nil) {
			t.Fatal("configured capsule bound", exact, err)
		}
	}
	s := scope
	s.Storage.ReplayRecords = 1
	r := *apply
	r.scopeDigest = s.digest()
	if encoded, err := s2EncodeLocalRecord(&r, s); err == nil || encoded != nil {
		t.Fatal("record limit ignored")
	}
	if encoded, err := s2EncodeLocalRecord(nil, scope); err == nil || encoded != nil {
		t.Fatal("nil record accepted")
	}
}

func TestS2LocalCodecChecksEveryCapsuleAgainstExpectedScope(t *testing.T) {
	_, scope, genesis := s2CodecTestGenesis(t)
	changes := map[string]func(*s2CapsuleHeader){
		"domain":       func(h *s2CapsuleHeader) { h.Cut.Domain[0]++; h.Floor.Domain = h.Cut.Domain },
		"cohort":       func(h *s2CapsuleHeader) { h.Cut.Cohort[0]++; h.Floor.Cohort = h.Cut.Cohort },
		"generation":   func(h *s2CapsuleHeader) { h.Cut.Generation[0]++ },
		"fences":       func(h *s2CapsuleHeader) { h.Cut.Fences[0]++ },
		"membership":   func(h *s2CapsuleHeader) { h.Membership[0]++ },
		"genesis root": func(h *s2CapsuleHeader) { h.Genesis[0]++ },
		"configuration": func(h *s2CapsuleHeader) {
			h.Configuration.Capacity.LedgerEntries++
			h.ConfigDigest = h.Configuration.Digest()
		},
		"policy": func(h *s2CapsuleHeader) {
			h.Configuration.Policy.MaxRoles--
			h.ConfigDigest = h.Configuration.Digest()
			h.Cut.Policy = s1PolicyConfiguration(h.Configuration.Policy)
		},
		"floor":            func(h *s2CapsuleHeader) { h.Floor.Through = 1; h.Floor.Root = [32]byte{1} },
		"genesis sequence": func(h *s2CapsuleHeader) { h.Cut.Sequence++ },
		"genesis previous": func(h *s2CapsuleHeader) { h.Cut.Previous = [32]byte{1} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			parts := s2TestSplit(t, []byte(genesis.capsule))
			var h s2CapsuleHeader
			if err := json.Unmarshal(parts.header, &h); err != nil {
				t.Fatal(err)
			}
			change(&h)
			parts.header = s2TestJSON(t, h)
			capsule := parts.encode()
			if _, err := s2DecodeCapsule(capsule, scope.Storage.Capsule); err != nil {
				t.Fatal("test substitution must itself be a valid S2-A candidate", err)
			}
			r := *genesis
			r.capsule, r.capsuleDigest = string(capsule), s2LocalCapsuleDigest(capsule)
			if encoded, err := s2EncodeLocalRecord(&r, scope); err == nil || encoded != nil {
				t.Fatal("mismatched capsule encoded under fixed scope")
			}
			header := s2TestJSON(t, s2LocalGenesisHeader{scope, 1, r.capsuleDigest})
			if record, err := s2DecodeLocalRecord(s2CodecTestFrame(s2LocalGenesis, header, capsule), scope); err == nil || record != nil {
				t.Fatal("header commitment replaced capsule scope validation")
			}
		})
	}
}

func TestS2LocalScopeBindsIndependentRetirementRoot(t *testing.T) {
	g, _, _ := s2CodecTestGenesis(t)
	c := g.state.projection.cut
	root := [32]byte{91}
	state, err := NewS1ApplyState(g.state.projection, g.state.membership, g.state.configuration.Capacity, S1Retention{7, c.Domain, c.Cohort, root})
	if err != nil {
		t.Fatal(err)
	}
	g = s2TestGenesis(state)
	g.roots.Retirement = root
	scope := s2CodecTestScope(g)
	if err := scope.validateGenesis(g); err != nil {
		t.Fatal("trusted fixed nonzero floor", err)
	}
	b := s2TestEncode(t, state, g.roots)
	r := &s2LocalRecord{kind: s2LocalGenesis, scope: scope, scopeDigest: scope.digest(), localIndex: 1, capsuleDigest: s2LocalCapsuleDigest(b), capsule: string(b)}
	encoded := s2CodecTestEncode(t, r, scope)
	if decoded, err := s2DecodeLocalRecord(encoded, scope); err != nil || *decoded != *r {
		t.Fatal("nonzero floor round trip", err)
	}
	for _, field := range []string{"floor", "root", "absent"} {
		t.Run(field, func(t *testing.T) {
			changed := scope
			switch field {
			case "floor":
				changed.Floor.Through++
			case "root":
				changed.Floor.Root[0]++
			case "absent":
				changed.Floor.Through, changed.Floor.Root = 0, [32]byte{}
			}
			if !changed.valid() || changed.validateGenesis(g) == nil {
				t.Fatal("fixed retirement trust weakened")
			}
			if decoded, err := s2DecodeLocalRecord(encoded, changed); err == nil || decoded != nil {
				t.Fatal("retirement policy changed on reopen")
			}
		})
	}
}

func TestS2LocalCodecMaximumFixedWidthHeaderFits(t *testing.T) {
	var identity [32]byte
	var incarnation [16]byte
	for i := range identity {
		identity[i] = math.MaxUint8
	}
	for i := range incarnation {
		incarnation[i] = math.MaxUint8
	}
	configuration := S1ExecutionConfig{S1Version, DefaultPolicyLimits(), S1Capacity{100000, 99999, MaxImageBytes, MaxImageBytes - 1}}
	projection, err := NewS1Projection(s1Image(), configuration.Policy, identity, identity, identity, incarnation)
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewS1ApplyState(projection, identity, configuration.Capacity, S1Retention{math.MaxUint64, identity, identity, identity})
	if err != nil {
		t.Fatal(err)
	}
	g := &s2TrustedGenesis{state, s2CapsuleRoots{identity, identity}}
	scope := s2CodecTestScope(g)
	scope.StoreIdentity, scope.JournalEpoch = identity, incarnation
	scope.Storage.ReplayRecords = scope.Storage.JournalBytes / 44
	if err := scope.validateGenesis(g); err != nil {
		t.Fatal(err)
	}
	b := s2TestEncode(t, state, g.roots)
	r := &s2LocalRecord{kind: s2LocalGenesis, scope: scope, scopeDigest: scope.digest(), localIndex: 1, capsuleDigest: s2LocalCapsuleDigest(b), capsule: string(b)}
	encoded := s2CodecTestEncode(t, r, scope)
	_, header, _ := s2CodecTestParts(t, encoded)
	if len(header) > s2LocalMaxHeaderBytes {
		t.Fatal("fixed-size header exceeds versioned bound", len(header))
	}
	if decoded, err := s2DecodeLocalRecord(encoded, scope); err != nil || *decoded != *r {
		t.Fatal("maximum-width scope round trip", err)
	}
	// Even the widest syntactic APPLY counter/digest encoding fits the same
	// cap. Such an index cannot fit this finite journal's replay-record budget.
	apply := s2LocalApplyHeader{identity, math.MaxUint64 - 1, identity, identity, CommitRef{S1Version, identity, identity, identity, identity, math.MaxUint64 - 2, identity}}
	if len(s2TestJSON(t, apply)) > s2LocalMaxHeaderBytes {
		t.Fatal("maximum-width APPLY header exceeds bound")
	}
}
