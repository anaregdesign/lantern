package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// parseValue converts a raw string from argv into a Go value suitable for
// Lantern.PutVertex. The valueType controls disambiguation:
//
//	auto      try int, then float, then bool, then RFC3339, else string
//	string    keep as raw string
//	int       strconv.Atoi (int64)
//	float     strconv.ParseFloat (float64)
//	bool      strconv.ParseBool
//	datetime  time.Parse(time.RFC3339, ...)
//	json      json.Unmarshal — supports objects, arrays, scalars. Objects
//	          and arrays are re-encoded as a compact JSON string before
//	          being sent (the Lantern wire format has no nested value
//	          variant); scalars pass through as their natural Go type.
//
// auto is the default. It is unambiguous for most string keys/values, but if
// you need to insist on a string that *looks* like a number, pass
// --value-type string.
func parseValue(raw, valueType string) (any, error) {
	switch valueType {
	case "", "auto":
		if v, err := strconv.Atoi(raw); err == nil {
			return v, nil
		}
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v, nil
		}
		if v, err := strconv.ParseBool(raw); err == nil {
			return v, nil
		}
		if v, err := time.Parse(time.RFC3339, raw); err == nil {
			return v, nil
		}
		return raw, nil
	case "string":
		return raw, nil
	case "int":
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("--value-type=int: %w", err)
		}
		return v, nil
	case "float":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("--value-type=float: %w", err)
		}
		return v, nil
	case "bool":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("--value-type=bool: %w", err)
		}
		return v, nil
	case "datetime":
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("--value-type=datetime (RFC3339): %w", err)
		}
		return v, nil
	case "duration":
		v, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("--value-type=duration: %w", err)
		}
		return v, nil
	case "json":
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("--value-type=json: %w", err)
		}
		// Lantern's wire format has no nested value variant. Re-encode
		// objects and arrays as a compact JSON string so they round-trip
		// cleanly through PutVertex/GetVertex. Scalars (bool, float64,
		// string, nil) are forwarded as-is.
		switch v.(type) {
		case map[string]any, []any:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("--value-type=json re-encode: %w", err)
			}
			return string(b), nil
		default:
			return v, nil
		}
	default:
		return nil, fmt.Errorf("unknown --value-type %q (want auto|string|int|float|bool|datetime|duration|json)", valueType)
	}
}
