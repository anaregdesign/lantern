package search

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// Normalizer rewrites raw text before tokenization—for example folding case or
// collapsing whitespace. Implementations in this package are stateless and
// therefore safe for concurrent use by multiple goroutines.
type Normalizer interface {
	Normalize(text string) string
}

// NormalizerFunc adapts an ordinary function to the Normalizer interface.
type NormalizerFunc func(text string) string

// Normalize calls f(text).
func (f NormalizerFunc) Normalize(text string) string { return f(text) }

// LowercaseNormalizer folds text to lower case with Unicode case mapping, so
// matching is case-insensitive independent of language.
type LowercaseNormalizer struct{}

// Normalize returns the lower-cased text.
func (LowercaseNormalizer) Normalize(text string) string { return strings.ToLower(text) }

// CaseFoldNormalizer applies Unicode's language-neutral full case fold. Unlike
// lowercasing, case folding expands equivalences such as German sharp-s to
// "ss" and maps both Greek sigma forms to sigma. It deliberately does not
// apply locale-specific mappings (for example Turkish dotted/dotless I).
type CaseFoldNormalizer struct{}

// Normalize returns the Unicode case-folded text.
func (CaseFoldNormalizer) Normalize(text string) string { return cases.Fold().String(text) }

// CanonicalNormalizer converts canonically equivalent spellings to NFC while
// preserving every combining mark. It is the conservative production choice:
// composed/decomposed input matches, but Thai tones, Indic viramas/matras,
// accents, niqqud, and harakat are never erased as a recall shortcut.
type CanonicalNormalizer struct{}

// Normalize returns text in Unicode NFC.
func (CanonicalNormalizer) Normalize(text string) string { return norm.NFC.String(text) }

// SpaceNormalizer collapses every run of Unicode whitespace to a single ASCII
// space and trims the ends. It is useful in front of a WhitespaceTokenizer when
// the input may contain tabs, newlines, or repeated spaces.
type SpaceNormalizer struct{}

// Normalize collapses internal whitespace runs to single spaces.
func (SpaceNormalizer) Normalize(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// PunctuationNormalizer replaces every Unicode punctuation or symbol rune with
// an ASCII space, turning marks such as '.', ',', '。', and '、' into word
// boundaries independent of language. It does not merge the spaces it
// introduces; chain SpaceNormalizer after it to collapse the resulting runs and
// trim the ends. Unlike PunctuationFilter, which only trims marks from the
// edges of an already-formed token (so "node-1" stays intact), this splits on
// every mark, so "node-1" becomes "node 1".
type PunctuationNormalizer struct{}

// Normalize replaces each punctuation or symbol rune with a space.
func (PunctuationNormalizer) Normalize(text string) string {
	return strings.Map(func(r rune) rune {
		if isPunctOrSymbol(r) {
			return ' '
		}
		return r
	}, text)
}

// SymbolPreservingPunctuationNormalizer replaces Unicode punctuation with a
// boundary while retaining symbols as searchable content. Production search
// uses it so emoji and intentional mathematical/currency symbols do not vanish;
// callers that want the older punctuation-and-symbol boundary policy can keep
// using PunctuationNormalizer.
type SymbolPreservingPunctuationNormalizer struct{}

// Normalize replaces punctuation, but not symbols, with an ASCII space.
func (SymbolPreservingPunctuationNormalizer) Normalize(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return ' '
		}
		return r
	}, text)
}

// EmojiPresentationNormalizer removes the text/emoji presentation selectors
// U+FE0E and U+FE0F. Presentation is a rendering preference rather than lexical
// identity, so "❤", "❤︎", and "❤️" share one searchable term. ZWJ and skin-tone
// modifiers remain significant.
type EmojiPresentationNormalizer struct{}

// Normalize removes Unicode variation selectors 15 and 16.
func (EmojiPresentationNormalizer) Normalize(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\ufe0e' || r == '\ufe0f' {
			return -1
		}
		return r
	}, text)
}

// DiacriticNormalizer folds accents and other nonspacing combining marks so
// matching is diacritic-insensitive: it decomposes text to Unicode NFD, drops
// every rune in category Mn (nonspacing mark), then recomposes to NFC. This
// makes "café" match "cafe", "naïve" match "naive", Greek "Ελλάδα" match
// "Ελλαδα", and Russian "ёж" match "еж" — across any script that encodes its
// accents as combining marks (Latin, Greek, Cyrillic, Vietnamese, …). It does
// not transliterate between scripts and never alters a base letter, so letters
// that are atomic rather than base-plus-mark — "ß", "ø", "ł" — are left intact.
//
// Because it removes every nonspacing mark, it also strips the optional
// harakat of Arabic, the niqqud of Hebrew, and the (non-optional) vowel marks
// of Thai, trading some precision for recall in those scripts. It preserves
// spacing combining marks (category Mc), so Brahmic base+matra clusters such as
// Devanagari "भारत" keep their vowels, and the NFC recomposition keeps Hangul
// syllables and other composed forms stable. It folds independently of case,
// so its order relative to LowercaseNormalizer does not matter, and it is a
// no-op for scripts written without combining marks (CJK, Hangul).
type DiacriticNormalizer struct{}

