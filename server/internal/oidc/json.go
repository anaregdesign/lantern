// Package oidc implements registered-Issuer authentication. It never assigns
// Roles from token claims or discovers an unregistered token Issuer.
package oidc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var ErrInvalidDocument = errors.New("invalid OIDC document")

// decodeJSON rejects ambiguous duplicate members, invalid UTF-8, excessive
// nesting and trailing JSON before handing a document to a typed decoder.
// Unknown extension members are allowed; OIDC metadata and JWTs are extensible.
func decodeJSON(raw []byte, target any) error {
	return decodeJSONWithBudget(raw, target, 10000)
}

func decodeJSONWithBudget(raw []byte, target any, budget int) error {
	if !utf8.Valid(raw) {
		return ErrInvalidDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := validateJSONValue(decoder, 0, &budget); err != nil {
		return ErrInvalidDocument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidDocument
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return ErrInvalidDocument
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, depth int, budget *int) error {
	*budget--
	if depth > 16 || *budget < 0 {
		return ErrInvalidDocument
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return ErrInvalidDocument
			}
			seen[name] = true
			if err := validateJSONValue(decoder, depth+1, budget); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder, depth+1, budget); err != nil {
				return err
			}
		}
	default:
		return ErrInvalidDocument
	}
	closeToken, err := decoder.Token()
	if err != nil || (delimiter == '{' && closeToken != json.Delim('}')) ||
		(delimiter == '[' && closeToken != json.Delim(']')) {
		return ErrInvalidDocument
	}
	return nil
}

// ValidateJSON validates bounded operator input without exposing decoded tokens.
func ValidateJSON(raw []byte) error { return decodeJSON(raw, new(any)) }

// ValidateOperatorJSON permits the larger numeric arrays in original authority
// provisioning without widening IdP, JWT or management-document token budgets.
// Its byte bound also limits the number of structural tokens and allocations.
func ValidateOperatorJSON(raw []byte) error {
	if len(raw) > 120<<10 {
		return ErrInvalidDocument
	}
	return decodeJSONWithBudget(raw, new(any), len(raw))
}
