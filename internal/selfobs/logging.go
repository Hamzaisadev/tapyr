// Package selfobs provides Tapyr's self-observability: structured logging,
// and later self-metrics and health checks (see
// docs/spec/07-self-observability.md).
package selfobs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/kentrow/tapyr/internal/clock"
)

// LogFormat selects the log encoding.
type LogFormat string

// Supported log formats.
const (
	FormatJSON LogFormat = "json"
	FormatText LogFormat = "text"
)

// TimeKey is the name of the timestamp field in log records.
const TimeKey = "ts"

// LogOptions configures NewLogger.
type LogOptions struct {
	// Level is the minimum level logged. Pass a *slog.LevelVar to change it
	// at runtime. Defaults to info.
	Level slog.Leveler
	// Format defaults to JSON.
	Format LogFormat
	// Clock stamps the records. Defaults to the real clock.
	Clock clock.Clock
	// Redactor, when set, redacts secret values from every record.
	Redactor *Redactor
}

// ParseLogLevel parses debug, info, warn (or warning) and error,
// case-insensitively.
func ParseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid log level %q (want debug, info, warn or error)", s)
}

// ParseLogFormat parses json and text, case-insensitively.
func ParseLogFormat(s string) (LogFormat, error) {
	switch f := LogFormat(strings.ToLower(s)); f {
	case FormatJSON, FormatText:
		return f, nil
	}
	return "", fmt.Errorf("invalid log format %q (want json or text)", s)
}

// NewLogger returns a structured logger writing to w. Records carry their
// timestamp in the "ts" field, taken from the injected clock.
func NewLogger(w io.Writer, opts LogOptions) *slog.Logger {
	if opts.Level == nil {
		opts.Level = slog.LevelInfo
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real()
	}
	ho := &slog.HandlerOptions{Level: opts.Level, ReplaceAttr: renameTime}
	var h slog.Handler
	if opts.Format == FormatText {
		h = slog.NewTextHandler(w, ho)
	} else {
		h = slog.NewJSONHandler(w, ho)
	}
	if opts.Redactor != nil {
		h = opts.Redactor.Handler(h)
	}
	return slog.New(&clockHandler{Handler: h, clock: opts.Clock})
}

func renameTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		a.Key = TimeKey
	}
	return a
}

// clockHandler stamps records with the injected clock instead of the wall
// clock read by slog.Logger.
type clockHandler struct {
	slog.Handler
	clock clock.Clock
}

func (h *clockHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Time = h.clock.Now()
	return h.Handler.Handle(ctx, r)
}

func (h *clockHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &clockHandler{Handler: h.Handler.WithAttrs(attrs), clock: h.clock}
}

func (h *clockHandler) WithGroup(name string) slog.Handler {
	return &clockHandler{Handler: h.Handler.WithGroup(name), clock: h.clock}
}
