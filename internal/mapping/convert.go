package mapping

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// DecodeJSON decodes a JSON document for CEL: integral numbers that fit in
// an int64 become int64 (so identifiers keep their precision), other
// numbers float64.
func DecodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decoding JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decoding JSON: unexpected data after the document")
	}
	return normalize(v), nil
}

func normalize(v any) any {
	switch v := v.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		f, _ := v.Float64()
		return f
	case map[string]any:
		for k, e := range v {
			v[k] = normalize(e)
		}
	case []any:
		for i, e := range v {
			v[i] = normalize(e)
		}
	}
	return v
}

// adapt converts a native Go value to a CEL value.
func adapt(v any) ref.Val { return types.DefaultTypeAdapter.NativeToValue(v) }

// typeName describes a CEL value in error messages.
func typeName(v ref.Val) string { return v.Type().TypeName() }

// ToFloat converts a metric value: numbers, and bools (true is 1).
func ToFloat(v ref.Val) (float64, error) {
	switch v := v.(type) {
	case types.Double:
		return float64(v), nil
	case types.Int:
		return float64(v), nil
	case types.Uint:
		return float64(v), nil
	case types.Bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case types.Null:
		return 0, errors.New("value is null")
	}
	return 0, fmt.Errorf("value must be a number or a bool, got %s", typeName(v))
}

// ToLabel converts a label value: strings, and scalars converted to their
// string form. Null gives an empty (dropped) label.
func ToLabel(v ref.Val) (string, error) {
	switch v := v.(type) {
	case types.String:
		return string(v), nil
	case types.Int:
		return strconv.FormatInt(int64(v), 10), nil
	case types.Uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case types.Double:
		return strconv.FormatFloat(float64(v), 'f', -1, 64), nil
	case types.Bool:
		return strconv.FormatBool(bool(v)), nil
	case types.Null:
		return "", nil
	}
	return "", fmt.Errorf("label value must be a string, got %s", typeName(v))
}

// windowString converts a windowField value: a scalar in its label form, a
// timestamp in RFC 3339; null is an error (the window is unknown).
func windowString(v ref.Val) (string, error) {
	switch v := v.(type) {
	case types.Timestamp:
		return v.Time.UTC().Format(time.RFC3339Nano), nil
	case types.Null:
		return "", errors.New("window is null")
	}
	s, err := ToLabel(v)
	if err != nil {
		return "", fmt.Errorf("window must be a string, a number, a bool or a timestamp, got %s", typeName(v))
	}
	return s, nil
}

// ToBool converts a condition.
func ToBool(v ref.Val) (bool, error) {
	if b, ok := v.(types.Bool); ok {
		return bool(b), nil
	}
	return false, fmt.Errorf("condition must be a bool, got %s", typeName(v))
}

// ToTimestamp converts a timestamp; ok is false for null.
func ToTimestamp(v ref.Val) (t time.Time, ok bool, err error) {
	switch v := v.(type) {
	case types.Timestamp:
		return v.Time, true, nil
	case types.Null:
		return time.Time{}, false, nil
	}
	return time.Time{}, false, fmt.Errorf("timestamp must be a timestamp, got %s", typeName(v))
}

// ToList returns the elements of a list.
func ToList(v ref.Val) ([]ref.Val, error) {
	l, ok := v.(traits.Lister)
	if !ok {
		return nil, fmt.Errorf("items must be a list, got %s", typeName(v))
	}
	n, ok := l.Size().(types.Int)
	if !ok {
		return nil, errors.New("items: invalid list size")
	}
	out := make([]ref.Val, 0, int(n))
	for it := l.Iterator(); it.HasNext() == types.True; {
		out = append(out, it.Next())
	}
	return out, nil
}
