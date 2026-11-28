package search

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/tarp"
)

func TestDiskAdmissionUsesExactFinalLiveCapacity(t *testing.T) {
	ctx := t.Context()
	source := diskTestSource()
	now := source.Watermark.Add(time.Second)
	base := []DiskDocument{
		{Key: "a", Document: Fields{{FieldKey, "red"}, {FieldValue, "red red"}}},
		{Key: "b", Document: Text("blue")},
		{Key: "d", Document: Text("ephemeral"), Expiration: now},
	}
	current := []DiskDocument{
		{Key: "b", Document: Text("green")},
		{Key: "c", Document: Text("red")},
	}
	analyzer := NewAnalyzer(nil, WhitespaceTokenizer{}, nil)
	limits := SearchAnalysisLimits{MaxDocumentBytes: 40, MaxLiveTerms: 3, MaxLivePostings: 4, MaxPositionEntries: 5}
	opts := []IndexOption{WithPositions(), WithAnalysisLimits(limits)}
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(base), analyzer, opts...); err != nil {
		t.Fatal(err)
	}
	idx, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if idx.footer.Capacity != (diskCapacity{Terms: 3, Postings: 4, Positions: 5}) {
		t.Fatalf("known base has capacity %+v", idx.footer.Capacity)
	}
	commits := db.Stats().WriteCommits
	for _, test := range []struct {
		name     string
		proposed []DiskDocument
		kind     AnalysisLimitKind
		got      int64
	}{
		{name: "at live limit after unflushed expiry"},
		{name: "terms", proposed: []DiskDocument{{Key: "x", Document: Text("teal pink")}}, kind: LimitLiveTerms, got: 4},
		{name: "posting", proposed: []DiskDocument{{Key: "x", Document: Text("red")}}, kind: LimitLivePostings, got: 5},
		{name: "position", proposed: []DiskDocument{{Key: "a", Document: Fields{{FieldKey, "red red red"}, {FieldValue, "red"}}}}, kind: LimitPositionEntries, got: 6},
		{name: "document", proposed: []DiskDocument{{Key: "x", Document: Text(strings.Repeat("red ", 20))}}, kind: LimitDocumentBytes, got: 80},
		{name: "born expired", proposed: []DiskDocument{{Key: "a", Document: Text("out of capacity"), Expiration: now}}},
		{name: "current replaced", proposed: []DiskDocument{{Key: "b", Document: Text("red")}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := idx.ValidateAdmission(ctx, now, diskTestDocuments(current), diskTestDocuments(test.proposed), Budget{})
			if test.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var limit *AnalysisLimitError
			if !errors.As(err, &limit) || limit.Kind != test.kind || limit.Got != test.got {
				t.Fatalf("expected %s at %d, got %v", test.kind, test.got, err)
			}
		})
	}
	proposed := []DiskDocument{
		{Key: "a", Document: Text("amber amber amber")},
		{Key: "b"}, // overrides an accepted postimage
		{Key: "e", Document: Text("red")},
	}
	if err := idx.ValidateAdmission(ctx, now, diskTestDocuments(current), diskTestDocuments(proposed), Budget{}); err != nil {
		t.Fatal(err)
	}
	if db.Stats().WriteCommits != commits {
		t.Fatal("admission wrote derived disk rows")
	}
	advancedSource := source
	advancedSource.Sequence++
	advancedSource.Digest[0] = 1
	advancedSource.Watermark = now
	advanced, err := idx.AdvanceDiskBase(ctx, advancedSource, mergeDiskPostimages(diskTestDocuments(current), diskTestDocuments(proposed)), Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if advanced.footer.Capacity != (diskCapacity{Terms: 2, Postings: 3, Positions: 5}) {
		t.Fatalf("advanced capacity = %+v", advanced.footer.Capacity)
	}
	reopened, err := OpenDiskIndex(ctx, db, 1, advancedSource, analyzer, nil, opts...)
	if err != nil || reopened.footer.Capacity != advanced.footer.Capacity {
		t.Fatalf("reopened capacity = %v, %v", reopened, err)
	}
}

func TestDiskAdmissionRejectsMalformedOverriddenInput(t *testing.T) {
	ctx := t.Context()
	source := diskTestSource()
	analyzer := NewAnalyzer(nil, WhitespaceTokenizer{}, nil)
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(nil), analyzer); err != nil {
		t.Fatal(err)
	}
	idx, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil)
	if err != nil {
		t.Fatal(err)
	}
	current := []DiskDocument{{Key: "a"}, {Key: "a"}}
	proposed := []DiskDocument{{Key: "a", Document: Text("valid")}}
	if err := idx.ValidateAdmission(ctx, source.Watermark, diskTestDocuments(current), diskTestDocuments(proposed), Budget{}); !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("duplicate overridden change = %v", err)
	}
}

func TestPinnedDiskAdmissionKeepsItsCutAcrossDerivedAdvance(t *testing.T) {
	ctx := t.Context()
	source := diskTestSource()
	analyzer := NewAnalyzer(nil, WhitespaceTokenizer{}, nil)
	opts := []IndexOption{WithAnalysisLimits(SearchAnalysisLimits{MaxLiveTerms: 1})}
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	before := []DiskDocument{{Key: "a", Document: Text("alpha")}}
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(before), analyzer, opts...); err != nil {
		t.Fatal(err)
	}
	idx, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil, opts...)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := idx.PinAdmission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admission.Close() }()

	next := source
	next.Sequence++
	next.Digest[0] = 1
	replacement := []DiskDocument{{Key: "a", Document: Text("beta")}}
	if _, err := idx.AdvanceDiskBase(ctx, next, diskTestDocuments(replacement), Budget{}); err != nil {
		t.Fatal(err)
	}
	proposed := []DiskDocument{{Key: "b", Document: Text("alpha")}}
	if err := admission.Validate(ctx, source.Watermark, nil, diskTestDocuments(proposed), Budget{}); err != nil {
		t.Fatalf("pinned admission lost the original term capacity: %v", err)
	}
	if err := idx.ValidateAdmission(ctx, source.Watermark, nil, diskTestDocuments(proposed), Budget{}); !errors.Is(err, ErrIndexIncomplete) {
		t.Fatalf("superseded handle recertified without a pin: %v", err)
	}
}
