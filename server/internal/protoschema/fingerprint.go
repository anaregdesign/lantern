// Package protoschema fingerprints the wire-relevant shape of a protobuf
// message and every message reachable from its fields. Versioned private
// persistent formats pin this value so a newly generated field cannot enter
// an old format without an explicit review and migration decision.
package protoschema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Fingerprint is independent of generated Go source layout and protobuf
// marshal field order. Revisited descriptors are recorded as references, so
// a future recursive message cannot cause unbounded traversal.
func Fingerprint(descriptor protoreflect.MessageDescriptor) string {
	if descriptor == nil {
		return ""
	}
	// sha256 implements hash.Hash, whose Write contract never returns an error.
	// These formatting writes therefore have no fallible external writer.
	h := sha256.New()
	visited := make(map[protoreflect.FullName]struct{})
	var visit func(protoreflect.MessageDescriptor)
	visit = func(message protoreflect.MessageDescriptor) {
		if _, ok := visited[message.FullName()]; ok {
			_, _ = fmt.Fprintf(h, "reference %s\n", message.FullName())
			return
		}
		visited[message.FullName()] = struct{}{}
		_, _ = fmt.Fprintf(h, "message %s %d %t\n", message.FullName(), message.ParentFile().Syntax(), message.IsMapEntry())
		fields := message.Fields()
		numbers := make([]int, 0, fields.Len())
		for i := 0; i < fields.Len(); i++ {
			numbers = append(numbers, int(fields.Get(i).Number()))
		}
		sort.Ints(numbers)
		for _, number := range numbers {
			field := fields.ByNumber(protoreflect.FieldNumber(number))
			oneof := protoreflect.FullName("")
			if field.ContainingOneof() != nil {
				oneof = field.ContainingOneof().FullName()
			}
			_, _ = fmt.Fprintf(h, "field %d %s %d %d %t %t %t %t %t %s\n",
				field.Number(), field.Name(), field.Kind(), field.Cardinality(),
				field.IsList(), field.IsMap(), field.IsPacked(), field.HasPresence(),
				field.IsExtension(), oneof)
			if field.Kind() == protoreflect.EnumKind {
				enum := field.Enum()
				_, _ = fmt.Fprintf(h, "enum %s\n", enum.FullName())
				for i := 0; i < enum.Values().Len(); i++ {
					value := enum.Values().Get(i)
					_, _ = fmt.Fprintf(h, "value %s %d\n", value.Name(), value.Number())
				}
			}
			if field.Kind() == protoreflect.MessageKind {
				visit(field.Message())
			}
		}
	}
	visit(descriptor)
	return hex.EncodeToString(h.Sum(nil))
}