// Normalize strips nonspacing combining marks via NFD → drop Mn → NFC.
func (DiacriticNormalizer) Normalize(text string) string {
	decomposed := norm.NFD.String(text)
	stripped := strings.Map(dropNonspacingMark, decomposed)
	return norm.NFC.String(stripped)
}

// WidthNormalizer folds full-width and half-width characters to their normal
// width with Unicode width folding, so the same word written at either width
// indexes and queries identically — a frequent need for CJK input. Full-width
// Latin letters, digits, and punctuation collapse to ASCII ("ＴＯＫＹＯ" → "TOKYO",
// "２０２４" → "2024", "！" → "!"), and half-width katakana expands to full-width
// ("ｶﾀｶﾅ" → "カタカナ"). It does not fold case, strip accents, or expand
// compatibility forms such as ligatures; chain LowercaseNormalizer and
// DiacriticNormalizer for those. Run it early — before PunctuationNormalizer —
// so width-folded punctuation is then treated as a word boundary. It is a no-op
// for text that already uses only normal-width characters, ordinary CJK
// ideographs ("東京") included.
type WidthNormalizer struct{}

// Normalize folds full-width and half-width variants to their normal width.
func (WidthNormalizer) Normalize(text string) string {
	return width.Fold.String(text)
}

// JSONStringValueNormalizer rewrites text that is a JSON object or array into
// just the text of its string values, dropping every object field name (key)
// and every non-string scalar (number, boolean, null). Its purpose is to keep
// a full-text index free of JSON structure: when a vertex stores a serialized
// document such as {"role":"admin","score":9,"active":true}, only the human
// content ("admin") becomes searchable — not the field names ("role", "score",
// "active") and not the numeric or boolean scalars. String values are emitted
// in object-key sorted order (arrays keep their order) so the output is
// deterministic and re-indexing is idempotent.
//
// Text that is not a JSON object or array passes through unchanged: a fast path
// returns the input verbatim when the first non-space byte is neither '{' nor
// '[', and any string that starts that way but fails to parse as JSON is also
// returned verbatim (so a value that merely looks like JSON is still indexed as
// raw text). A JSON object/array carrying no string values normalizes to the
// empty string.
//
// Unlike the other normalizers in this package, this one is meant to run on a
// value in isolation — the document projection of a single stored value — not
// on a composed document such as "key value", because a key concatenated with
// a JSON blob is not itself valid JSON and the fast path would return it
// unchanged. Place it at the projection step, not in a shared analyzer chain.
type JSONStringValueNormalizer struct{}

// Normalize returns the space-joined string values of a JSON object or array,
// or the input unchanged when it is not such a document.
func (JSONStringValueNormalizer) Normalize(text string) string {
	values, structured := (JSONStringValueNormalizer{}).Values(text)
	if !structured {
		return text
	}
	return strings.Join(values, " ")
}

// Values returns each JSON string leaf as a separate deterministic field
// instance. structured is false for plain text and malformed JSON, whose
// caller should preserve the original input as one field.
func (JSONStringValueNormalizer) Values(text string) (values []string, structured bool) {
	trimmed := strings.TrimSpace(text)
	// Only a JSON object or array is treated as structured JSON. Plain prose
	// and bare scalars (including a quoted JSON string) are left untouched, so
	// values like "true" or "42" stay searchable as their literal text.
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, false // looked like JSON but is not: preserve raw text.
	}
	appendJSONStringValueFields(&values, parsed)
	return values, true
}

func appendJSONStringValueFields(out *[]string, v any) {
	switch val := v.(type) {
	case string:
		if val != "" {
			*out = append(*out, val)
		}
	case []any:
		for _, e := range val {
			appendJSONStringValueFields(out, e)
		}
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			appendJSONStringValueFields(out, val[k])
		}
	}
}

// dropNonspacingMark returns r unless r is a Unicode nonspacing combining mark
// (category Mn), in which case it returns -1 so strings.Map removes it.
func dropNonspacingMark(r rune) rune {
	if unicode.Is(unicode.Mn, r) {
		return -1
	}
	return r
}
