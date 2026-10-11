package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type boundedPreparationTestDocument struct {
	bytes, fields int
	called        *bool
}

func (d boundedPreparationTestDocument) PreparationSizeHint() (int, int) { return d.bytes, d.fields }
func (d boundedPreparationTestDocument) String() string                  { *d.called = true; return "prepared" }

func TestPreparationScratch(t *testing.T) {
	idx := NewInvertedIndex[string, Document](NewScriptAwareAnalyzer(), BM25{}, strings.Compare, WithPositions())
	for _, doc := range []Document{Text("alpha 東京 Straße"), Fields{{ID: FieldKey, Text: "item"}, {ID: FieldValue, Text: "alpha alpha"}}} {
		want, _, err := idx.Prepare(doc)
		if err != nil {
			t.Fatal(err)
		}
		got, charge, err := idx.PrepareWithScratchLimit(doc, 8<<20)
		if err != nil || charge == 0 || !reflect.DeepEqual(want, got) {
			t.Fatal(charge, err)
		}
		if _, _, err := idx.PrepareWithScratchLimit(doc, charge-1); !errors.Is(err, ErrPreparationScratch) {
			t.Fatal(err)
		}
		if _, exact, err := idx.PrepareWithScratchLimit(doc, charge); err != nil || exact != charge {
			t.Fatal(exact, err)
		}
	}
	for _, bounds := range [][2]int{{1 << 20, 1}, {1, 1 << 20}, {-1, 1}, {1, -1}, {int(^uint(0) >> 1), 1}} {
		called := false
		_, _, err := idx.PrepareWithScratchLimit(boundedPreparationTestDocument{bounds[0], bounds[1], &called}, 8<<20)
		if !errors.Is(err, ErrPreparationScratch) || called {
			t.Fatal("projection ran before reservation", bounds, called, err)
		}
	}
	called := false
	if _, _, err := idx.PrepareWithScratchLimit(hintedDocument{hint: 10, called: &called}, 8<<20); !errors.Is(err, ErrPreparationScratch) || called {
		t.Fatal("uncertified projection ran", err)
	}
}
