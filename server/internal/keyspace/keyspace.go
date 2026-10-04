// Package keyspace defines the storage domains owned by the Server. Public
// callers always supply logical data keys; internal callers use physical keys.
package keyspace

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	Version    = "namespaced-v1"
	DataPrefix = "data:"
	SysPrefix  = "sys:"
)

var ErrInvalidKey = errors.New("invalid storage key")

// DataKey encodes a public identity exactly once. An apparent namespace in a
// logical key is ordinary user data; it is never treated as an escape.
func DataKey(logical string) (string, error) {
	if logical == "" || !utf8.ValidString(logical) {
		return "", ErrInvalidKey
	}
	return DataPrefix + logical, nil
}

// DataKeyPrefix encodes a literal public prefix. The empty prefix selects the
// complete data domain, not the system domain.
func DataKeyPrefix(logical string) (string, error) {
	if !utf8.ValidString(logical) {
		return "", ErrInvalidKey
	}
	return DataPrefix + logical, nil
}

// LogicalKey decodes only an internal data identity. Callers must never use
// this function to decide whether a public key needs encoding.
func LogicalKey(physical string) (string, error) {
	logical, ok := strings.CutPrefix(physical, DataPrefix)
	if !ok || logical == "" || !utf8.ValidString(logical) {
		return "", ErrInvalidKey
	}
	return logical, nil
}

// ValidateDataEdge rejects generic graph edges outside the data domain. System
// records have typed transaction semantics and cannot join the business graph.
func ValidateDataEdge(tail, head string) error {
	if _, err := LogicalKey(tail); err != nil {
		return err
	}
	_, err := LogicalKey(head)
	return err
}
