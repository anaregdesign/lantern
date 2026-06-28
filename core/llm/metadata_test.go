package llm

import "testing"

// TestFinishReasonValues pins the wire-stable string values, since backends map
// their provider's vocabulary onto these constants.
func TestFinishReasonValues(t *testing.T) {
	cases := map[FinishReason]string{
		FinishStop:          "stop",
		FinishLength:        "length",
		FinishContentFilter: "content_filter",
		FinishOther:         "other",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("FinishReason = %q, want %q", string(got), want)
		}
	}
}
