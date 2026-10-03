package keyspace

import (
	"errors"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var ErrNamespaceFormat = errors.New("incompatible physical namespace format")

// ValidatePhysicalGraphMessage checks graph identities without rewriting them.
// Only internal graph-protocol messages may cross this boundary. Origin maps,
// values and receipt-result bytes are not graph identities.
func ValidatePhysicalGraphMessage(message protoreflect.Message) error {
	fields := message.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if field.IsMap() {
			continue
		}
		value := message.Get(field)
		if field.Kind() == protoreflect.StringKind {
			switch field.Name() {
			case "key", "tail", "head", "keys":
				if field.IsList() {
					for j := range value.List().Len() {
						if _, err := LogicalKey(value.List().Get(j).String()); err != nil {
							return err
						}
					}
				} else {
					if _, err := LogicalKey(value.String()); err != nil {
						return err
					}
				}
			}
		} else if field.Kind() == protoreflect.MessageKind {
			if field.IsList() {
				for j := range value.List().Len() {
					if err := ValidatePhysicalGraphMessage(value.List().Get(j).Message()); err != nil {
						return err
					}
				}
			} else if message.Has(field) {
				if err := ValidatePhysicalGraphMessage(value.Message()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ValidateFormat accepts only explicitly classified data representations.
// The empty format is reserved for lower-level logical-key component fixtures.
func ValidateFormat(format string) error {
	if format != "" && format != Version {
		return ErrNamespaceFormat
	}
	return nil
}
