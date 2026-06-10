package mcp

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// maxKeyDepth is the advisory ceiling on dot-delimited key segments. Keys
// deeper than this still store, but lintKey nudges toward flatter namespaces
// that are easier to recall and scan.
const maxKeyDepth = 5

// maxLeafWords is the advisory ceiling on alphabetic words in the final key
// segment. A leaf with more words than this reads like free text, which is
// hard to reconstruct verbatim for an exact recall_fact.
const maxLeafWords = 3

// recognizedScopes are the namespace heads the memory instructions teach
// (session.* / task.* / project.* / user.*). A key whose first segment is not
// one of these gets an advisory nudge toward a scope head.
var recognizedScopes = map[string]struct{}{
	"session": {},
	"task":    {},
	"project": {},
	"user":    {},
}

var (
	// isoDateRe matches an ISO calendar date embedded anywhere in a segment.
	isoDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	// longDigitRe matches a run of 6+ digits (YYYYMMDD, epochs, counters).
	longDigitRe = regexp.MustCompile(`\d{6,}`)
	// uuidRe matches a canonical UUID embedded anywhere in a segment.
	uuidRe = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
)

// lintKey returns advisory, non-blocking warnings about a memory key's shape.
// It NEVER rejects a key: the caller always stores the fact and surfaces these
// hints alongside the result, so the model learns the conventions for the
// three recall surfaces (exact recall_fact, prefix list_under, substring
// search_facts) without ever breaking a write.
//
// The same helper backs both remember_fact (singular) and each item of
// remember_facts (batch) so the guidance is identical across both surfaces.
func lintKey(key string) []string {
	var warnings []string

	// Rule: no whitespace. Spaces make a key awkward to type back verbatim
	// for exact recall and read oddly in prefix scans.
	if strings.ContainsFunc(key, unicode.IsSpace) {
		warnings = append(warnings,
			`key contains whitespace; use dot-delimited segments without spaces (e.g. "user.preferences.tone")`)
	}

	// strings.Split always returns at least one element, so segments[0] and
	// the leaf access below are safe even for a degenerate key.
	segments := strings.Split(key, ".")

	// Rule: recognized scope head.
	if _, ok := recognizedScopes[segments[0]]; !ok {
		warnings = append(warnings, fmt.Sprintf(
			`first segment %q is not a recognized scope (session, task, project, user); a scope head keeps memory organized`,
			segments[0]))
	}

	// Rule: high-cardinality token (date / UUID / long digit run) outside the
	// leaf. Such a token in a non-final segment fragments the prefix tree that
	// list_under and search_facts walk; it belongs in the leaf if anywhere.
	if len(segments) > 1 {
		for _, seg := range segments[:len(segments)-1] {
			if isHighCardinality(seg) {
				warnings = append(warnings, fmt.Sprintf(
					`high-cardinality token %q is not the final segment; dates/UUIDs mid-key fragment prefix scans — move them to the leaf`,
					seg))
				break
			}
		}
	}

	// Rule: free-text / multi-word leaf.
	leaf := segments[len(segments)-1]
	if leafWordCount(leaf) > maxLeafWords {
		warnings = append(warnings, fmt.Sprintf(
			`leaf %q reads like free text (%d+ words); a concise canonical leaf is easier to reconstruct for exact recall`,
			leaf, maxLeafWords+1))
	}

	// Rule: excessive depth.
	if len(segments) > maxKeyDepth {
		warnings = append(warnings, fmt.Sprintf(
			"key has %d segments; deep nesting is hard to recall — prefer %d or fewer",
			len(segments), maxKeyDepth))
	}

	return warnings
}

// isHighCardinality reports whether a single key segment contains a date,
// UUID, or long digit run — tokens that vary per write and so bloat a prefix
// tree if they sit anywhere but the leaf.
func isHighCardinality(seg string) bool {
	return uuidRe.MatchString(seg) || isoDateRe.MatchString(seg) || longDigitRe.MatchString(seg)
}

// leafWordCount counts alphabetic words in a leaf, splitting on hyphen,
// underscore, and whitespace. Pure-numeric tokens (e.g. a trailing date's
// 2026 / 06 / 10) are NOT counted, so an immutable-event leaf that ends in a
// date is not mistaken for free text.
func leafWordCount(leaf string) int {
	tokens := strings.FieldsFunc(leaf, func(r rune) bool {
		return r == '-' || r == '_' || unicode.IsSpace(r)
	})
	n := 0
	for _, tok := range tokens {
		if strings.ContainsFunc(tok, unicode.IsLetter) {
			n++
		}
	}
	return n
}
