package security

import (
	"google.golang.org/protobuf/types/known/wrapperspb"
	"strings"
	"testing"
)

func TestAuthorityOutputCodecBoundsBeforeEncoding(t *testing.T) {
	c := authorityOutputCodec{}
	m := wrapperspb.String("exact")
	raw, err := c.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got wrapperspb.StringValue
	if err := c.Unmarshal(raw, &got); err != nil || got.Value != "exact" {
		t.Fatal(err)
	}
	if _, err := c.Marshal(wrapperspb.String(strings.Repeat("x", authorityOutputUnitBytes))); err == nil {
		t.Fatal("oversize encoding")
	}
	if _, err := c.Marshal("not protobuf"); err == nil {
		t.Fatal("unknown encoder")
	}
	if err := c.Unmarshal(make([]byte, authorityOutputUnitBytes+1), &got); err == nil {
		t.Fatal("oversize ingress")
	}
	if len(authorityOutputOptions()) != 5 {
		t.Fatal("missing private codec limits")
	}
}
