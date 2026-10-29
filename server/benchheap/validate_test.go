package benchheap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBenchHeapStrictProtocol(t *testing.T) {
	s := Session{Schema: Schema, Protocol: Protocol, RunNonce: strings.Repeat("a", 64), SourceCommit: strings.Repeat("b", 40), SourceTree: strings.Repeat("c", 40), ImageID: "sha256:" + strings.Repeat("d", 64), ContainerID: strings.Repeat("e", 64), BinarySHA256: strings.Repeat("f", 64), UID: 501}
	i := Identity{Session: s, PID: 123, StartNonce: strings.Repeat("1", 64)}
	r := Request{Schema: Schema, Protocol: Protocol, Identity: i, Phase: "baseline", Ordinal: 1, CutNonce: strings.Repeat("2", 64), ExpectedReceipts: 1}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got Request
	if Decode(raw, &got, MaxInputBytes) != nil || got.Validate(i, 1) != nil {
		t.Fatal("valid request rejected")
	}
	for _, bad := range []string{strings.Replace(string(raw), `"ordinal":1`, `"ordinal":1,"ordinal":2`, 1), strings.Replace(string(raw), `"ordinal":1,`, "", 1), strings.Replace(string(raw), `"ordinal":1`, `"ordinal":1,"other":0`, 1), string(raw) + "{}"} {
		if Decode([]byte(bad), &got, MaxInputBytes) == nil {
			t.Fatal("ambiguous or incomplete request accepted")
		}
	}
	sessionRaw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		name string
		raw  string
		out  any
	}{
		{"session_alias", strings.Replace(string(sessionRaw), `"schema":1`, `"schema":2,"Schema":1`, 1), &Session{}},
		{"request_alias", strings.Replace(string(raw), `"schema":1`, `"schema":2,"Schema":1`, 1), &Request{}},
		{"nested_session_alias", strings.Replace(string(raw), `"session":{"schema":1`, `"session":{"schema":2,"Schema":1`, 1), &Request{}},
	} {
		t.Run(input.name, func(t *testing.T) {
			if Decode([]byte(input.raw), input.out, MaxInputBytes) == nil {
				t.Fatal("case-insensitive alias accepted")
			}
		})
	}
	r.ExpectedReceipts = 0
	if r.Validate(i, 1) == nil {
		t.Fatal("missing submitted-work bound accepted")
	}
	r.ExpectedReceipts = 8001
	if r.Validate(i, 1) == nil {
		t.Fatal("unbounded submitted work accepted")
	}
	r.ExpectedReceipts = 1
	if r.Validate(i, 2) == nil {
		t.Fatal("phase replay accepted")
	}
	r.Identity.StartNonce = strings.Repeat("3", 64)
	if r.Validate(i, 1) == nil {
		t.Fatal("foreign process accepted")
	}
}
