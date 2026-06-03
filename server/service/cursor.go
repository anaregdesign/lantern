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

// decodeCursor unpacks the bytes a client sent back. Empty input is the
// "start from the beginning" sentinel and is not an error. Malformed input
// is rejected so misbehaving clients cannot silently restart their scan.
func decodeCursor(b []byte) (scanCursor, error) {
	if len(b) == 0 {
		return scanCursor{}, nil
	}
	raw := make([]byte, base64.RawURLEncoding.DecodedLen(len(b)))
	n, err := base64.RawURLEncoding.Decode(raw, b)
	if err != nil {
		return scanCursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var c scanCursor
	if err := json.Unmarshal(raw[:n], &c); err != nil {
		return scanCursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	if c.Version != cursorVersion {
		return scanCursor{}, fmt.Errorf("decode cursor: unsupported version %d", c.Version)
	}
	return c, nil
}
