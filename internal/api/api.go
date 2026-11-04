// Package api is the read-only status API under /api/v1 (see "Status API"
// in docs/spec/09-cli-api-ui.md). It serves what a Source describes and
// never changes anything.
package api

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// OpenAPI is the OpenAPI 3.1 description of the API.
//
//go:embed openapi.yaml
var OpenAPI []byte

// Pagination bounds.
const (
	DefaultLimit = 100
	MaxLimit     = 1000
)

// ErrNotFound is returned by a Source for an unknown name.
var ErrNotFound = errors.New("not found")

// ErrDebugDisabled is returned by Source.Debug when the live debug
// endpoint is disabled (settings.server.debug.enabled).
var ErrDebugDisabled = errors.New("the live debug endpoint is disabled (settings.server.debug.enabled)")

// Source is the read-only view of the running collector the API serves.
type Source interface {
	Status(ctx context.Context) Status
	Providers(ctx context.Context) []Provider
	Accounts(ctx context.Context) []Account
	Jobs(ctx context.Context) []Job
	// Job returns one job with its definition.
	Job(ctx context.Context, name string) (Job, error)
	Instances(ctx context.Context, job string) ([]Instance, error)
	// Runs returns the most recent runs of a job (of one instance when
	// instance is set), newest first.
	Runs(ctx context.Context, job, instance string, limit int) ([]Run, error)
	Budgets(ctx context.Context) []Budget
	Timeline(ctx context.Context, limit, scope string) (Timeline, error)
	// Plan returns the plan applied, ErrNotFound before the first.
	Plan(ctx context.Context) (Plan, error)
	Graph(ctx context.Context) Graph
	Outputs(ctx context.Context) []Output
	// Debug returns the capture of the last run of a job instance (the
	// most recent instance of the job when instance is empty).
	Debug(ctx context.Context, job, instance string) (Debug, error)
}

// routes are the routes of the API, as registered (the OpenAPI test
// checks that they are all documented).
var routes = []string{
	"GET /api/v1/status",
	"GET /api/v1/providers",
	"GET /api/v1/accounts",
	"GET /api/v1/accounts/{name}",
	"GET /api/v1/jobs",
	"GET /api/v1/jobs/{name}",
	"GET /api/v1/jobs/{name}/instances",
	"GET /api/v1/jobs/{name}/runs",
	"GET /api/v1/budgets",
	"GET /api/v1/budgets/{limit}/timeline",
	"GET /api/v1/plan",
	"GET /api/v1/graph",
	"GET /api/v1/outputs",
	"GET /api/v1/openapi.yaml",
	"GET /api/v1/debug/jobs/{name}/last",
	"GET /api/v1/events",
}

// Handler returns the API handler, to mount on the server mux for
// /api/v1/. events serves the event stream.
func Handler(src Source, events *Broker) http.Handler {
	mux := http.NewServeMux()
	handle := map[string]http.HandlerFunc{
		"GET /api/v1/status": func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, src.Status(r.Context())) },
		"GET /api/v1/providers": func(w http.ResponseWriter, r *http.Request) {
			list(w, r, src.Providers(r.Context()))
		},
		"GET /api/v1/accounts": func(w http.ResponseWriter, r *http.Request) {
			list(w, r, src.Accounts(r.Context()))
		},
		"GET /api/v1/accounts/{name}": func(w http.ResponseWriter, r *http.Request) {
			for _, a := range src.Accounts(r.Context()) {
				if a.Name == r.PathValue("name") {
					writeJSON(w, http.StatusOK, a)
					return
				}
			}
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("account %q not found", r.PathValue("name")))
		},
		"GET /api/v1/jobs": func(w http.ResponseWriter, r *http.Request) { list(w, r, src.Jobs(r.Context())) },
		"GET /api/v1/jobs/{name}": func(w http.ResponseWriter, r *http.Request) {
			one[Job](w, "job", r.PathValue("name"))(src.Job(r.Context(), r.PathValue("name")))
		},
		"GET /api/v1/jobs/{name}/instances": func(w http.ResponseWriter, r *http.Request) {
			instances, err := src.Instances(r.Context(), r.PathValue("name"))
			if err != nil {
				notFound(w, err, "job", r.PathValue("name"))
				return
			}
			list(w, r, instances)
		},
		"GET /api/v1/jobs/{name}/runs": func(w http.ResponseWriter, r *http.Request) {
			limit, ok := limitParam(w, r)
			if !ok {
				return
			}
			runs, err := src.Runs(r.Context(), r.PathValue("name"), r.URL.Query().Get("instance"), limit)
			if err != nil {
				notFound(w, err, "job", r.PathValue("name"))
				return
			}
			writeJSON(w, http.StatusOK, List[Run]{Items: nonNil(runs)})
		},
		"GET /api/v1/budgets": func(w http.ResponseWriter, r *http.Request) { list(w, r, src.Budgets(r.Context())) },
		"GET /api/v1/budgets/{limit}/timeline": func(w http.ResponseWriter, r *http.Request) {
			scope := r.URL.Query().Get("scope")
			one[Timeline](w, "quota scope", r.PathValue("limit")+" "+scope)(src.Timeline(r.Context(), r.PathValue("limit"), scope))
		},
		"GET /api/v1/plan": func(w http.ResponseWriter, r *http.Request) {
			plan, err := src.Plan(r.Context())
			if err != nil {
				writeError(w, http.StatusNotFound, "not_found", "no plan applied yet")
				return
			}
			writeJSON(w, http.StatusOK, plan)
		},
		"GET /api/v1/graph":   func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, src.Graph(r.Context())) },
		"GET /api/v1/outputs": func(w http.ResponseWriter, r *http.Request) { list(w, r, src.Outputs(r.Context())) },
		"GET /api/v1/openapi.yaml": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(OpenAPI)
		},
		"GET /api/v1/debug/jobs/{name}/last": func(w http.ResponseWriter, r *http.Request) {
			d, err := src.Debug(r.Context(), r.PathValue("name"), r.URL.Query().Get("instance"))
			if errors.Is(err, ErrDebugDisabled) {
				writeError(w, http.StatusNotFound, "debug_disabled", err.Error())
				return
			}
			one[Debug](w, "capture of job", r.PathValue("name"))(d, err)
		},
		"GET /api/v1/events": events.serve,
	}
	for _, route := range routes {
		mux.HandleFunc(route, handle[route])
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no endpoint %s %s", r.Method, r.URL.Path))
	})
	return mux
}

// one writes a single item or the matching error.
func one[T any](w http.ResponseWriter, kind, name string) func(T, error) {
	return func(v T, err error) {
		if err != nil {
			notFound(w, err, kind, name)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func notFound(w http.ResponseWriter, err error, kind, name string) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("%s %q not found", kind, name))
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", err.Error())
}

// list writes a page of items: ?limit= (default 100, at most 1000) and the
// opaque ?cursor= of the previous page.
func list[T any](w http.ResponseWriter, r *http.Request, items []T) {
	limit, ok := limitParam(w, r)
	if !ok {
		return
	}
	offset := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		n, perr := strconv.Atoi(string(raw))
		if err != nil || perr != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "invalid cursor")
			return
		}
		offset = n
	}
	page := List[T]{Items: []T{}}
	if offset < len(items) {
		end := min(len(items), offset+limit)
		page.Items = items[offset:end]
		if end < len(items) {
			page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func limitParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return DefaultLimit, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > MaxLimit {
		writeError(w, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", MaxLimit))
		return 0, false
	}
	return n, true
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}
