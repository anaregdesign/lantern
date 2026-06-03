package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// cursorVersion is the only currently understood cursor encoding. Bumping
// it is a wire-compatibility change — clients sitting on an older cursor
// would receive an error when they pass it back. Add new fields by tagging
// them omitempty and keeping the version stable; bump the version only when
// the meaning of an existing field changes.
const cursorVersion uint8 = 1

// scanCursor is the opaque pagination cursor returned by ScanVertices and
// echoed back by callers. The wire representation is base64-wrapped JSON so
// the bytes survive HTTP/JSON transport unchanged and so future fields can
// be added without breaking older clients (omitempty + version byte).
//
// LastKey holds the last vertex key returned in the previous page. The next
// page resumes with the first key strictly greater than LastKey within the
// same prefix.
type scanCursor struct {
	Version uint8  `json:"v"`
	LastKey string `json:"k"`
}

// encodeCursor packs a scanCursor into the opaque bytes returned to clients.
// Returns nil for the "end of stream" sentinel — callers should pass an
// empty LastKey when they intend to signal completion.
func encodeCursor(c scanCursor) []byte {
	c.Version = cursorVersion
	raw, err := json.Marshal(c)
	if err != nil {
		// json.Marshal of a tiny fixed-shape struct cannot fail in
		// practice; degrade to an empty cursor rather than crash the
		// RPC. A surfaced error here would tell us nothing actionable.
		return nil
	}
	out := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(out, raw)
	return out
}

// cursorEnvelope is the union of every Scan* RPC's cursor payload shape.
// We decode through it once so we can detect cross-RPC cursor reuse (e.g.
// a ScanVertices next_cursor being fed back into ScanEdges) and return a
// friendly error rather than silently restarting the scan. Each Scan* RPC
// owns exactly one shape:
//
//   - ScanVertices  -> {v, k}           (LastKey populated)
//   - ScanEdges     -> {v, t, h}        (LastTail and/or LastHead populated)
//
// json.Unmarshal silently ignores unknown fields, so a vertex cursor parsed
// as scanEdgesCursor would otherwise yield an all-zero "start over" cursor
// — exactly the bug #168 is about.
type cursorEnvelope struct {
	Version  uint8  `json:"v"`
	LastKey  string `json:"k,omitempty"`
	LastTail string `json:"t,omitempty"`
	LastHead string `json:"h,omitempty"`
}

// decodeEnvelope is the shared base64+JSON+version unwrapper used by both
// concrete cursor decoders. It returns the populated envelope so callers
// can apply the shape check appropriate to their RPC.
func decodeEnvelope(b []byte) (cursorEnvelope, error) {
	raw := make([]byte, base64.RawURLEncoding.DecodedLen(len(b)))
	n, err := base64.RawURLEncoding.Decode(raw, b)
	if err != nil {
		return cursorEnvelope{}, fmt.Errorf("decode cursor: %w", err)
	}
	var env cursorEnvelope
	if err := json.Unmarshal(raw[:n], &env); err != nil {
		return cursorEnvelope{}, fmt.Errorf("decode cursor: %w", err)
	}
	if env.Version != cursorVersion {
		return cursorEnvelope{}, fmt.Errorf("decode cursor: unsupported version %d", env.Version)
	}
	return env, nil
}

// decodeCursor unpacks the bytes a client sent back. Empty input is the
// "start from the beginning" sentinel and is not an error. Malformed input
// is rejected so misbehaving clients cannot silently restart their scan.
// A cursor issued by a different Scan* RPC is also rejected — see
// cursorEnvelope.
func decodeCursor(b []byte) (scanCursor, error) {
	if len(b) == 0 {
		return scanCursor{}, nil
	}
	env, err := decodeEnvelope(b)
	if err != nil {
		return scanCursor{}, err
	}
	if env.LastTail != "" || env.LastHead != "" {
		return scanCursor{}, fmt.Errorf("decode cursor: cursor was issued by a different Scan RPC (expected ScanVertices cursor)")
	}
	return scanCursor{Version: env.Version, LastKey: env.LastKey}, nil
}

// scanEdgesCursor pages ScanEdges by the last (tail, head) pair returned.
// Wire format mirrors scanCursor: a versioned, base64-wrapped JSON blob
// opaque to clients. The next page resumes at the first edge whose
// (tail, head) is strictly greater (lexicographic, tail dominant) than
// (LastTail, LastHead) within the request's tail/head prefix.
type scanEdgesCursor struct {
	Version  uint8  `json:"v"`
	LastTail string `json:"t"`
	LastHead string `json:"h"`
}

func encodeEdgesCursor(c scanEdgesCursor) []byte {
	c.Version = cursorVersion
	raw, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	out := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(out, raw)
	return out
}

func decodeEdgesCursor(b []byte) (scanEdgesCursor, error) {
	if len(b) == 0 {
		return scanEdgesCursor{}, nil
	}
	env, err := decodeEnvelope(b)
	if err != nil {
		return scanEdgesCursor{}, err
	}
	if env.LastKey != "" {
		return scanEdgesCursor{}, fmt.Errorf("decode cursor: cursor was issued by a different Scan RPC (expected ScanEdges cursor)")
	}
	return scanEdgesCursor{Version: env.Version, LastTail: env.LastTail, LastHead: env.LastHead}, nil
}
