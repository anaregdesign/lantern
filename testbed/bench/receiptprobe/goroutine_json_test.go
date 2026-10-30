package main

import "testing"

func TestGoroutineCanonicalJSON(t *testing.T) {
	type nested struct {
		Count *int64 `json:"count"`
	}
	type value struct {
		Schema   int     `json:"schema"`
		Optional *nested `json:"optional,omitempty"`
	}
	for _, good := range []string{`{"schema":1}`, `{"schema":1,"optional":{"count":0}}`} {
		var v value
		if err := decodeGoroutineJSON([]byte(good), &v); err != nil {
			t.Fatal("canonical optional/pointer field refused", err)
		}
	}
	for _, bad := range []string{`{"Schema":1}`, `{"schema":1,"schema":1}`, `{"schema":1,"unknown":0}`, `{}`, `{"schema":1} {}`, `{"schema":1,"optional":null}`, `{"schema":1,"optional":{"Count":0}}`, `{"schema":1,"optional":{"count":0,"count":0}}`, `{"schema":1,"optional":{}}`} {
		var v value
		if decodeGoroutineJSON([]byte(bad), &v) == nil {
			t.Fatal("noncanonical candidate JSON accepted")
		}
	}
}
