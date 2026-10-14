package mcp

import (
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestCurrentUser(t *testing.T) {
	h := newContextHarness(t)
	for _, id := range []*string{new("11111111111111111111111111111111"), nil, new("22222222222222222222222222222222")} {
		h.fake.userID = id
		var out currentUserOutput
		structuredAs(t, h.call(t, "current_user", map[string]any{}), &out)
		if id == nil && out.UserID != nil || id != nil && (out.UserID == nil || *id != *out.UserID) {
			t.Fatal("missing/current account ID changed", out)
		}
	}
	h.fake.userIDErr = client.ErrUnauthenticated
	if result := h.call(t, "current_user", map[string]any{}); !result.IsError {
		t.Fatal("signed-out inspection returned prior user ID")
	}
	if h.fake.userIDCalls != 4 {
		t.Fatal("unexpected retry")
	}
}
