package authz

import (
	"strings"
	"testing"
)

func TestSubjectBindings(t *testing.T) {
	for _, uid := range []string{"", strings.Repeat("0", 32), strings.Repeat("A", 32), strings.Repeat("1", 31), strings.Repeat("1", 33), "g" + testUIDA[1:], " " + testUIDA[1:]} {
		if ValidPersonalUID(uid) {
			t.Fatalf("invalid UID accepted: %q", uid)
		}
		if _, err := PersonalPrefix(uid); err == nil {
			t.Fatalf("invalid UID produced a personal prefix: %q", uid)
		}
	}
	if !ValidPersonalUID("0123456789abcdef0123456789abcdef") {
		t.Fatal("canonical UID rejected")
	}
	if prefix, err := PersonalPrefix(testUIDA); err != nil || prefix != "users:"+testUIDA+":" {
		t.Fatal(prefix, err)
	}
	a, b := (SubjectBindings{testUIDA}).Digest(), (SubjectBindings{testUIDB}).Digest()
	if a == b || a == [32]byte{} || (SubjectBindings{}).Digest() != [32]byte{} {
		t.Fatal("subject binding collapsed")
	}
}

func TestPrincipalUIDPrefixByteSemantics(t *testing.T) {
	binding := SubjectBindings{testUIDA}
	for _, selector := range []PrincipalUIDPrefix{
		{LiteralBefore: "users:", LiteralAfter: ":"},
		{LiteralBefore: "public:users:", LiteralAfter: ":profile:"},
		{LiteralBefore: "日本語:{uid}:*:", LiteralAfter: ":e\u0301:"},
	} {
		if !selector.valid(1024) {
			t.Fatal("valid byte literal rejected")
		}
		prefix := selector.bind(binding)
		for _, key := range []string{prefix, prefix + "entry", strings.TrimSuffix(prefix, ":"), selector.LiteralBefore + testUIDB + selector.LiteralAfter, strings.ReplaceAll(prefix, "e\u0301", "é")} {
			if selector.matches(key, binding) != strings.HasPrefix(key, prefix) {
				t.Fatal("match/binding mismatch", key)
			}
		}
	}
}
