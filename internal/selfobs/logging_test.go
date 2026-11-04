package selfobs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/clock"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "debug", want: slog.LevelDebug},
		{in: "INFO", want: slog.LevelInfo},
		{in: "warn", want: slog.LevelWarn},
		{in: "warning", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "", wantErr: true},
		{in: "verbose", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseLogLevel(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseLogLevel(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseLogFormat(t *testing.T) {
	for in, want := range map[string]LogFormat{"json": FormatJSON, "TEXT": FormatText} {
		if got, err := ParseLogFormat(in); err != nil || got != want {
			t.Errorf("ParseLogFormat(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "logfmt"} {
		if _, err := ParseLogFormat(in); err == nil {
			t.Errorf("ParseLogFormat(%q) succeeded, want error", in)
		}
	}
}

func TestNewLoggerJSON(t *testing.T) {
	now := time.Date(2026, time.November, 2, 9, 30, 0, 123456789, time.UTC)
	var buf bytes.Buffer
	logger := NewLogger(&buf, LogOptions{Level: slog.LevelInfo, Format: FormatJSON, Clock: clock.NewFake(now)})

	logger.With("component", "scheduler").Info("job started", "provider", "example-sim", "job", "devices")
	logger.Debug("filtered out")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1:\n%s", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("invalid JSON %q: %v", lines[0], err)
	}
	want := map[string]any{
		"ts":        "2026-11-02T09:30:00.123456789Z",
		"level":     "INFO",
		"msg":       "job started",
		"component": "scheduler",
		"provider":  "example-sim",
		"job":       "devices",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("field %q = %v, want %v", k, rec[k], v)
		}
	}
	if _, ok := rec["time"]; ok {
		t.Error(`record has a "time" field, want "ts" only`)
	}
}

func TestNewLoggerText(t *testing.T) {
	now := time.Date(2026, time.November, 2, 9, 30, 0, 0, time.UTC)
	var buf bytes.Buffer
	logger := NewLogger(&buf, LogOptions{Level: slog.LevelDebug, Format: FormatText, Clock: clock.NewFake(now)})
	logger.Debug("hello", "k", "v")
	if got, want := buf.String(), "ts=2026-11-02T09:30:00.000Z level=DEBUG msg=hello k=v\n"; got != want {
		t.Fatalf("text output = %q, want %q", got, want)
	}
}

func TestNewLoggerKeepsNestedTimeKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LogOptions{Level: slog.LevelInfo, Format: FormatJSON, Clock: clock.NewFake(time.Unix(0, 0).UTC())})
	logger.WithGroup("window").Info("reset", "time", "00:00")
	if !strings.Contains(buf.String(), `"window":{"time":"00:00"}`) {
		t.Fatalf("nested time attribute was renamed: %s", buf.String())
	}
}

func TestNewLoggerDefaultsAndLevelVar(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelWarn)
	logger := NewLogger(&buf, LogOptions{Level: level})
	logger.Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("info logged at warn level: %s", buf.String())
	}
	level.Set(slog.LevelInfo)
	logger.Info("shown")
	if !strings.Contains(buf.String(), `"msg":"shown"`) {
		t.Fatalf("level change not honored, or default format is not JSON: %s", buf.String())
	}
	if !logger.Handler().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("handler not enabled at info")
	}
}
