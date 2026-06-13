package search

import "testing"

func TestLowercaseNormalizer(t *testing.T) {
	if got := (LowercaseNormalizer{}).Normalize("HeLLo Wörld"); got != "hello wörld" {
		t.Fatalf("Normalize = %q, want %q", got, "hello wörld")
	}
}

func TestSpaceNormalizer(t *testing.T) {
	if got := (SpaceNormalizer{}).Normalize("  a\t\nb   c  "); got != "a b c" {
		t.Fatalf("Normalize = %q, want %q", got, "a b c")
	}
}

func TestNormalizersChain(t *testing.T) {
	n := Normalizers(LowercaseNormalizer{}, SpaceNormalizer{})
	if got := n.Normalize("  A\tB  "); got != "a b" {
		t.Fatalf("Normalize = %q, want %q", got, "a b")
	}
	// An empty chain is the identity transform.
	if got := Normalizers().Normalize("Keep As-Is"); got != "Keep As-Is" {
		t.Fatalf("empty chain = %q, want unchanged", got)
	}
}

func TestNormalizerFunc(t *testing.T) {
	var n Normalizer = NormalizerFunc(func(s string) string { return s + "!" })
	if got := n.Normalize("x"); got != "x!" {
		t.Fatalf("Normalize = %q, want %q", got, "x!")
	}
}
