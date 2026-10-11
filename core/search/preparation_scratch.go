package search

import "errors"

// PreparationBoundedDocument supplies allocation-free bounds before a lazy
// projection runs. Bytes bounds its source and projected UTF-8; fields bounds
// field instances, including empty ones. Implementations are trusted just as
// Document.String is; this does not constrain arbitrary user callback code.
type PreparationBoundedDocument interface {
	Document
	PreparationSizeHint() (bytes, fields int)
}

var ErrPreparationScratch = errors.New("search: preparation scratch budget exhausted or unbounded projection")

// PrepareWithScratchLimit is the opt-in bounded counterpart of Prepare. It
// reserves a conservative representation bound before projection or analysis,
// then uses the same analyzer and prepared-document implementation. Ordinary
// Prepare pays no accounting cost. The returned charge includes temporary and
// retained preparation storage; it is an accounting bound, not a Go RSS limit.
// Only the built-in production analyzer has a certified amplification bound.
func (idx *InvertedIndex[S, D]) PrepareWithScratchLimit(doc D, remaining uint64) (PreparedDocument, uint64, error) {
	if _, ok := idx.analyzer.(*scriptAwareAnalyzer); !ok {
		return PreparedDocument{}, 0, ErrPreparationScratch
	}
	bytes, fields, ok := preparationSizeHint(doc)
	if !ok || bytes < 0 || fields < 0 {
		return PreparedDocument{}, 0, ErrPreparationScratch
	}
	// Width/NFC/case folding and the production bigram channel are bounded
	// expansions. Reserve normalization/projection buffers, token and occurrence
	// arrays, terms, positions and growth slack together. Counting all sources
	// at their worst representation deliberately overcounts repeated terms.
	const fixed, perByte, perField = uint64(1024), uint64(2048), uint64(128)
	if remaining < fixed || uint64(bytes) > (remaining-fixed)/perByte {
		return PreparedDocument{}, 0, ErrPreparationScratch
	}
	charge := fixed + uint64(bytes)*perByte
	if uint64(fields) > (remaining-charge)/perField {
		return PreparedDocument{}, 0, ErrPreparationScratch
	}
	charge += uint64(fields) * perField
	prepared, _, err := idx.Prepare(doc)
	return prepared, charge, err
}

func preparationSizeHint(doc Document) (bytes, fields int, ok bool) {
	switch value := doc.(type) {
	case PreparationBoundedDocument:
		bytes, fields = value.PreparationSizeHint()
	case Text:
		bytes, fields = len(value), 1
	case Bytes:
		bytes, fields = len(value), 1
	case Fields:
		fields = len(value)
		for _, field := range value {
			if len(field.Text) > int(^uint(0)>>1)-bytes {
				return 0, 0, false
			}
			bytes += len(field.Text)
		}
	case Int, Uint, Float, Bool, Time, Duration:
		bytes, fields = 128, 1
	default:
		return 0, 0, false
	}
	return bytes, fields, true
}
