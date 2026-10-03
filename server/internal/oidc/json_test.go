package oidc

import (
	"strings"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	for _, raw := range []string{`{"iss":"a","iss":"b"}`, `{"x":{"a":1,"a":2}}`, `{"x":1} {}`, `[}`, `{"x":"` + string([]byte{0xff}) + `"}`,
		strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18), `{"a":[` + strings.Repeat("0,", 10001) + "0]}"} {
		var out any
		if err := decodeJSON([]byte(raw), &out); err == nil {
			t.Fatal("ambiguous or unbounded document accepted")
		}
	}
	var out struct {
		Issuer string `json:"iss"`
	}
	if err := decodeJSON([]byte(`{"iss":"https://idp.example","extension":[null,true,1.5]}`), &out); err != nil || out.Issuer != "https://idp.example" {
		t.Fatalf("valid extensible JSON: %v", err)
	}
}
