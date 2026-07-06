package parser

import "testing"

// FuzzValidate fuzzes the CLI/REPL grammar dispatcher end-to-end: NewSource
// tokenises arbitrary input, then Validate dispatches it. Neither may panic
// on any byte sequence — a syntactically invalid command must surface as an
// error, never a crash. Seeds are drawn from the shared grammar corpus
// (admin/test/cli-grammar/verbs.json) plus adversarial quoting/escaping so
// the fuzzer starts from realistic, structurally diverse commands.
func FuzzValidate(f *testing.F) {
	seeds := []string{
		// Valid forms (mirrors the "valid" arm of the shared fixture).
		"exit", "help", "help me",
		"get vertex alice", "get edge alice bob",
		"put vertex alice Alice", "put vertex alice Alice 60",
		"put vertex price 1234 type=int", "put vertex name 007 type=string",
		"put edge alice bob 1.5", "put edge alice bob 1.5 60",
		"add edge alice bob 1.0",
		"add decaying-edge alice bob 16 0.5 5 1",
		"delete vertex alice", "delete vertex alice bob carol",
		"delete edge alice bob", "delete edge alice bob carol dave",
		"scan vertices users/", "scan vertices users/ 100 all=true",
		"scan edges alice head=post:", "count vertices users/",
		"delete-prefix vertices tmp/ confirm=yes",
		"delete-prefix vertices tmp/ dry_run=true limit=500",
		"keys users/", "keys users/ 100",
		"illuminate alice 2 5",
		"illuminate alice 2 5 reduction=spt objective=max weighting=tfidf",
		"illuminate alice 2 5 weighting=bm25",
		"illuminate alice 2 5 prefix=team:",
		"illuminate alice 2 5 algorithm=ppr",
		"illuminate alice 2 5 algorithm=ppr restart_prob=0.25 epsilon=0.001",
		"illuminate alice 2 5 algorithm=community reduction=mst",
		`put vertex greeting "hello world"`,
		`put vertex code 'console.log("hi")'`,
		`put vertex path "a\nb\tc"`,
		// Invalid / adversarial forms (mirrors the "invalid" arm + quoting).
		"", "nonsense", "get", "put vertex k v type=bogus",
		"put edge alice bob notafloat", "scan vertex users/",
		"illuminate alice 2 5 algorithm=bogus",
		`put vertex greeting "hello world`,
		`put vertex greeting "bad \q escape"`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Must not panic or hang. Both a nil (valid) and non-nil (rejected)
		// result are acceptable outcomes; only a crash is a failure.
		_ = Validate(input)
		// Exercise the tokeniser directly too — it is the front end Validate
		// relies on, and REPL/RPC callers may invoke it standalone.
		if s, err := NewSource(input); err == nil {
			_, _ = Verb(s)
		}
	})
}

// TestValidate_AddDecayingEdge covers the #952 decay verb through the
// dispatcher: the full six-arg form validates, while short/trailing forms and
// `add vertex` (which the add verb never accepted) are rejected. The shared
// grammar fixture deliberately omits this verb (Go-only until the TS parser
// gains parity), so its Validate coverage lives here.
func TestValidate_AddDecayingEdge(t *testing.T) {
	valid := []string{
		"add decaying-edge alice bob 16 0.5 5 1",
		"add decaying-edge a b -8 0.9 16 3",
	}
	for _, in := range valid {
		if err := Validate(in); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", in, err)
		}
	}
	invalid := []string{
		"add decaying-edge alice bob",          // missing numerics
		"add decaying-edge alice bob 16 0.5 5", // missing interval
		"add decaying-edge a b 16 0.5 5 1 x",   // trailing token
		"add vertex alice",                     // add never took vertex
	}
	for _, in := range invalid {
		if err := Validate(in); err == nil {
			t.Errorf("Validate(%q) = nil, want error", in)
		}
	}
}
