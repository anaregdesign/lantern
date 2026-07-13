package search

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Document is the indexable payload handed to an Indexer: anything that can
// serialize itself to the text to be analyzed. It is structurally identical to
// fmt.Stringer, so any existing Stringer already satisfies it. Implementing
// String lets a richer record decide which of its fields become searchable
// (for example concatenating a title and a body) instead of forcing callers to
// pre-flatten every document into a bare string.
type Document interface {
	String() string
}

// FieldID is a stable semantic search field. FieldDefault preserves the
// single-text Document contract; FieldKey and FieldValue let layered stores
// keep identifier and content evidence separate without importing their
// domain types into core.
type FieldID uint8

const (
	FieldDefault FieldID = iota
	FieldKey
	FieldValue
	numDocumentFields
)

// DocumentField is one independently analyzed field instance. Multiple
// FieldValue entries are allowed (for example JSON string leaves); phrase and
// proximity never cross from one instance to another.
type DocumentField struct {
	ID   FieldID
	Text string
}

// FieldedDocument exposes structured search fields. It remains a Document so
// existing generic index bounds and diagnostics keep a plain-text fallback,
// but Prepare uses SearchFields directly when this interface is present.
type FieldedDocument interface {
	Document
	SearchFields() []DocumentField
}

// Fields is a convenient immutable FieldedDocument for callers that already
// have projected text instances.
type Fields []DocumentField

func (f Fields) SearchFields() []DocumentField { return f }

func (f Fields) String() string {
	var b strings.Builder
	for _, field := range f {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(field.Text)
	}
	return b.String()
}

// SizedDocument can expose a cheap upper bound before String performs an
// expensive projection. The production vertex projection uses it to reject a
// large JSON/string payload before parsing or projecting its fields.
type SizedDocument interface {
	Document
	SizeHint() int
}

// Text adapts a plain string to Document so raw text can be indexed without a
// bespoke wrapper type: idx.Index(id, Text("some words")).
type Text string

// String returns the underlying text, making Text a Document.
func (t Text) String() string { return string(t) }

// The adapters below cover the remaining value kinds Lantern stores in a
// Vertex, so any scalar value is indexable the same way as Text — without a
// bespoke wrapper — by rendering itself to the text the Analyzer tokenizes.
// Their widths follow the SDK's read accessors (IntValue → int64, UIntValue →
// uint64, FloatValue → float64), so a narrower value is wrapped with a plain
// conversion, e.g. Int(int8(7)). The present-nil tombstone (VertexKindNil) has
// no adapter on purpose: it carries no searchable text, and indexing an empty
// Document is already a no-op.

// Int adapts a signed integer to Document, rendered in base 10.
type Int int64

// String returns the base-10 text of the integer, making Int a Document.
func (i Int) String() string { return strconv.FormatInt(int64(i), 10) }

// Uint adapts an unsigned integer to Document, rendered in base 10.
type Uint uint64

// String returns the base-10 text of the integer, making Uint a Document.
func (u Uint) String() string { return strconv.FormatUint(uint64(u), 10) }

// Float adapts a floating-point number to Document, using the shortest decimal
// that round-trips back to the same float64 (e.g. 3.14, 1e+20).
type Float float64

// String returns the shortest round-tripping decimal, making Float a Document.
func (f Float) String() string { return strconv.FormatFloat(float64(f), 'g', -1, 64) }

// Bool adapts a boolean to Document as "true" or "false".
type Bool bool

// String returns "true" or "false", making Bool a Document.
func (b Bool) String() string { return strconv.FormatBool(bool(b)) }

// Bytes adapts valid UTF-8 to text. Arbitrary binary bytes produce an empty
// document instead of accidentally entering the text analyzer.
type Bytes []byte

// String returns the bytes interpreted as text, making Bytes a Document.
func (b Bytes) String() string {
	if !utf8.Valid(b) {
		return ""
	}
	return string(b)
}

// Time adapts a timestamp to Document, formatted as RFC3339 with nanosecond
// precision — the same rendering Vertex timestamps use elsewhere.
type Time time.Time

// String returns the RFC3339Nano text of the timestamp, making Time a Document.
func (t Time) String() string { return time.Time(t).Format(time.RFC3339Nano) }

// Duration adapts a duration to Document in its canonical Go form (e.g. "1h30m0s").
type Duration time.Duration

// String returns the canonical duration text, making Duration a Document.
func (d Duration) String() string { return time.Duration(d).String() }
