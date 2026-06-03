package service

import (
	"encoding/base64"
	"testing"
)

func TestCursor_RoundTrip(t *testing.T) {
	cases := []string{
		"",
		"a",
		"users/42",
		"emoji-✓-binary\x00bytes",
	}
	for _, last := range cases {
		t.Run(last, func(t *testing.T) {
			b := encodeCursor(scanCursor{LastKey: last})
			got, err := decodeCursor(b)
			if err != nil {
				t.Fatalf("decodeCursor: %v", err)
			}
			if got.LastKey != last {
				t.Errorf("LastKey = %q, want %q", got.LastKey, last)
			}
			if got.Version != cursorVersion {
				t.Errorf("Version = %d, want %d", got.Version, cursorVersion)
			}
		})
	}
}

func TestCursor_DecodeEmpty(t *testing.T) {
	c, err := decodeCursor(nil)
	if err != nil {
		t.Fatalf("decodeCursor(nil): %v", err)
	}
	if c.LastKey != "" {
		t.Errorf("LastKey = %q, want empty", c.LastKey)
	}
}

func TestCursor_DecodeMalformed(t *testing.T) {
	if _, err := decodeCursor([]byte("not-base64!@#$")); err == nil {
		t.Errorf("expected error for malformed base64")
	}
	// Valid base64 but not JSON.
	notJSON := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	if _, err := decodeCursor([]byte(notJSON)); err == nil {
		t.Errorf("expected error for non-JSON payload")
	}
}

func TestCursor_RejectUnknownVersion(t *testing.T) {
	raw := []byte(`{"v":99,"k":"x"}`)
	b := []byte(base64.RawURLEncoding.EncodeToString(raw))
	if _, err := decodeCursor(b); err == nil {
		t.Errorf("expected error for unknown version")
	}
}
