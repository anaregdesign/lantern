package parser

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Focused tokeniser coverage for #437 + #438. The shared fixture under
// admin/test/cli-grammar/verbs.json exercises the full validator
// pipeline; this file pins the raw token stream so a future refactor
// that subtly changes (say) case-handling or escape semantics turns
// red here before propagating.

func tokens(t *testing.T, in string) []string {
	t.Helper()
	s, err := NewSource(in)
	if err != nil {
		t.Fatalf("NewSource(%q) failed: %v", in, err)
	}
	return s.Slice()
}

func TestNewSource_Empty(t *testing.T) {
	if got := tokens(t, ""); len(got) != 0 {
		t.Errorf("empty input -> %#v, want []", got)
	}
	if got := tokens(t, "   \t\n  "); len(got) != 0 {
		t.Errorf("whitespace-only -> %#v, want []", got)
	}
}

func TestNewSource_CasePreservation(t *testing.T) {
	got := tokens(t, "Get VERTEX CamelKey")
	want := []string{"Get", "VERTEX", "CamelKey"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestNewSource_BarewordBoundary(t *testing.T) {
	// Quotes embedded inside a bareword stay verbatim — they are only
	// special at the *start* of a token.
	got := tokens(t, `key=foo"bar a=b`)
	want := []string{`key=foo"bar`, `a=b`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestNewSource_DoubleQuoted(t *testing.T) {
	cases := map[string][]string{
		`put vertex k "hello world"`: {"put", "vertex", "k", "hello world"},
		`"" trailing`:                {"", "trailing"},
		`"say \"hi\""`:               {`say "hi"`},
		`"a\\b"`:                     {`a\b`},
		`"a\nb\tc\rd"`:               {"a\nb\tc\rd"},
		`"foo" "bar baz"`:            {"foo", "bar baz"},
	}
	for in, want := range cases {
		got := tokens(t, in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tokens(%q) = %#v, want %#v", in, got, want)
		}
	}
}

func TestNewSource_SingleQuoted(t *testing.T) {
	cases := map[string][]string{
		`put vertex code 'console.log("hi")'`: {"put", "vertex", "code", `console.log("hi")`},
		`'C:\Users\hiroki'`:                   {`C:\Users\hiroki`},
		`a '' b`:                              {"a", "", "b"},
	}
	for in, want := range cases {
		got := tokens(t, in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tokens(%q) = %#v, want %#v", in, got, want)
		}
	}
}

func TestNewSource_UnterminatedQuote(t *testing.T) {
	cases := []string{
		`"hello world`,
		`'hello world`,
		`"foo\`,
	}
	for _, in := range cases {
		_, err := NewSource(in)
		if !errors.Is(err, ErrUnterminatedString) {
			t.Errorf("NewSource(%q) err = %v, want ErrUnterminatedString", in, err)
		}
	}
}

func TestNewSource_InvalidEscape(t *testing.T) {
	in := `"bad \q escape"`
	_, err := NewSource(in)
	if err == nil || !strings.Contains(err.Error(), "invalid escape sequence") {
		t.Errorf("NewSource(%q) err = %v, want invalid escape error", in, err)
	}
}
