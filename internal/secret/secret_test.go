package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"testing"

	"github.com/kentrow/tapyr/internal/config"
)

const value = "hunter2-very-secret"

func TestValueNeverLeaks(t *testing.T) {
	v := New(value)
	type holder struct {
		Token Value
		Ptr   *Value
		inner Value // unexported: fmt prints it by reflection
	}
	h := holder{Token: v, Ptr: &v, inner: v}
	var outputs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T"} {
		outputs = append(outputs, fmt.Sprintf(verb, v), fmt.Sprintf(verb, h))
	}
	outputs = append(outputs, v.String(), fmt.Sprint(v), fmt.Sprintln(&v))
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(b))
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("auth", "token", v, "holder", h)
	slog.New(slog.NewTextHandler(&logs, nil)).Info("auth", "token", v)
	outputs = append(outputs, logs.String())

	for _, out := range outputs {
		if strings.Contains(out, value) || strings.Contains(out, fmt.Sprintf("%x", value)) {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	if v.Reveal() != value || v.IsZero() || !New("").IsZero() {
		t.Fatal("Reveal or IsZero returned unexpected values")
	}
}

func TestResolve(t *testing.T) {
	src := config.SecretSource{
		LookupEnv: func(name string) (string, bool) {
			switch name {
			case "SET":
				return value, true
			case "BLANK":
				return "  ", true
			}
			return "", false
		},
		ReadFile: func(path string) ([]byte, error) {
			if path == "/run/secrets/key" {
				return []byte(value + "\n"), nil
			}
			return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		},
	}
	tests := []struct {
		ref     config.SecretRef
		wantErr string
	}{
		{ref: config.SecretRef{Env: "SET"}},
		{ref: config.SecretRef{File: "/run/secrets/key"}},
		{ref: config.SecretRef{Env: "UNSET"}, wantErr: "environment variable UNSET is not set"},
		{ref: config.SecretRef{Env: "BLANK"}, wantErr: "secret is empty"},
		{ref: config.SecretRef{File: "/nope"}, wantErr: "reading secret file /nope: file does not exist"},
		{ref: config.SecretRef{}, wantErr: "empty secret reference"},
	}
	for _, tt := range tests {
		got, err := Resolve(tt.ref, src)
		if tt.wantErr != "" {
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("Resolve(%+v) error = %v, want %q", tt.ref, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got.Reveal() != value {
			t.Errorf("Resolve(%+v) = %q, %v", tt.ref, got.Reveal(), err)
		}
		if err != nil && strings.Contains(err.Error(), value) {
			t.Error("secret value in error")
		}
	}
	if _, err := Resolve(config.SecretRef{File: "/nope"}, src); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file error does not wrap fs.ErrNotExist: %v", err)
	}
	// OnResolve receives the resolved values, for log redaction.
	var resolved []string
	src.OnResolve = func(v string) { resolved = append(resolved, v) }
	for _, ref := range []config.SecretRef{{Env: "SET"}, {Env: "BLANK"}, {File: "/run/secrets/key"}} {
		_, _ = Resolve(ref, src)
	}
	if len(resolved) != 2 || resolved[0] != value || resolved[1] != value {
		t.Errorf("OnResolve received %d values", len(resolved))
	}
}
