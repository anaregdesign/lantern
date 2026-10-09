package security

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Options must be the final composition of a current listener's Connect
// options. All codecs (including Connect's JSON UTF-8 alias) enforce the same
// encoded, pre-compression bound. The outer interceptor also bounds errors and
// trailers, which Connect encodes outside the message codec.
func (p *CurrentOutput) Options(options ...connect.HandlerOption) []connect.HandlerOption {
	result := []connect.HandlerOption{connect.WithInterceptors(currentOutputInterceptor{p})}
	result = append(result, options...)
	for _, name := range []string{"proto", "json", "json; charset=utf-8"} {
		result = append(result, connect.WithCodec(currentOutputCodec{name: name, read: p.limits.ReadBytes, send: p.limits.SendBytes}))
	}
	return result
}

// MarshalBrowserJSON uses the same pre-encoding bound and requires the actual
// request's reserved credit; HTTP session endpoints do not use Connect codecs.
func (p *CurrentOutput) MarshalBrowserJSON(ctx context.Context, message proto.Message) ([]byte, error) {
	if p == nil {
		return nil, ErrAuthorityUnavailable
	}
	if _, err := currentRequest(ctx, p.owner); err != nil {
		return nil, err
	}
	return (currentOutputCodec{name: "json", read: p.limits.ReadBytes, send: p.limits.SendBytes}).Marshal(message)
}

type currentOutputCodec struct {
	name       string
	read, send int
}

func (c currentOutputCodec) Name() string   { return c.name }
func (c currentOutputCodec) IsBinary() bool { return c.name == "proto" }
func (c currentOutputCodec) Marshal(value any) ([]byte, error) {
	return c.marshal(value, false)
}
func (c currentOutputCodec) MarshalStable(value any) ([]byte, error) {
	return c.marshal(value, true)
}
func (c currentOutputCodec) marshal(value any, stable bool) ([]byte, error) {
	m, ok := value.(proto.Message)
	if !ok || m == nil {
		return nil, errCurrentOutput
	}
	if c.IsBinary() {
		if proto.Size(m) > c.send {
			return nil, currentEncodingLimit()
		}
		return (proto.MarshalOptions{Deterministic: stable}).Marshal(m)
	}
	budget := currentJSONBudget{remaining: c.send}
	if !budget.message(m.ProtoReflect(), 0) {
		return nil, currentEncodingLimit()
	}
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(raw) > c.send { // Guard the sizing contract against codec drift.
		return nil, currentEncodingLimit()
	}
	if stable {
		out := bytes.NewBuffer(raw[:0])
		if err = json.Compact(out, raw); err != nil {
			return nil, err
		}
		raw = out.Bytes()
	}
	return raw, nil
}
func (c currentOutputCodec) Unmarshal(raw []byte, value any) error {
	m, ok := value.(proto.Message)
	if !ok || m == nil {
		return errCurrentOutput
	}
	if len(raw) > c.read {
		return currentEncodingLimit()
	}
	if c.IsBinary() {
		return (proto.UnmarshalOptions{RecursionLimit: 100}).Unmarshal(raw, m)
	}
	return (protojson.UnmarshalOptions{DiscardUnknown: false, RecursionLimit: 100}).Unmarshal(raw, m)
}

func currentEncodingLimit() error {
	return connect.NewError(connect.CodeResourceExhausted, errors.New("encoded message exceeds current output limit"))
}

// This traversal allocates no output-sized buffer. It bounds the actual JSON
// representation, including escaped strings, base64, numeric strings, map keys,
// oneof defaults and the encoder's optional delimiter whitespace. It stops as
// soon as the finite budget is exhausted. No ratio of binary size is assumed.
type currentJSONBudget struct{ remaining int }

func (b *currentJSONBudget) take(n int) bool {
	if n < 0 || n > b.remaining {
		b.remaining = -1
		return false
	}
	b.remaining -= n
	return true
}
func (b *currentJSONBudget) quoted(s string) bool {
	if !b.take(2) {
		return false
	}
	for i := 0; i < len(s); i++ {
		n := 1
		if s[i] < 0x20 {
			n = 6
		} else if s[i] == '"' || s[i] == '\\' {
			n = 2
		}
		if !b.take(n) {
			return false
		}
	}
	return true
}
func (b *currentJSONBudget) message(m protoreflect.Message, depth int) bool {
	if depth > 100 || !b.take(2) {
		return false
	}
	name := m.Descriptor().FullName()
	switch name {
	case "google.protobuf.Timestamp":
		return b.take(40)
	case "google.protobuf.Duration":
		return b.take(32)
	case "google.protobuf.Empty":
		return true
	}
	// Public methods currently use Timestamp/Duration only. An added WKT must
	// supply its real JSON shape before it can enter this listener's encoder.
	if strings.HasPrefix(string(name), "google.protobuf.") && (name == "google.protobuf.Any" || name == "google.protobuf.Value" || name == "google.protobuf.Struct" || name == "google.protobuf.ListValue" || name == "google.protobuf.FieldMask" || strings.HasSuffix(string(name), "Value")) {
		return false
	}
	ok := true
	m.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		ok = b.quoted(field.JSONName()) && b.take(4)
		if !ok {
			return false
		}
		switch {
		case field.IsList():
			ok = b.take(2)
			list := value.List()
			for i := 0; ok && i < list.Len(); i++ {
				ok = b.take(2) && b.scalar(field, list.Get(i), depth+1)
			}
		case field.IsMap():
			ok = b.take(2)
			if ok {
				value.Map().Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
					var k string
					switch field.MapKey().Kind() {
					case protoreflect.StringKind:
						k = key.String()
					case protoreflect.BoolKind:
						k = strconv.FormatBool(key.Bool())
					case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
						k = strconv.FormatUint(key.Uint(), 10)
					default:
						k = strconv.FormatInt(key.Int(), 10)
					}
					ok = b.quoted(k) && b.take(4) && b.scalar(field.MapValue(), value, depth+1)
					return ok
				})
			}
		default:
			ok = b.scalar(field, value, depth+1)
		}
		return ok
	})
	return ok
}
func (b *currentJSONBudget) scalar(f protoreflect.FieldDescriptor, v protoreflect.Value, depth int) bool {
	switch f.Kind() {
	case protoreflect.StringKind:
		return b.quoted(v.String())
	case protoreflect.BytesKind:
		n := len(v.Bytes())
		return n <= b.remaining && b.take(2+4*((n+2)/3))
	case protoreflect.BoolKind:
		return b.take(5)
	case protoreflect.EnumKind:
		if enum := f.Enum().Values().ByNumber(v.Enum()); enum != nil {
			return b.quoted(string(enum.Name()))
		}
		return b.take(11)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return b.take(len(strconv.FormatInt(v.Int(), 10)))
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return b.take(len(strconv.FormatUint(v.Uint(), 10)))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return b.take(2 + len(strconv.FormatInt(v.Int(), 10)))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return b.take(2 + len(strconv.FormatUint(v.Uint(), 10)))
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return b.take(32)
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return b.message(v.Message(), depth)
	default:
		return false
	}
}
