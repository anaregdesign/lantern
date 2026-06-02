package cmd

import (
	"reflect"
	"testing"
	"time"
)

func TestParseValue(t *testing.T) {
	rfc := "2025-01-02T03:04:05Z"
	parsedTime, _ := time.Parse(time.RFC3339, rfc)

	tests := []struct {
		name      string
		raw       string
		valueType string
		want      any
		wantErr   bool
	}{
		// auto
		{"auto/int", "42", "auto", 42, false},
		{"auto/float", "3.14", "auto", 3.14, false},
		{"auto/bool", "true", "auto", true, false},
		{"auto/datetime", rfc, "auto", parsedTime, false},
		{"auto/string", "hello", "auto", "hello", false},
		{"auto/empty-default", "x", "", "x", false},
		// string
		{"string/keeps-leading-zero", "01234", "string", "01234", false},
		{"string/keeps-numeric", "42", "string", "42", false},
		// int
		{"int/ok", "42", "int", 42, false},
		{"int/err", "x", "int", nil, true},
		// float
		{"float/ok", "3.14", "float", 3.14, false},
		{"float/err", "x", "float", nil, true},
		// bool
		{"bool/ok", "true", "bool", true, false},
		{"bool/err", "yes", "bool", nil, true},
		// datetime
		{"datetime/ok", rfc, "datetime", parsedTime, false},
		{"datetime/err", "yesterday", "datetime", nil, true},
		// json
		{"json/object", `{"a":1}`, "json", map[string]any{"a": float64(1)}, false},
		{"json/array", `[1,2]`, "json", []any{float64(1), float64(2)}, false},
		{"json/scalar", `"hi"`, "json", "hi", false},
		{"json/err", `{`, "json", nil, true},
		// unknown
		{"unknown-type", "x", "weird", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseValue(tc.raw, tc.valueType)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v (%T), want %#v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}
