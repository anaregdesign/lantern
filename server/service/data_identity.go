package service

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func receiptLogicalKey(key string, format []string) (string, error) {
	if len(format) == 0 || (len(format) == 1 && format[0] == "") {
		return key, nil
	}
	if len(format) != 1 || format[0] != keyspace.Version {
		return "", keyspace.ErrInvalidKey
	}
	return keyspace.LogicalKey(key)
}

// validatePhysicalDataIdentities validates identities on the private peer,
// restore and WAL paths without re-encoding or copying graph values. Every
// key/tail/head field in the graph protocol is a physical graph identity.
func validatePhysicalDataIdentities(message protoreflect.Message) error {
	return keyspace.ValidatePhysicalGraphMessage(message)
}

func validateDataFormat(format string) error {
	return keyspace.ValidateFormat(format)
}

func validateReceiptDataIdentity(format string, keys ...string) error {
	if err := validateDataFormat(format); err != nil {
		return err
	}
	if format != "" {
		for _, key := range keys {
			if _, err := keyspace.LogicalKey(key); err != nil {
				return err
			}
		}
	}
	return nil
}
