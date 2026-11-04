package selfobs

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
)

// Redacted replaces secret values in logs.
const Redacted = "[REDACTED]"

// MinRedactedLength is the length below which a value is not redacted: a
// very short value would mangle every log line containing it.
const MinRedactedLength = 4

// Redactor replaces secret values (configured secrets, tokens) in logs. It
// is safe for concurrent use; values are added while the process runs.
type Redactor struct {
	mu       sync.RWMutex
	values   map[string]bool
	replacer *strings.Replacer
}

// NewRedactor returns an empty Redactor.
func NewRedactor() *Redactor { return &Redactor{values: map[string]bool{}} }

// Add registers a secret value. It reports false when the value is too
// short to be redacted reliably.
func (r *Redactor) Add(value string) bool {
	if len(value) < MinRedactedLength {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values[value] {
		return true
	}
	r.values[value] = true
	// Longest first, so that a value containing another one is replaced
	// whole.
	values := make([]string, 0, len(r.values))
	for v := range r.values {
		values = append(values, v)
	}
	slices.SortFunc(values, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	pairs := make([]string, 0, 2*len(values))
	for _, v := range values {
		pairs = append(pairs, v, Redacted)
	}
	r.replacer = strings.NewReplacer(pairs...)
	return true
}

// Redact replaces the registered values in s.
func (r *Redactor) Redact(s string) string {
	r.mu.RLock()
	rep := r.replacer
	r.mu.RUnlock()
	if rep == nil || s == "" {
		return s
	}
	return rep.Replace(s)
}

// RedactedHeader reports whether a header (or a log attribute named after
// one) carries credentials: Authorization, Cookie, Set-Cookie, X-*-Key and
// X-*-Token, case-insensitively.
func RedactedHeader(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	}
	return strings.HasPrefix(n, "x-") && (strings.HasSuffix(n, "-key") || strings.HasSuffix(n, "-token"))
}

// Handler wraps h: messages and attribute values are redacted before h sees
// them.
func (r *Redactor) Handler(h slog.Handler) slog.Handler { return &redactHandler{Handler: h, r: r} }

type redactHandler struct {
	slog.Handler
	r *Redactor
}

func (h *redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.r.attr(a))
		return true
	})
	return h.Handler.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = h.r.attr(a)
	}
	return &redactHandler{Handler: h.Handler.WithAttrs(redacted), r: h.r}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{Handler: h.Handler.WithGroup(name), r: h.r}
}

// attr redacts one attribute.
func (r *Redactor) attr(a slog.Attr) slog.Attr {
	if RedactedHeader(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, r.Redact(v.String()))
	case slog.KindGroup:
		group := v.Group()
		attrs := make([]slog.Attr, len(group))
		for i, ga := range group {
			attrs[i] = r.attr(ga)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(attrs...)}
	case slog.KindAny:
		switch x := v.Any().(type) {
		case http.Header:
			return slog.Any(a.Key, r.header(x))
		case error:
			return slog.String(a.Key, r.Redact(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, r.Redact(x.String()))
		default:
			// Formatted once to look for secrets; kept as is when clean, so
			// that JSON logs keep the structure of maps and slices.
			if s := fmt.Sprint(x); r.Redact(s) != s {
				return slog.String(a.Key, r.Redact(s))
			}
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// header returns a copy of h with the credential headers redacted, and the
// registered values redacted in the others.
func (r *Redactor) header(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for name, values := range h {
		if RedactedHeader(name) {
			out[name] = []string{Redacted}
			continue
		}
		for _, v := range values {
			out[name] = append(out[name], r.Redact(v))
		}
	}
	return out
}
