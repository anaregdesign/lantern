package service

import "google.golang.org/protobuf/reflect/protoreflect"

// cloneDataEnvelope owns every message, identity list and map that the boundary
// may rewrite. Scalar payload bytes remain immutable throughout ingress,
// storage and wire encoding; copying them here would duplicate a potentially
// multi-megabyte value just to add five bytes to its key. Unknown wire bytes are
// immutable too. No caller may mutate payloads while a request is in flight.
func cloneDataEnvelope(source protoreflect.Message) protoreflect.Message {
	target := source.New()
	target.SetUnknown(source.GetUnknown())
	source.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsMap():
			destination := target.Mutable(field).Map()
			value.Map().Range(func(key protoreflect.MapKey, entry protoreflect.Value) bool {
				if field.MapValue().Kind() == protoreflect.MessageKind {
					entry = protoreflect.ValueOfMessage(cloneDataEnvelope(entry.Message()))
				}
				destination.Set(key, entry)
				return true
			})
		case field.IsList():
			destination := target.Mutable(field).List()
			for i := range value.List().Len() {
				entry := value.List().Get(i)
				if field.Kind() == protoreflect.MessageKind {
					entry = protoreflect.ValueOfMessage(cloneDataEnvelope(entry.Message()))
				}
				destination.Append(entry)
			}
		case field.Kind() == protoreflect.MessageKind:
			target.Set(field, protoreflect.ValueOfMessage(cloneDataEnvelope(value.Message())))
		default:
			target.Set(field, value)
		}
		return true
	})
	return target
}
