package search

import (
	"errors"
	"iter"
	"testing"

	"github.com/anaregdesign/tarp"
)

func TestDiskBuildSealsOnlyCompleteFreshBases(t *testing.T) {
	ctx := t.Context()
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := diskTestSource()
	analyzer := NewScriptAwareAnalyzer()
	readErr := errors.New("source stream failed")
	broken := iter.Seq2[DiskDocument, error](func(yield func(DiskDocument, error) bool) {
		if yield(DiskDocument{Key: "a\x00b", Document: Text("indexed")}, nil) {
			yield(DiskDocument{}, readErr)
		}
	})
	if err := BuildDiskBase(ctx, db, 1, source, broken, analyzer); !errors.Is(err, readErr) {
		t.Fatalf("failed input = %v", err)
	}
	if _, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil); !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("unsealed input = %v", err)
	}
	docs := []DiskDocument{{Key: "a\x00b", Document: Text("indexed")}}
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(docs), analyzer); !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("reused ID = %v", err)
	}
	var analysisLimit *AnalysisLimitError
	if err := BuildDiskBase(ctx, db, 2, source, diskTestDocuments(docs), analyzer, WithAnalysisLimits(SearchAnalysisLimits{MaxDocumentBytes: 1})); !errors.As(err, &analysisLimit) {
		t.Fatalf("over-limit document = %v", err)
	}
	if err := BuildDiskBase(ctx, db, 3, source, diskTestDocuments(docs), analyzer); err != nil {
		t.Fatal(err)
	}
	wrong := source
	wrong.Sequence++
	if _, err := OpenDiskIndex(ctx, db, 3, wrong, analyzer, nil); !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("incorrect source cut = %v", err)
	}
	if _, err := OpenDiskIndex(ctx, db, 3, source, analyzer, nil, WithPositions()); !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("incorrect index configuration = %v", err)
	}
}
