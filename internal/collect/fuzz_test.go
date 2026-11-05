package collect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/budget/budgettest"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/mapping"
	"github.com/kentrow/tapyr/internal/runtime"
)

// fuzzMaxPages bounds the runs of FuzzPagination: a server answering the
// same page forever must not paginate forever.
const fuzzMaxPages = 5

// FuzzPagination answers every request of every pagination strategy with
// the same fuzzed Link header, cursor header and body. Whatever they hold,
// a run ends within MaxPages requests and never panics; links to another
// origin are refused (TestRunRefusesCrossOriginLinks). "HOST" in the Link
// header stands for the test server.
func FuzzPagination(f *testing.F) {
	f.Add(`<http://HOST/devices?page=2>; rel="next"`, "c1", []byte(`{"next": "c1", "items": [1, 2], "total": 5, "more": true, "from": 1}`))
	f.Add(`</devices?page=3>; rel="prev next", <http://HOST/x>; rel=last`, "", []byte(`[{"id": 1}, {"id": 2}]`))
	f.Add(`<https://evil.example.invalid/steal>; rel="next"`, "", []byte(`{"next": "", "items": []}`))
	f.Add(`<http://HOST:99999/%zz>; rel=next`, "\x00", []byte(`{"next": 7, "items": "x", "total": "a", "more": null}`))
	f.Add("garbage;;rel", "next", []byte("\xff\xfe not json"))
	f.Add("", "", []byte(`null`))
	// One server and one executor for every input: the handler answers
	// with the input under test.
	var mu sync.Mutex
	var link, cursor string
	var body []byte
	requests := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if l := strings.ReplaceAll(link, "HOST", strings.TrimPrefix(srv.URL, "http://")); l != "" && validHeader(l) {
			w.Header().Set("Link", l)
		}
		if validHeader(cursor) {
			w.Header().Set("X-Next-Cursor", cursor)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	f.Cleanup(srv.Close)
	exec := newExecutor(budgettest.Passthrough{})
	f.Fuzz(func(t *testing.T, l, c string, b []byte) {
		mu.Lock()
		link, cursor, body = l, c, b
		mu.Unlock()
		for name, p := range fuzzPaginations(t) {
			mu.Lock()
			requests = 0
			mu.Unlock()
			job := newJob(t, srv.URL, &config.Extract{Items: "has(response.body.items) ? response.body.items : []"})
			job.Pagination = p
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			res, err := exec.Run(ctx, job, start, time.Time{})
			timedOut := ctx.Err() == context.DeadlineExceeded
			cancel()
			mu.Lock()
			n := requests
			mu.Unlock()
			if n > fuzzMaxPages {
				t.Errorf("%s: %d requests, MaxPages is %d", name, n, fuzzMaxPages)
			}
			if err == nil && len(res.Pages) > fuzzMaxPages {
				t.Errorf("%s: %d pages, MaxPages is %d", name, len(res.Pages), fuzzMaxPages)
			}
			if timedOut {
				t.Errorf("%s: the run did not end", name)
			}
		}
	})
}

// validHeader reports a value net/http accepts in a response header.
func validHeader(v string) bool {
	return !strings.ContainsAny(v, "\r\n\x00")
}

func fuzzPaginations(t *testing.T) map[string]runtime.Pagination {
	t.Helper()
	start := 1
	return map[string]runtime.Pagination{
		"offset": {
			Type: config.PaginationOffset, MaxPages: fuzzMaxPages,
			Offset: &config.OffsetPagination{OffsetParam: "offset", LimitParam: "limit", PageSize: 2},
			Total:  compile(t, mapping.ContextPage, "response.body.total", mapping.OutputNumber),
		},
		"page": {
			Type: config.PaginationPage, MaxPages: fuzzMaxPages,
			Page: &config.PagePagination{PageParam: "p", SizeParam: "size", PageSize: 2, Start: &start},
			Stop: compile(t, mapping.ContextPage, "!response.body.more", mapping.OutputBool),
		},
		"link_header": {
			Type: config.PaginationLinkHeader, MaxPages: fuzzMaxPages,
			LinkHeader: &config.LinkHeaderPagination{PageSizeParam: "perPage", PageSize: 2, Rel: "next"},
		},
		"cursor": {
			Type: config.PaginationCursor, MaxPages: fuzzMaxPages,
			Cursor: &config.CursorPagination{Cursor: "response.body.next", Param: "after"},
			Next:   compile(t, mapping.ContextPage, "response.body.next", mapping.OutputString),
		},
		"cursor_header": {
			Type: config.PaginationCursor, MaxPages: fuzzMaxPages,
			Cursor: &config.CursorPagination{Cursor: "response.headers['x-next-cursor'][0]", Header: "X-Cursor"},
			Next:   compile(t, mapping.ContextPage, "response.headers['x-next-cursor'][0]", mapping.OutputString),
		},
		"expression": {
			Type: config.PaginationExpression, MaxPages: fuzzMaxPages,
			Next: compile(t, mapping.ContextPage, "response.body.more ? {'query': {'from': string(response.body.from)}} : dyn(null)", mapping.OutputMap),
		},
	}
}
