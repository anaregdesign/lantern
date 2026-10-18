package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// queryFixtureInput supplies untrusted expected metadata for parser/capability tests.
// It does not construct an authority, Admission or serving-time qualification.
func queryFixtureInput(t *testing.T) fixtureInput {
	t.Helper()
	profile := &pb.CurrentAuthorityProfile{Version: 2, Domain: bytes.Repeat([]byte{1}, 32), Cohort: bytes.Repeat([]byte{2}, 32), Generation: bytes.Repeat([]byte{3}, 16), Protocol: bytes.Repeat([]byte{4}, 32), TimeProfile: bytes.Repeat([]byte{5}, 32), Membership: bytes.Repeat([]byte{6}, 32), Configuration: bytes.Repeat([]byte{7}, 32)}
	var f fixtureInput
	raw := `{"nodes":[{"public_origin":"https://localhost:6380"},{"public_origin":"https://localhost:6381"},{"public_origin":"https://localhost:6382"}],"ca_file":"local.pem","protected_query":{"mode":"oidc","security_profile":"current-v2","fixture_id":"11111111111111111111111111111111","reader_role":"fixture_query_reader","reader_subject":"fixture-query-reader","server":{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"exporter":{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}`
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	f.Query.ExpiresAt = time.Now().Add(5 * time.Minute)
	f.Query.CurrentProfile = profile
	f.Query.CurrentBinding = "current-v2:" + strings.Repeat("a", 64)
	return f
}
