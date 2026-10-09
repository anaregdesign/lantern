package security

import "testing"

func TestAuthorityRenewalCertificate(t *testing.T) {
	for _, count := range []int{3, 31} {
		n := s2cTestNativeCluster(t, count, nil)
		workloads := [32]byte{7}
		raw := testAuthorityRenewalRequest(t, n, 1, workloads)
		request, err := parseAuthorityRenewalRequest(raw, n.fixture.trust, workloads)
		if err != nil {
			t.Fatal(err)
		}
		votes := make([]authorityRenewalVote, 0, count)
		for id := uint32(count); id > 0; id-- {
			v, err := n.nodes[id].signAuthorityRenewal(raw, workloads, func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			vote, err := parseAuthorityRenewalVote(v, request, n.fixture.trust)
			if err != nil {
				t.Fatal(err)
			}
			votes = append(votes, vote)
		}
		certificate, err := encodeAuthorityRenewalCertificate(raw, votes, n.fixture.trust, workloads)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parseAuthorityRenewalCertificate([]byte(certificate.raw), n.fixture.trust, workloads)
		if err != nil || len(parsed.votes) != count || len(certificate.raw) > authorityRenewalMaxCertificate {
			t.Fatal("bounded certificate", err)
		}
		for i, v := range parsed.votes {
			if v.member != uint32(i+1) {
				t.Fatal("noncanonical voter order")
			}
		}
		if _, err := encodeAuthorityRenewalCertificate(raw, votes[:n.fixture.trust.majority()-1], n.fixture.trust, workloads); err == nil {
			t.Fatal("minority certificate")
		}
		duplicate := append([]authorityRenewalVote(nil), votes...)
		duplicate[0] = duplicate[1]
		if _, err := encodeAuthorityRenewalCertificate(raw, duplicate, n.fixture.trust, workloads); err == nil {
			t.Fatal("duplicate certificate")
		}
		for _, mutate := range []func([]byte) []byte{
			func(b []byte) []byte { return b[:len(b)-1] }, func(b []byte) []byte { return append(b, 0) },
			func(b []byte) []byte { b[len(authorityRenewalCertificateDomain)] = 255; return b },
			func(b []byte) []byte { b[len(b)-1] ^= 1; return b },
		} {
			if _, err := parseAuthorityRenewalCertificate(mutate([]byte(certificate.raw)), n.fixture.trust, workloads); err == nil {
				t.Fatal("malformed certificate")
			}
		}
	}
}
