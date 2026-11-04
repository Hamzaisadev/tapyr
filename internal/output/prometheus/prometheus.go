// Package prometheus is the pull output: it holds the samples of the last
// successful run of every job instance and serves them on /metrics in the
// Prometheus text format or OpenMetrics, by content negotiation, gzipped
// when the client accepts it.
package prometheus

import (
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/output"
)

// Output is the prometheus pull output.
type Output struct {
	name             string
	exposeTimestamps bool
	logger           *slog.Logger

	mu        sync.RWMutex
	instances map[string][]model.Sample
}

// New returns a pull output.
func New(name string, exposeTimestamps bool, logger *slog.Logger) *Output {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Output{name: name, exposeTimestamps: exposeTimestamps, logger: logger, instances: map[string][]model.Sample{}}
}

// Name implements output.Output.
func (o *Output) Name() string { return o.name }

// Enqueue replaces the series of the batch's job instance: series absent
// from the latest successful run disappear from the exposition.
func (o *Output) Enqueue(b model.Batch) {
	samples := slices.Clone(b.Samples)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.instances[b.Instance] = samples
}

// Retire implements output.Output: the series of the instance leave the
// exposition.
func (o *Output) Retire(instance string, _ []model.Series) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.instances, instance)
}

// Run implements output.Output; the pull output has no sender loop.
func (o *Output) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Flush implements output.Output.
func (o *Output) Flush(context.Context) error { return nil }

// State implements output.Output: the pull output has no queue.
func (o *Output) State() output.State { return output.State{} }

// acceptedFormats are the exposition formats served, in order of preference
// when the client accepts several.
var acceptedFormats = []expfmt.Format{expfmt.FmtOpenMetrics_1_0_0, expfmt.FmtOpenMetrics_0_0_1, expfmt.FmtText}

// gzipWriters reuses the compressors between scrapes: each one holds
// several hundred kilobytes of buffers.
var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// Handler serves the exposition.
func (o *Output) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		format := expfmt.NegotiateAccept(r.Header, acceptedFormats...)
		w.Header().Set("Content-Type", string(format))
		w.Header().Add("Vary", "Accept-Encoding")
		var out io.Writer = w
		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzipWriters.Get().(*gzip.Writer)
			gz.Reset(w)
			defer func() {
				if err := gz.Close(); err != nil {
					o.logger.Warn("compressing metrics", "component", "output", "output", o.name, "error", err)
				}
				gzipWriters.Put(gz)
			}()
			out = gz
		}
		enc := expfmt.NewEncoder(out, format)
		for _, mf := range o.Families() {
			if err := enc.Encode(mf); err != nil {
				o.logger.Warn("encoding metrics", "component", "output", "output", o.name, "error", err)
				return
			}
		}
		if c, ok := enc.(expfmt.Closer); ok {
			if err := c.Close(); err != nil {
				o.logger.Warn("encoding metrics", "component", "output", "output", o.name, "error", err)
			}
		}
	})
}

// acceptsGzip reports whether Accept-Encoding values list gzip with a
// non-zero quality (RFC 9110, section 12.5.3).
func acceptsGzip(values []string) bool {
	for _, v := range values {
		for coding := range strings.SplitSeq(v, ",") {
			name, params, _ := strings.Cut(coding, ";")
			if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
				continue
			}
			q := 1.0
			for param := range strings.SplitSeq(params, ";") {
				k, val, _ := strings.Cut(param, "=")
				if strings.EqualFold(strings.TrimSpace(k), "q") {
					if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
						q = f
					}
				}
			}
			return q > 0
		}
	}
	return false
}

// Families returns the metric families, sorted by name, each with its
// metrics sorted by labels. A series produced by several job instances is
// exposed once (the first instance in name order wins).
func (o *Output) Families() []*dto.MetricFamily {
	o.mu.RLock()
	names := make([]string, 0, len(o.instances))
	for name := range o.instances {
		names = append(names, name)
	}
	slices.Sort(names)
	families := map[string]*dto.MetricFamily{}
	keys := map[string]map[string]bool{}
	for _, instance := range names {
		for i := range o.instances[instance] {
			s := &o.instances[instance][i]
			mf, ok := families[s.Name]
			if !ok {
				mf = newFamily(s)
				families[s.Name] = mf
				keys[s.Name] = map[string]bool{}
			}
			if key := s.Labels.Key(); !keys[s.Name][key] {
				keys[s.Name][key] = true
				mf.Metric = append(mf.Metric, o.metric(s))
			}
		}
	}
	o.mu.RUnlock()

	out := make([]*dto.MetricFamily, 0, len(families))
	for _, mf := range families {
		slices.SortFunc(mf.Metric, func(a, b *dto.Metric) int { return compareLabels(a.Label, b.Label) })
		out = append(out, mf)
	}
	slices.SortFunc(out, func(a, b *dto.MetricFamily) int { return strings.Compare(a.GetName(), b.GetName()) })
	return out
}

func newFamily(s *model.Sample) *dto.MetricFamily {
	mf := &dto.MetricFamily{Name: ptr(s.Name), Type: dto.MetricType_GAUGE.Enum()}
	if s.Type == model.Counter {
		mf.Type = dto.MetricType_COUNTER.Enum()
	}
	if s.Help != "" {
		mf.Help = ptr(s.Help)
	}
	// OpenMetrics requires the name (without _total) to end with the unit:
	// a unit written with capitals (dBm, kept as is for OTLP) is exposed as
	// the suffix of the name (dbm).
	if unit := strings.ToLower(s.Unit); unit != "" && strings.HasSuffix(strings.TrimSuffix(s.Name, "_total"), "_"+unit) {
		mf.Unit = ptr(unit)
	}
	return mf
}

func (o *Output) metric(s *model.Sample) *dto.Metric {
	m := &dto.Metric{Label: make([]*dto.LabelPair, 0, len(s.Labels))}
	for _, l := range s.Labels {
		m.Label = append(m.Label, &dto.LabelPair{Name: ptr(l.Name), Value: ptr(l.Value)})
	}
	if s.Type == model.Counter {
		m.Counter = &dto.Counter{Value: ptr(s.Value)}
	} else {
		m.Gauge = &dto.Gauge{Value: ptr(s.Value)}
	}
	if o.exposeTimestamps {
		m.TimestampMs = ptr(s.Timestamp.UnixMilli())
	}
	return m
}

func compareLabels(a, b []*dto.LabelPair) int {
	for i := range min(len(a), len(b)) {
		if c := strings.Compare(a[i].GetName(), b[i].GetName()); c != 0 {
			return c
		}
		if c := strings.Compare(a[i].GetValue(), b[i].GetValue()); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

func ptr[T any](v T) *T { return &v }
