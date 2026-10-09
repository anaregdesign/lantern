package security

import "testing"

func TestS2CTrustCurrentProfileEnrollment(t *testing.T) {
	for name, edit := range map[string]func(*s2cBootstrap){
		"mixed profiles":      func(b *s2cBootstrap) { b.Origins[0].Profile = s2cTestProfile() },
		"voting key":          func(b *s2cBootstrap) { b.Origins[0].PublicKey = b.Members[1].PublicKey },
		"missing namespace":   func(b *s2cBootstrap) { b.Origins[0].Namespace = 0 },
		"duplicate namespace": func(b *s2cBootstrap) { b.Origins[1].Namespace = b.Origins[0].Namespace },
		"arbitrary contract":  func(b *s2cBootstrap) { b.Origins[0].Profile.ConsumeContract[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := authorityTestFixture(t, 3)
			b := s2cBootstrap{f.genesis, f.members, f.origins, f.trust.bounds}
			edit(&b)
			if _, err := newS2CTrust(b); err == nil {
				t.Fatal("invalid v2 enrollment accepted")
			}
		})
	}
}

func TestS2CTrustIndependentImmutableBootstrap(t *testing.T) {
	f := s2cTestCluster(t, 3)
	t0 := f.trust
	clone, err := newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()})
	if err != nil || clone.scope != t0.scope || clone.memberSet != t0.memberSet || clone.majority() != 2 {
		t.Fatal("deterministic trust", err)
	}
	// Caller-owned bootstrap slices/state never remain shared with trust.
	f.members[0].PublicKey[0] ^= 1
	f.origins[0].Incarnation[0] ^= 1
	f.genesis.state.projection.lineage[testIdentity()]++
	if t0.members[0].PublicKey == f.members[0].PublicKey || t0.origins[0].Incarnation == f.origins[0].Incarnation || t0.genesis.state.projection.lineage[testIdentity()] == f.genesis.state.projection.lineage[testIdentity()] {
		t.Fatal("mutable bootstrap alias")
	}
	if _, ok := t0.member(999); ok {
		t.Fatal("unknown member")
	}
	if _, ok := t0.origin(999); ok {
		t.Fatal("unknown origin")
	}
}

func TestS2CTrustRejectsInvalidBootstrap(t *testing.T) {
	cases := map[string]func(*s2cBootstrap){
		"nil genesis":   func(b *s2cBootstrap) { b.Genesis = nil },
		"nil snapshot":  func(b *s2cBootstrap) { b.Genesis.state.projection.snapshot = nil },
		"small set":     func(b *s2cBootstrap) { b.Members = b.Members[:1] },
		"even set":      func(b *s2cBootstrap) { b.Members = b.Members[:2] },
		"duplicate id":  func(b *s2cBootstrap) { b.Members[1].ID = b.Members[0].ID },
		"duplicate key": func(b *s2cBootstrap) { b.Members[1].PublicKey = b.Members[0].PublicKey },
		"unsorted":      func(b *s2cBootstrap) { b.Members[0], b.Members[1] = b.Members[1], b.Members[0] },
		"no proposer": func(b *s2cBootstrap) {
			for i := range b.Members {
				b.Members[i].Proposer = false
			}
		},
		"unknown owner":        func(b *s2cBootstrap) { b.Origins[0].Member = 999 },
		"duplicate origin":     func(b *s2cBootstrap) { b.Origins[1].ID = b.Origins[0].ID },
		"duplicate origin key": func(b *s2cBootstrap) { b.Origins[1].PublicKey = b.Origins[0].PublicKey },
		"missing incarnation":  func(b *s2cBootstrap) { b.Origins[0].Incarnation = [16]byte{} },
		"invalid profile":      func(b *s2cBootstrap) { b.Origins[0].Profile.Version++ },
		"missing origins":      func(b *s2cBootstrap) { b.Origins = nil },
		"membership mismatch":  func(b *s2cBootstrap) { b.Genesis.state.membership[0] ^= 1 },
		"prefix mismatch":      func(b *s2cBootstrap) { b.Genesis.state.prefix[0] ^= 1 },
		"full H ceiling":       func(b *s2cBootstrap) { b.Bounds.HistoricalBytes = s2cMaxHistoricalBytes + 1 },
		"zero metadata":        func(b *s2cBootstrap) { b.Bounds.HeaderBytes = 0 },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := s2cTestCluster(t, 3)
			b := s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()}
			edit(&b)
			if _, err := newS2CTrust(b); err == nil {
				t.Fatal("invalid bootstrap accepted")
			}
		})
	}
	for _, n := range []int{3, 5, 31} {
		f := s2cTestCluster(t, n)
		if f.trust.majority() != n/2+1 {
			t.Fatal("majority")
		}
	}
}

func TestS2CTrustNoncyclicScopeBinding(t *testing.T) {
	f := s2cTestCluster(t, 3)
	b := s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()}
	b.Bounds.HistoricalBytes--
	other, err := newS2CTrust(b)
	if err != nil || other.scope == f.trust.scope || other.originDigests[1] != f.trust.originDigests[1] {
		t.Fatal("common bounds/origin independence", err)
	}
	b.Bounds = s2cTestBounds()
	b.Origins[0].Incarnation[0]++
	other, err = newS2CTrust(b)
	if err != nil || other.scope == f.trust.scope || other.originDigests[1] == f.trust.originDigests[1] || other.originDigests[2] != f.trust.originDigests[2] {
		t.Fatal("origin incarnation binding", err)
	}
}
