package security

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidDisplayEmail accepts a bounded mailbox, never a display-name/address
// list. It validates optional display metadata only; email is not an identity,
// account-linking key, enrollment proof or source of permissions.
func ValidDisplayEmail(value string) bool {
	if value == "" || len(value) > 254 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "<>\"(),;\\") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Name == "" && address.Address == value
}
