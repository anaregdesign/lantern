package benchheap

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
)

var ErrInput = errors.New("invalid heap observation input")
var hex32 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var hex20 = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s Session) Validate() error {
	if s.Schema != Schema || s.Protocol != Protocol || !hex32.MatchString(s.RunNonce) || !hex20.MatchString(s.SourceCommit) || !hex20.MatchString(s.SourceTree) || !hex32.MatchString(s.ContainerID) || !hex32.MatchString(s.BinarySHA256) || len(s.ImageID) != 71 || s.ImageID[:7] != "sha256:" || !hex32.MatchString(s.ImageID[7:]) || s.UID < 0 {
		return ErrInput
	}
	return nil
}

func (i Identity) Validate() error {
	if i.Session.Validate() != nil || i.PID <= 0 || !hex32.MatchString(i.StartNonce) {
		return ErrInput
	}
	return nil
}

func (r Request) Validate(identity Identity, ordinal int) error {
	phase := "baseline"
	if ordinal == 2 {
		phase = "post"
	}
	if ordinal < 1 || ordinal > 2 || r.Schema != Schema || r.Protocol != Protocol || r.Identity != identity || identity.Validate() != nil || r.Ordinal != ordinal || r.Phase != phase || !hex32.MatchString(r.CutNonce) || r.ExpectedReceipts <= 0 || r.ExpectedReceipts > 8000 {
		return ErrInput
	}
	return nil
}

// Decode requires every declared field, no unknown/duplicate keys or trailing
// JSON, bounded bytes, and native integer decoding (no float round trips).
func Decode(data []byte, out any, limit int) error {
	if len(data) == 0 || len(data) > limit {
		return ErrInput
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	tokens.UseNumber()
	depth := 0
	var scan func() error
	scan = func() error {
		depth++
		defer func() { depth-- }()
		if depth > 32 {
			return ErrInput
		}
		token, err := tokens.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for tokens.More() {
					key, err := tokens.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return ErrInput
					}
					seen[name] = true
					if err := scan(); err != nil {
						return err
					}
				}
				end, err := tokens.Token()
				if err != nil || end != json.Delim('}') {
					return ErrInput
				}
			case '[':
				for tokens.More() {
					if err := scan(); err != nil {
						return err
					}
				}
				end, err := tokens.Token()
				if err != nil || end != json.Delim(']') {
					return ErrInput
				}
			default:
				return ErrInput
			}
		}
		return nil
	}
	if scan() != nil {
		return ErrInput
	}
	if _, err := tokens.Token(); err != io.EOF {
		return ErrInput
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ErrInput
	}
	typ := reflect.TypeOf(out)
	if typ == nil || typ.Kind() != reflect.Pointer {
		return ErrInput
	}
	if requiredFields(data, typ.Elem()) != nil {
		return ErrInput
	}
	return nil
}

func requiredFields(raw []byte, typ reflect.Type) error {
	if typ.Kind() != reflect.Slice && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrInput
	}
	if typ.Kind() == reflect.Struct {
		var object map[string]json.RawMessage
		// Required canonical names plus this exact count reject aliases that
		// encoding/json otherwise matches case-insensitively to struct fields.
		if json.Unmarshal(raw, &object) != nil || object == nil || len(object) != typ.NumField() {
			return ErrInput
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := field.Tag.Get("json")
			if name == "" {
				name = field.Name
			}
			value, ok := object[name]
			if !ok {
				return ErrInput
			}
			if requiredFields(value, field.Type) != nil {
				return ErrInput
			}
		}
	} else if typ.Kind() == reflect.Slice {
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return ErrInput
		}
		for _, value := range values {
			if requiredFields(value, typ.Elem()) != nil {
				return ErrInput
			}
		}
	}
	return nil
}
