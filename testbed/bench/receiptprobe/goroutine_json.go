package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

// Candidate evidence has optional companion references, unlike the fixed
// benchheap wire schema. Preserve canonical keys, presence and duplicate
// rejection without changing that schema or the historical readers.
func decodeGoroutineJSON(data []byte, value any) error {
	if len(data) == 0 || len(data) > 1<<20 {
		return errors.New("candidate JSON bound")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var unique func(int) error
	unique = func(depth int) error {
		if depth > 32 {
			return errors.New("candidate JSON depth")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return errors.New("candidate duplicate key")
				}
				keys[name] = true
				if err := unique(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := unique(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("candidate JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if unique(0) != nil {
		return errors.New("candidate JSON structure")
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("candidate trailing JSON")
	}
	typ := reflect.TypeOf(value)
	if typ == nil || typ.Kind() != reflect.Pointer {
		return errors.New("candidate JSON target")
	}
	if goroutineJSONFields(data, typ.Elem()) != nil {
		return errors.New("candidate canonical fields")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func goroutineJSONFields(data []byte, typ reflect.Type) error {
	if typ == reflect.TypeOf(json.RawMessage{}) {
		if !json.Valid(data) {
			return errors.New("candidate raw context")
		}
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		if typ.Kind() == reflect.Slice {
			return nil
		}
		return errors.New("candidate null")
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil || object == nil {
			return errors.New("candidate object")
		}
		allowed := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			parts := strings.Split(field.Tag.Get("json"), ",")
			name := parts[0]
			if name == "" {
				name = field.Name
			}
			if name == "-" {
				continue
			}
			allowed[name] = true
			optional := false
			for _, option := range parts[1:] {
				if option == "omitempty" {
					optional = true
				}
			}
			raw, ok := object[name]
			if !ok {
				if optional {
					continue
				}
				return errors.New("candidate required field")
			}
			if goroutineJSONFields(raw, field.Type) != nil {
				return errors.New("candidate nested field")
			}
		}
		for name := range object {
			if !allowed[name] {
				return errors.New("candidate unknown field")
			}
		}
	case reflect.Slice, reflect.Array:
		var rows []json.RawMessage
		if json.Unmarshal(data, &rows) != nil {
			return errors.New("candidate array")
		}
		for _, row := range rows {
			if goroutineJSONFields(row, typ.Elem()) != nil {
				return errors.New("candidate array element")
			}
		}
	case reflect.Map:
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil {
			return errors.New("candidate map")
		}
		for _, raw := range object {
			if goroutineJSONFields(raw, typ.Elem()) != nil {
				return errors.New("candidate map value")
			}
		}
	}
	return nil
}
