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

func TestValidateOperatorJSON(t *testing.T) {
	raw := []byte(`{"provisioning":[` + strings.Repeat("0,", 10001) + "0]}")
	if ValidateOperatorJSON(raw) != nil {
		t.Fatal("bounded original provisioning array rejected")
	}
	if ValidateJSON(raw) == nil {
		t.Fatal("operator validation widened the existing document budget")
	}
	for _, invalid := range [][]byte{
		[]byte(`{"node":{"key":1,"key":2}}`),
		[]byte(strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18)),
		[]byte(strings.Repeat(" ", 120<<10+1)),
		[]byte(`{"private":"` + string([]byte{0xff}) + `"}`),
	} {
		if ValidateOperatorJSON(invalid) == nil {
			t.Fatal("ambiguous or unbounded operator input accepted")
		}
	}
}
