package service

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
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

// TestCursor_RejectCrossRPC pins the #168 behaviour: an edge cursor
// (LastTail / LastHead populated) handed to decodeCursor — and a vertex
// cursor (LastKey populated) handed to decodeEdgesCursor — must each be
// rejected with a friendly error, not silently re-anchored to the start
// of the scan. The error string is asserted because handlers forward it
// verbatim into the codes.InvalidArgument status returned to clients.
func TestCursor_RejectCrossRPC(t *testing.T) {
	edgeCursor := encodeEdgesCursor(scanEdgesCursor{LastTail: "users/", LastHead: "posts/42"})
	if _, err := decodeCursor(edgeCursor); err == nil {
		t.Fatalf("decodeCursor(edge cursor) = nil error, want rejection")
	} else if !strings.Contains(err.Error(), "different Scan RPC") {
		t.Errorf("decodeCursor(edge cursor) error = %q, want 'different Scan RPC'", err)
	}

	vertexCursor := encodeCursor(scanCursor{LastKey: "users/42"})
	if _, err := decodeEdgesCursor(vertexCursor); err == nil {
		t.Fatalf("decodeEdgesCursor(vertex cursor) = nil error, want rejection")
	} else if !strings.Contains(err.Error(), "different Scan RPC") {
		t.Errorf("decodeEdgesCursor(vertex cursor) error = %q, want 'different Scan RPC'", err)
	}
}

// FuzzDecodeCursor feeds arbitrary bytes to all three Scan* cursor decoders.
// None may panic on any input, and a cursor minted by one Scan RPC must never
// be silently accepted by another (the #168 / #674 cross-RPC-reuse guard).
// Only panic-freedom is asserted here; the precise cross-RPC rejection is
// pinned by the unit tests above.
func FuzzDecodeCursor(f *testing.F) {
	f.Add(encodeCursor(scanCursor{LastKey: "alice"}))
	f.Add(encodeEdgesCursor(scanEdgesCursor{LastTail: "a", LastHead: "b"}))
	f.Add(encodeKeysCursor(scanKeysCursor{LastKey: "k"}))
	f.Add([]byte(nil))
	f.Add([]byte("!!!not base64!!!"))
	f.Add([]byte("////"))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = decodeCursor(b)
		_, _ = decodeEdgesCursor(b)
		_, _ = decodeKeysCursor(b)
	})
}

// FuzzCursorRoundTrip asserts the encode→decode identity for ScanVertices
// cursors over arbitrary keys. JSON cannot carry invalid UTF-8 (it is replaced
// with U+FFFD on the way through json.Marshal), and vertex keys are always
// valid UTF-8 proto strings, so non-UTF-8 inputs are out of contract and
// skipped rather than treated as failures.
func FuzzCursorRoundTrip(f *testing.F) {
	f.Add("")
	f.Add("alice")
	f.Add("users/42")
	f.Add("emoji-✓-tab\tnewline\n")
	f.Fuzz(func(t *testing.T, key string) {
		if !utf8.ValidString(key) {
			return
		}
		got, err := decodeCursor(encodeCursor(scanCursor{LastKey: key}))
		if err != nil {
			t.Fatalf("decode(encode(%q)): %v", key, err)
		}
		if got.LastKey != key {
			t.Fatalf("round-trip key = %q, want %q", got.LastKey, key)
		}
	})
}
