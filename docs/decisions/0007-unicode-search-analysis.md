# 0007: Preserve Unicode lexical identity and separate two-rune query analysis

Status: Accepted

## Context

The v1 production analyzer lowercased rather than applying full Unicode case
folding, erased every nonspacing mark, discarded every Unicode symbol, and
treated combining marks as token boundaries. Those choices collapsed Thai and
Indic distinctions, made emoji-only content unsearchable, and did not equate
sharp-s or Greek final sigma. Its shared document/query analyzer also omitted
an auxiliary gram for a two-rune word: documents containing `search` indexed
`ar`, but the query `ar` emitted only a primary word token.

UAX #29 word/grapheme segmentation was evaluated. Go's pinned `x/text` provides
case, normalization, and width transforms but no complete dictionary word
breaker for the Southeast Asian scripts in scope. Adding an unmeasured
language-specific segmenter would violate this Issue's non-goal and increase
binary/dependency cost. The implementation instead follows UAX #29's essential
extend behavior (marks continue a lexical run), retains whole Southeast Asian
runs plus bounded auxiliary grams, and derives Han membership from Unicode's
`Ideographic` property. CJK primary bigrams retain Lucene CJKBigramFilter's
documented Han/Hiragana/Katakana/Hangul set.

## Decision

Analyzer v2 applies, in order:

1. width folding;
2. canonical NFC without deleting marks;
3. Unicode language-neutral full case folding (`x/text/cases`);
4. removal of emoji text/graphic presentation selectors only;
5. punctuation boundaries that preserve Unicode symbols;
6. whitespace normalization and script-aware tokenization.

Combining marks remain in the preceding word. Unicode symbols are searchable
primary tokens; adjacent symbols are separate unless joined by U+200D, and
skin-tone modifiers remain significant. Presentation selectors are not lexical
identity, so `❤`, `❤︎`, and `❤️` are equivalent. The aggressive public
`DiacriticNormalizer` remains available as an explicit opt-in, but production
no longer uses it. Fuzzy search remains the explicit way to request recall
across distinct accented spellings.

`QueryAnalyzer` is an optional extension to `Analyzer`. Documents always use
`Analyze`; query paths use `AnalyzeQuery` when supported. Production query
analysis adds a `ClassGram` copy of each two-rune `ClassWord`. Thus `ar` finds
the gram carried by `search`, while a document whose whole word is `ar` also
matches the higher-weight primary channel and ranks first. Match modes already
count only distinct `ClassWord` terms, so auxiliary evidence never raises their
coverage threshold. Corpus statistics and retained document postings remain
unchanged.

When prefix or fuzzy expansion is requested, the duplicate auxiliary gram is
removed before clause construction: the primary word already owns that bounded
semantic expansion, and retaining an exact gram alongside it would bypass the
expansion cap. Phrase search continues to use primary terms only.

The discoverable analyzer version changes from `script-aware-v1` to
`script-aware-v2`. It already participates in `SearchCapabilities`' SHA-256
configuration fingerprint, so HA members with different analysis semantics are
observable as incompatible. Search currently has no pagination cursor; future
search cursors must bind this fingerprint.

## Measurement

The committed benchmarks compare v1/v2 analysis and a 20,000-document broad
query corpus. On an Apple M3 Max (`darwin/arm64`, three runs), they measured:

| Path | latency | query allocation | retained heap |
|---|---:|---:|---:|
| v1 analyzer | 3.03 µs/op | 2,664 B/op, 44 allocs | — |
| v2 analyzer | 3.05 µs/op | 3,512 B/op, 48 allocs | — |
| v1 `arch` top-10 | 7.34 ms/op | 2,544 B/op, 52 allocs | 795 B/document |
| v2 `arch` top-10 | 7.47 ms/op | 2,800 B/op, 53 allocs | 794 B/document |
| v2 `ar` top-10 | 3.12 ms/op | 2,152 B/op, 39 allocs | 794 B/document |

The common-script analyzer fixture isolates the changed transforms on scripts
whose tokenizer policy stayed fixed: CPU was within 1%, with four additional
allocations. The comparable broad-query path was within about 2%, adds one query allocation,
and retains no additional per-document heap. The newly enabled two-rune path
remains below the comparable four-rune broad query.

Reproduce with:

```shell
go test ./core/search -run '^$' -bench 'BenchmarkScriptAwareAnalyzerVersions|BenchmarkSearchTwoRuneInfix' -benchmem -count=5
```

The relevance gates include reviewed multilingual minimal-pair qrels for case
folding, composition, accents, Thai/Indic marks, emoji, CJK, digits, and mixed
scripts. Existing English/Japanese/mixed floors and Lucene comparisons remain
blocking.

## Consequences

- Canonically equivalent and full-case-fold-equivalent text matches reliably.
- Meaningful marks and ZWJ emoji intent no longer disappear at analysis time.
- Accentless matching is no longer silently global; callers can opt into fuzzy
  expansion when that recall tradeoff is desired.
- A two-rune query performs one additional bounded dictionary/posting lookup on
  the auxiliary channel. Document token counts and retained postings do not grow.
- The tokenizer is not claimed to be an optimal morphological analyzer for any
  language; its documented bounded units remain intentionally language-neutral.
