package security

import (
	"strings"
	"testing"
)

func TestValidDisplayEmail(t *testing.T) {
	for _, value := range []string{"user@example.test", "User+tag@example.test", "name.surname@sub.example.test"} {
		if !ValidDisplayEmail(value) {
			t.Fatalf("valid optional mailbox rejected: %q", value)
		}
	}
	for _, value := range []string{"", "user", "user@", "User <user@example.test>", "<script>@example.test", "user@example.test\n", " user@example.test", "a@example.test,b@example.test", "\"user\"@example.test", strings.Repeat("a", 255) + "@example.test"} {
		if ValidDisplayEmail(value) {
			t.Fatalf("malformed or unsafe mailbox accepted: %q", value)
		}
	}
}
