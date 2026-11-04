// Package secret holds resolved secret values. A Value never reveals its
// content through formatting, JSON, text marshaling or slog: only Reveal
// returns it, at the point of use (an HTTP header, for example).
package secret

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/kentrow/tapyr/internal/config"
)

// Redacted replaces secret values in every output.
const Redacted = "[REDACTED]"

// Value is a resolved secret. The content is captured by a closure: when a
// Value is an unexported field of another struct, fmt cannot call its
// methods and prints the fields by reflection, which shows a function
// address and never the secret (a string or a pointer would be printed).
type Value struct{ get func() string }

// New wraps a secret value.
func New(s string) Value { return Value{get: func() string { return s }} }

// Reveal returns the secret. Call it only where the value is sent.
func (v Value) Reveal() string {
	if v.get == nil {
		return ""
	}
	return v.get()
}

// IsZero reports whether the secret is empty.
func (v Value) IsZero() bool { return v.Reveal() == "" }

// String implements fmt.Stringer.
func (Value) String() string { return Redacted }

// GoString implements fmt.GoStringer.
func (Value) GoString() string { return Redacted }

// Format implements fmt.Formatter, covering every verb.
func (Value) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, Redacted) }

// MarshalJSON implements json.Marshaler.
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText implements encoding.TextMarshaler.
func (Value) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// LogValue implements slog.LogValuer.
func (Value) LogValue() slog.Value { return slog.StringValue(Redacted) }

// Resolve reads a secret reference. File secrets are trimmed of trailing
// newlines; empty secrets are errors.
func Resolve(ref config.SecretRef, src config.SecretSource) (Value, error) {
	var raw string
	switch {
	case ref.Env != "":
		v, ok := src.LookupEnv(ref.Env)
		if !ok {
			return Value{}, fmt.Errorf("environment variable %s is not set", ref.Env)
		}
		raw = v
	case ref.File != "":
		b, err := src.ReadFile(ref.File)
		if err != nil {
			return Value{}, fmt.Errorf("reading secret file %s: %w", ref.File, unwrap(err))
		}
		raw = strings.TrimRight(string(b), "\r\n")
	default:
		return Value{}, errors.New("empty secret reference")
	}
	if strings.TrimSpace(raw) == "" {
		return Value{}, errors.New("secret is empty")
	}
	if src.OnResolve != nil {
		src.OnResolve(raw)
	}
	return New(raw), nil
}

func unwrap(err error) error {
	if u := errors.Unwrap(err); u != nil {
		return u
	}
	return err
}
