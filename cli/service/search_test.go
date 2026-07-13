package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
	client "github.com/anaregdesign/lantern/sdks/go"
)

type fakeSearchClient struct {
	pages []client.SearchPage
	err   error
	calls int
	after func()
}

func (fake *fakeSearchClient) SearchVerticesPage(context.Context, string, ...client.SearchOption) (client.SearchPage, error) {
	fake.calls++
	if fake.after != nil {
		fake.after()
	}
	if fake.err != nil {
		return client.SearchPage{}, fake.err
	}
	if len(fake.pages) == 0 {
		return client.SearchPage{}, errors.New("unexpected SearchVerticesPage call")
	}
	page := fake.pages[0]
	fake.pages = fake.pages[1:]
	return page, nil
}

func TestRunSearchJSONIsLosslessAndBounded(t *testing.T) {
	projected, err := client.UnmarshalVertexJSON([]byte(`{"key":"doc/1","type":"string","value":"alpha"}`))
	if err != nil {
		t.Fatal(err)
	}
	key := "tab\tnewline\nquote\"slash\\"
	fake := &fakeSearchClient{pages: []client.SearchPage{{
		Hits: []client.SearchHit{{
			Key: key, Score: 3.5, Vertex: projected,
			ProjectionStatus: client.SearchHitProjectionSnapshot,
		}},
		NextCursor: []byte{1, 2, 3}, EffectiveLimit: 25, Truncated: true,
	}}}
	var out bytes.Buffer
	if err := runSearch(context.Background(), fake, parser.Search{Query: "alpha", Mode: "server", Projection: "full-vertex"}, &out); err != nil {
		t.Fatal(err)
	}
	var page searchPageOutput
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(page.Hits) != 1 || page.Hits[0].Key != key || page.NextCursor != "AQID" || !page.Truncated {
		t.Fatalf("page = %#v", page)
	}
	var vertex map[string]any
	if err := json.Unmarshal(page.Hits[0].Vertex, &vertex); err != nil {
		t.Fatal(err)
	}
	if vertex["value"] != "alpha" {
		t.Fatalf("vertex = %#v", vertex)
	}
}

func TestRunSearchNDJSONStreamsEveryPage(t *testing.T) {
	fake := &fakeSearchClient{pages: []client.SearchPage{
		{Hits: []client.SearchHit{{Key: "a", Score: 2, ProjectionStatus: client.SearchHitProjectionKeyScore}}, NextCursor: []byte{1}},
		{Hits: []client.SearchHit{{Key: "b", Score: 1, ProjectionStatus: client.SearchHitProjectionKeyScore}}},
	}}
	var out bytes.Buffer
	if err := runSearch(context.Background(), fake, parser.Search{Query: "x", All: true}, &out); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&out)
	var keys []string
	for {
		var hit searchHitOutput
		if err := decoder.Decode(&hit); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, hit.Key)
	}
	if strings.Join(keys, ",") != "a,b" || fake.calls != 2 {
		t.Fatalf("keys=%v calls=%d", keys, fake.calls)
	}
}

func TestRunSearchTSVUsesRealEscaping(t *testing.T) {
	key := "tab\tnewline\nquote\"slash\\"
	fake := &fakeSearchClient{pages: []client.SearchPage{{Hits: []client.SearchHit{{
		Key: key, Score: 1.25, ProjectionStatus: client.SearchHitProjectionKeyScore,
	}}}}}
	var out bytes.Buffer
	if err := runSearch(context.Background(), fake, parser.Search{Query: "x", Format: "tsv"}, &out); err != nil {
		t.Fatal(err)
	}
	reader := csv.NewReader(&out)
	reader.Comma = '\t'
	record, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(record) != 4 || record[0] != key || record[1] != "1.25" || record[2] != "key-score" {
		t.Fatalf("record = %#v", record)
	}
}

func TestRunSearchCancellationDoesNotWritePartialJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeSearchClient{
		pages: []client.SearchPage{{Hits: []client.SearchHit{{Key: "too-late"}}}},
		after: cancel,
	}
	var out bytes.Buffer
	err := runSearch(ctx, fake, parser.Search{Query: "x"}, &out)
	if !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatalf("err=%v output=%q", err, out.String())
	}
}

func TestSearchOptionsValidateBeforeRPC(t *testing.T) {
	fake := &fakeSearchClient{}
	for _, command := range []parser.Search{
		{Query: "x", Mode: "any", MinShould: 1},
		{Query: "x", Phrase: true, Fuzziness: 1},
		{Query: "x", Fuzziness: 3},
		{Query: "x", All: true, Format: "json"},
		{Query: "x", Cursor: "not+base64"},
	} {
		var out bytes.Buffer
		if err := runSearch(context.Background(), fake, command, &out); err == nil {
			t.Fatalf("runSearch(%#v) accepted invalid command", command)
		}
	}
	if fake.calls != 0 {
		t.Fatalf("invalid commands made %d RPCs", fake.calls)
	}
}

func TestActionableSearchError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.Join(client.ErrFailedPrecondition, client.ErrSearchPositionsDisabled), "LANTERN_SEARCH_POSITIONS=true"},
		{errors.Join(client.ErrFailedPrecondition, client.ErrSearchDisabled), "LANTERN_SEARCH_ENABLED=true"},
		{client.ErrSearchIndexIncomplete, "bounded rebuild"},
		{client.ErrSearchCursorStale, "restart explicitly"},
		{client.ErrSearchCursorInvalid, "option set"},
		{client.ErrSearchContinuationLimited, "bounded session cap"},
	} {
		if got := actionableSearchError(tc.err); !strings.Contains(got.Error(), tc.want) || !errors.Is(got, tc.err) {
			t.Errorf("actionableSearchError(%v) = %v, want %q and wrapped sentinel", tc.err, got, tc.want)
		}
	}
}
