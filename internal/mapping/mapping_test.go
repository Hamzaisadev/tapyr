package mapping

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/common/types"
	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/model"
)

var now = time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)

func newEnv(t *testing.T) *Env {
	t.Helper()
	e, err := NewEnv(1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// eval compiles and evaluates src in the item context with the given item.
func eval(t *testing.T, e *Env, src string, item any) (any, error) {
	t.Helper()
	p, err := e.Compile(ContextItem, src, OutputAny)
	if err != nil {
		return nil, err
	}
	v, err := p.Eval(&Activation{Item: item, Now: now, Response: map[string]any{}})
	if err != nil {
		return nil, err
	}
	return v.Value(), nil
}

func TestFunctions(t *testing.T) {
	e := newEnv(t)
	item := map[string]any{"name": "Core-1", "count": int64(3), "nothing": nil, "size": "1.5 GB", "num": "42.5"}
	tests := []struct {
		src     string
		want    any
		wantErr string
	}{
		{src: "default(item.missing, 'x')", want: "x"},
		{src: "default(item.nothing, 'x')", want: "x"},
		{src: "default(item.name, 'x')", want: "Core-1"},
		{src: "default(item.?missing, 7)", want: int64(7)},
		{src: "default(item.?count, 7)", want: int64(3)},
		{src: "toFloat(item.count)", want: 3.0},
		{src: "toFloat(item.num)", want: 42.5},
		{src: "toFloat(' 7 ')", want: 7.0},
		{src: "toFloat(item.name)", wantErr: `toFloat: "Core-1" is not a number`},
		{src: "toFloat(true)", wantErr: "cannot convert bool"},
		{src: "parseTimestamp('2026-11-02T08:00:00+01:00')", want: time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)},
		{src: "parseTimestamp('02/11/2026', '02/01/2006')", want: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)},
		{src: "parseTimestamp('yesterday')", wantErr: "invalid timestamp"},
		{src: "fromUnix(1700000000)", want: time.Unix(1700000000, 0).UTC()},
		{src: "fromUnix(1700000000.5)", want: time.Unix(1700000000, 5e8).UTC()},
		{src: "fromUnixMillis(1700000000123)", want: time.UnixMilli(1700000000123).UTC()},
		{src: "fromUnix('x')", wantErr: "expected an epoch number"},
		{src: "parseDuration('3d 4h')", want: 3*86400.0 + 4*3600},
		{src: "parseDuration('1h30m')", want: 5400.0},
		{src: "parseDuration('01:02:03')", want: 3723.0},
		{src: "parseDuration('02:03')", want: 123.0},
		{src: "parseDuration('90')", want: 90.0},
		{src: "parseDuration('2 days')", wantErr: "invalid duration"},
		{src: "parseBytes(item.size)", want: 1.5e9},
		{src: "parseBytes('512 KiB')", want: 524288.0},
		{src: "parseBytes('2048')", want: 2048.0},
		{src: "parseBytes('1 parsec')", wantErr: "unknown unit"},
		{src: "macToString('AA-BB-CC-DD-EE-FF')", want: "aa:bb:cc:dd:ee:ff"},
		{src: "macToString('aabb.ccdd.eeff')", want: "aa:bb:cc:dd:ee:ff"},
		{src: "macToString('AABBCCDDEEFF')", want: "aa:bb:cc:dd:ee:ff"},
		{src: "macToString('aa:bb:cc')", wantErr: "invalid MAC address"},
		{src: "item.name.lower()", want: "core-1"},
		{src: "item.name.upper()", want: "CORE-1"},
		{src: "'  x '.trim()", want: "x"},
		{src: "'a,b'.split(',').size()", want: int64(2)},
		{src: "[[1, 2], [3]].flatten().size()", want: int64(3)},
		{src: "now", want: now},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			got, err := eval(t, e, tt.src, item)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gt, ok := got.(time.Time); ok {
				if !gt.Equal(tt.want.(time.Time)) {
					t.Fatalf("got %v, want %v", gt, tt.want)
				}
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		ctx     Context
		src     string
		out     Output
		wantErr string
	}{
		{ContextPage, "item.x", OutputList, "undeclared reference to 'item' (at column 1)"},
		{ContextItem, "responses", OutputAny, "undeclared reference to 'responses'"},
		{ContextItem, "item.x +", OutputAny, "invalid expression: Syntax error"},
		{ContextItem, "'up'", OutputNumber, "expression must return a number or a bool, not string"},
		{ContextItem, "1", OutputBool, "expression must return a bool, not int"},
		{ContextPage, "1", OutputList, "expression must return a list"},
		{ContextItem, "[1]", OutputString, "expression must return a string"},
		{ContextItem, "unknownFn(item)", OutputAny, "undeclared reference to 'unknownFn'"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			_, err := e.Compile(tt.ctx, tt.src, tt.out)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Compile() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
	// Dyn results are accepted and checked at runtime.
	for _, src := range []string{"item.status", "item.a == 1 ? item.b : item.c"} {
		if _, err := e.Compile(ContextItem, src, OutputNumber); err != nil {
			t.Errorf("Compile(%q) = %v", src, err)
		}
	}
}

func TestCostLimit(t *testing.T) {
	e, err := NewEnv(100)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ContextItem, "[1,2,3,4,5,6,7,8,9,10].map(x, [1,2,3,4,5,6,7,8,9,10].map(y, x * y)).size()", OutputAny)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Eval(&Activation{}); err == nil || !strings.Contains(err.Error(), "cost limit") {
		t.Fatalf("Eval() error = %v, want cost limit exceeded", err)
	}
}

func TestDecodeJSON(t *testing.T) {
	v, err := DecodeJSON([]byte(`{"id": 9007199254740993, "ratio": 0.5, "big": 1e300, "n": [1, 2.0], "s": "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": int64(9007199254740993), "ratio": 0.5, "big": 1e300, "n": []any{int64(1), 2.0}, "s": "x"}
	if diff := cmp.Diff(want, v); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
	for _, bad := range []string{`{"a":`, `{} {}`, ``} {
		if _, err := DecodeJSON([]byte(bad)); err == nil {
			t.Errorf("DecodeJSON(%q) succeeded", bad)
		}
	}
	// Precision is kept through CEL.
	e := newEnv(t)
	got, err := eval(t, e, "string(item.id)", v)
	if err != nil || got != "9007199254740993" {
		t.Fatalf("string(item.id) = %v, %v", got, err)
	}
}

func TestConversions(t *testing.T) {
	if f, err := ToFloat(types.Bool(true)); err != nil || f != 1 {
		t.Errorf("ToFloat(true) = %v, %v", f, err)
	}
	if _, err := ToFloat(types.NullValue); err == nil || err.Error() != "value is null" {
		t.Errorf("ToFloat(null) error = %v", err)
	}
	if _, err := ToFloat(types.String("1")); err == nil {
		t.Error("ToFloat(string) succeeded")
	}
	for _, tt := range []struct {
		in   any
		want string
	}{{types.Double(1.5), "1.5"}, {types.Int(42), "42"}, {types.Uint(7), "7"}, {types.Bool(true), "true"}, {types.NullValue, ""}, {types.String("s"), "s"}} {
		got, err := ToLabel(adapt(tt.in))
		if err != nil || got != tt.want {
			t.Errorf("ToLabel(%v) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := ToLabel(adapt([]any{1})); err == nil {
		t.Error("ToLabel(list) succeeded")
	}
	if _, _, err := ToTimestamp(adapt("x")); err == nil {
		t.Error("ToTimestamp(string) succeeded")
	}
	if _, err := ToBool(adapt(1)); err == nil {
		t.Error("ToBool(int) succeeded")
	}
}

func devicesExtract() *config.Extract {
	return &config.Extract{
		Items:  "response.body.devices",
		Filter: "item.status != 'gone'",
		Metrics: []config.Metric{
			{
				Name: "device_up", Type: config.MetricGauge, Help: "Up", Value: "item.status == 'online'",
				Labels: map[string]config.Expr{"serial": "item.serial", "site": "default(item.site, '')"},
			},
			{
				Name: "device_clients", Type: config.MetricGauge, Value: "item.clients",
				Labels: map[string]config.Expr{"serial": "item.serial"}, SkipIf: "!has(item.clients)",
			},
			{
				Name: "device_rx_bytes_total", Type: config.MetricCounter, Unit: "bytes", Value: "toFloat(item.rx)",
				Labels: map[string]config.Expr{"serial": "item.serial"}, Timestamp: "parseTimestamp(item.at)",
			},
		},
		Info: []config.InfoMetric{
			{Name: "device_info", Labels: map[string]config.Expr{"serial": "item.serial", "model": "item.model", "port": "item.port"}},
		},
		Post: []config.Metric{
			{Name: "devices_total", Type: config.MetricGauge, Value: "double(size(responses.map(r, r.body.devices).flatten()))"},
		},
	}
}

func page(t *testing.T, body string, at time.Time) Page {
	t.Helper()
	v, err := DecodeJSON([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return Page{Status: 200, Headers: http.Header{"X-Total": {"3"}}, Body: v, ReceivedAt: at}
}

func TestEvaluate(t *testing.T) {
	e := newEnv(t)
	x, errs := e.CompileExtract(devicesExtract())
	if len(errs) != 0 {
		t.Fatalf("CompileExtract() errors: %v", errs)
	}
	t1, t2 := now.Add(time.Second), now.Add(2*time.Second)
	in := &Input{
		Pages: []Page{
			page(t, `{"devices": [
				{"serial": "A", "site": "north", "status": "online", "clients": 3, "rx": "10", "at": "2026-11-02T11:00:00Z", "model": "M1", "port": 8080},
				{"serial": "B", "status": "offline", "rx": 20, "at": "2026-11-02T11:00:00Z", "model": "M2", "port": 1}
			]}`, t1),
			page(t, `{"devices": [
				{"serial": "C", "status": "gone", "rx": 0, "at": "x", "model": "M3", "port": 2},
				{"serial": "D", "status": "online", "clients": "many", "rx": "bad", "at": "2026-11-02T11:00:00Z", "model": "M4", "port": 3}
			]}`, t2),
		},
		Labels: []map[string]string{{"tenant": "acme", "site": "account-site"}, {"env": "prod"}},
	}
	res, err := x.Evaluate(in, Limits{LabelConflict: "override"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Items != 4 {
		t.Errorf("items = %d, want 4", res.Items)
	}
	// D: clients "many" is not a number and rx "bad" fails toFloat.
	if res.MappingErrors != 2 || len(res.Errors) != 2 {
		t.Errorf("mapping errors = %d %v", res.MappingErrors, res.Errors)
	}
	if len(res.ErrorsByMetric) != 2 || res.ErrorsByMetric["device_clients"] != 1 {
		t.Errorf("mapping errors by metric = %v", res.ErrorsByMetric)
	}
	if !strings.Contains(res.Errors[0].Error(), "metrics[1] (device_clients): value: value must be a number or a bool, got string") {
		t.Errorf("first error = %v", res.Errors[0])
	}

	got := map[string]model.Sample{}
	for _, s := range res.Samples {
		got[s.Name+"/"+s.Labels.Get("serial")] = s
	}
	base := model.Labels{{Name: "env", Value: "prod"}, {Name: "tenant", Value: "acme"}}
	with := func(extra ...model.Label) model.Labels {
		ls := append(model.Labels{}, extra...)
		ls = append(ls, base...)
		return model.LabelsFromMap(func() map[string]string {
			m := map[string]string{}
			for _, l := range ls {
				m[l.Name] = l.Value
			}
			return m
		}())
	}
	src := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	want := map[string]model.Sample{
		"device_up/A": {
			Name: "device_up", Type: model.Gauge, Help: "Up", Value: 1, Timestamp: t1,
			// The account label "site" overrides the mapping label.
			Labels: with(model.Label{Name: "serial", Value: "A"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_up/B": {
			Name: "device_up", Type: model.Gauge, Help: "Up", Value: 0, Timestamp: t1,
			Labels: with(model.Label{Name: "serial", Value: "B"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_up/D": {
			Name: "device_up", Type: model.Gauge, Help: "Up", Value: 1, Timestamp: t2,
			Labels: with(model.Label{Name: "serial", Value: "D"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_clients/A": {
			Name: "device_clients", Type: model.Gauge, Value: 3, Timestamp: t1,
			Labels: with(model.Label{Name: "serial", Value: "A"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_rx_bytes_total/A": {
			Name: "device_rx_bytes_total", Type: model.Counter, Unit: "bytes", Value: 10,
			Timestamp: src, SourceTimestamp: true, Labels: with(model.Label{Name: "serial", Value: "A"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_rx_bytes_total/B": {
			Name: "device_rx_bytes_total", Type: model.Counter, Unit: "bytes", Value: 20,
			Timestamp: src, SourceTimestamp: true, Labels: with(model.Label{Name: "serial", Value: "B"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_info/A": {
			Name: "device_info", Type: model.Gauge, Value: 1, Timestamp: t1,
			Labels: with(model.Label{Name: "serial", Value: "A"}, model.Label{Name: "model", Value: "M1"}, model.Label{Name: "port", Value: "8080"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_info/B": {
			Name: "device_info", Type: model.Gauge, Value: 1, Timestamp: t1,
			Labels: with(model.Label{Name: "serial", Value: "B"}, model.Label{Name: "model", Value: "M2"}, model.Label{Name: "port", Value: "1"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"device_info/D": {
			Name: "device_info", Type: model.Gauge, Value: 1, Timestamp: t2,
			Labels: with(model.Label{Name: "serial", Value: "D"}, model.Label{Name: "model", Value: "M4"}, model.Label{Name: "port", Value: "3"}, model.Label{Name: "site", Value: "account-site"}),
		},
		"devices_total/": {
			Name: "devices_total", Type: model.Gauge, Value: 4, Timestamp: t2,
			Labels: with(model.Label{Name: "site", Value: "account-site"}),
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("samples (-want +got):\n%s", diff)
	}
}

func TestEvaluateLabelConflictError(t *testing.T) {
	e := newEnv(t)
	x, _ := e.CompileExtract(&config.Extract{Metrics: []config.Metric{
		{Name: "m", Value: "1", Labels: map[string]config.Expr{"site": "item.site"}},
	}})
	in := &Input{Pages: []Page{page(t, `[{"site": "north"}, {"site": "south"}]`, now)}, Labels: []map[string]string{{"site": "south"}}}
	res, err := x.Evaluate(in, Limits{LabelConflict: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Samples) != 1 || res.MappingErrors != 1 || !strings.Contains(res.Errors[0].Error(), `label site set by the mapping ("north") conflicts with a configured label ("south")`) {
		t.Fatalf("samples %v, errors %v", res.Samples, res.Errors)
	}
}

func TestEvaluateFailures(t *testing.T) {
	e := newEnv(t)
	plain, _ := e.CompileExtract(&config.Extract{Metrics: []config.Metric{{Name: "m", Value: "1"}}})
	if _, err := plain.Evaluate(&Input{Pages: []Page{page(t, `{"a": 1}`, now)}}, Limits{}); err == nil ||
		!strings.Contains(err.Error(), "the response body is not a list") {
		t.Errorf("non-list body error = %v", err)
	}
	items, _ := e.CompileExtract(&config.Extract{Items: "response.body.items", Metrics: []config.Metric{{Name: "m", Value: "1"}}})
	if _, err := items.Evaluate(&Input{Pages: []Page{page(t, `{"items": 3}`, now)}}, Limits{}); err == nil ||
		!strings.Contains(err.Error(), "extract.items: items must be a list, got int") {
		t.Errorf("items type error = %v", err)
	}
	if _, err := items.Evaluate(&Input{Pages: []Page{page(t, `{}`, now)}}, Limits{}); err == nil ||
		!strings.Contains(err.Error(), "extract.items: no such key: items") {
		t.Errorf("missing items error = %v", err)
	}
	many := &Input{Pages: []Page{page(t, `[1, 2, 3]`, now)}}
	if _, err := plain.Evaluate(many, Limits{MaxItems: 2}); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("max items error = %v", err)
	}
	if _, err := plain.Evaluate(many, Limits{MaxSamples: 2}); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("max samples error = %v", err)
	}
	if res, err := plain.Evaluate(many, Limits{}); err != nil || len(res.Samples) != 3 || res.Samples[0].Value != 1 {
		t.Errorf("default items = %v, %v", res, err)
	}
}

func TestCompileExtractErrors(t *testing.T) {
	e := newEnv(t)
	_, errs := e.CompileExtract(&config.Extract{
		Items:  "item",
		Filter: "'yes'",
		Metrics: []config.Metric{{
			Name: "m", Value: "'x'", Labels: map[string]config.Expr{"a": "[1]"},
			Timestamp: "1", SkipIf: "2",
		}},
		Info:      []config.InfoMetric{{Name: "m_info", Labels: map[string]config.Expr{"b": "{}"}}},
		Post:      []config.Metric{{Name: "p", Value: "item.x"}},
		Inventory: &config.Inventory{Key: "[]", Fields: map[string]config.Expr{"f": "item."}},
	})
	var got []string
	for _, fe := range errs {
		got = append(got, strings.Join(fe.Path, "."))
	}
	want := []string{
		"items", "filter", "metrics.0.value", "metrics.0.timestamp", "metrics.0.skipIf", "metrics.0.labels.a",
		"post.0.value", "info.0.labels.b", "inventory.key", "inventory.fields.f",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("error paths (-want +got):\n%s", diff)
	}
	if x, errs := e.CompileExtract(nil); x == nil || errs != nil {
		t.Error("CompileExtract(nil) must compile to an empty extract")
	}
}

func TestFromEpochRejectsNaN(t *testing.T) {
	if v := fromEpoch(types.Double(math.NaN()), time.Second); !types.IsError(v) {
		t.Fatalf("fromEpoch(NaN) = %v", v)
	}
}

func TestAccumulateWindows(t *testing.T) {
	e := newEnv(t)
	metric := func(window config.Expr) config.Metric {
		return config.Metric{
			Name: "bytes_total", Type: config.MetricCounter, Value: "double(item.v)", Accumulate: true, WindowField: window,
			Labels: map[string]config.Expr{"id": "item.id"},
		}
	}
	x, errs := e.CompileExtract(&config.Extract{Items: "response.body", Metrics: []config.Metric{
		metric("item.w"), metric(""),
	}})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	in := &Input{Pages: []Page{page(t, `[
		{"id": "string", "v": 1, "w": "2026-11-02T11:55"},
		{"id": "int", "v": 2, "w": 42},
		{"id": "time", "v": 3, "w": "x"},
		{"id": "negative", "v": -1, "w": 1},
		{"id": "null", "v": 4, "w": null},
		{"id": "list", "v": 5, "w": [1]}
	]`, now)}}
	res, err := x.Evaluate(in, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	windows := map[string]string{}
	for _, s := range res.Samples {
		if !s.Accumulate {
			t.Errorf("%v not marked", s.Labels)
		}
		windows[s.Labels.Get("id")+"/"+strconv.FormatBool(s.Window == now.Format(time.RFC3339Nano))] = s.Window
	}
	want := map[string]string{
		"string/false": "2026-11-02T11:55", "int/false": "42", "time/false": "x",
		// Without windowField, the window is the sample timestamp.
		"string/true": now.Format(time.RFC3339Nano), "int/true": now.Format(time.RFC3339Nano), "time/true": now.Format(time.RFC3339Nano),
		"null/true": now.Format(time.RFC3339Nano), "list/true": now.Format(time.RFC3339Nano),
	}
	if diff := cmp.Diff(want, windows); diff != "" {
		t.Errorf("windows (-want +got):\n%s", diff)
	}
	// Negative values (both metrics), a null and a list window are errors.
	if res.MappingErrors != 4 || res.ErrorsByMetric["bytes_total"] != 4 {
		t.Errorf("mapping errors = %d %v", res.MappingErrors, res.Errors)
	}
	for _, msg := range []string{"must not be negative", "window is null", "window must be a string, a number, a bool or a timestamp, got list"} {
		if !strings.Contains(fmt.Sprint(res.Errors), msg) {
			t.Errorf("no error %q in %v", msg, res.Errors)
		}
	}
	v, err := windowString(types.Timestamp{Time: now})
	if err != nil || v != now.Format(time.RFC3339Nano) {
		t.Errorf("timestamp window = %q, %v", v, err)
	}
}

// TestInventory: the items of an inventory job, by key: duplicates kept
// once, an empty key or a failing field counted as a mapping error
// without stopping the others.
func TestInventory(t *testing.T) {
	e := newEnv(t)
	x, errs := e.CompileExtract(&config.Extract{
		Items: "response.body",
		Inventory: &config.Inventory{
			Key:    "item.id",
			Fields: map[string]config.Expr{"name": "item.name", "size": "item.size + 1"},
		},
	})
	if len(errs) != 0 {
		t.Fatalf("CompileExtract() errors: %v", errs)
	}
	res, err := x.Evaluate(&Input{Pages: []Page{page(t, `[
		{"id": "n1", "name": "first", "size": 1},
		{"id": "n1", "name": "duplicate", "size": 2},
		{"id": "", "name": "no key", "size": 3},
		{"id": "n2", "name": "bad size", "size": "x"},
		{"id": "n3", "name": "third", "size": 3}
	]`, now)}}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range res.Inventory {
		got = append(got, fmt.Sprintf("%s=%v/%v", it.Key, it.Fields["name"], it.Fields["size"]))
	}
	if strings.Join(got, " ") != "n1=first/2 n3=third/4" {
		t.Errorf("inventory = %v", got)
	}
	if res.MappingErrors != 2 {
		t.Errorf("%d mapping errors, want 2 (empty key, bad field): %v", res.MappingErrors, res.Errors)
	}
}
