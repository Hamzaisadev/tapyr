// Package remotewrite is the Prometheus remote write 1.0 output (see
// "remote_write" in docs/spec/06-mapping-and-outputs.md).
package remotewrite

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/snappy"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/output"
	"github.com/kentrow/tapyr/internal/output/push"
	"github.com/kentrow/tapyr/internal/runtime"
)

// metadataInterval is how often the metadata of the metric families seen
// is sent.
const metadataInterval = time.Minute

// maxResponseBody bounds the response bodies read.
const maxResponseBody = 64 << 10

// Options configures an Output.
type Options struct {
	Name        string
	Spec        config.OutputSpec
	Credentials *runtime.OutputCredentials
	Client      *http.Client
	Clock       clock.Clock
	Logger      *slog.Logger
	Observer    push.Observer
	// UserAgent is sent with every request (tapyr/<version>).
	UserAgent string
	// Queue overrides the sender loop options (tests); Name, Sender, Clock,
	// Logger and Observer are set by New.
	Queue push.Options
}

// Output pushes samples with remote write.
type Output struct {
	name     string
	url      string
	timeout  time.Duration
	creds    *runtime.OutputCredentials
	client   *http.Client
	clock    clock.Clock
	logger   *slog.Logger
	agent    string
	external model.Labels
	queue    *push.Queue

	mu       sync.Mutex
	families map[string]metadata
	types    map[string]model.MetricType // series key -> type, for stale markers
	metaSent time.Time
}

var _ output.Output = (*Output)(nil)

// New returns a remote write output.
func New(opts Options) *Output {
	rw := opts.Spec.RemoteWrite
	o := &Output{
		name: opts.Name, url: rw.URL, timeout: rw.Timeout.Std(), creds: opts.Credentials, client: opts.Client,
		clock: opts.Clock, logger: opts.Logger, agent: opts.UserAgent,
		external: model.LabelsFromMap(opts.Spec.ExternalLabels),
		families: map[string]metadata{}, types: map[string]model.MetricType{},
	}
	if o.client == nil {
		o.client = &http.Client{}
	}
	// The headers of the output (credentials) must not follow a redirect
	// to another host: a redirect is a failed send.
	nc := *o.client
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.client = &nc
	if o.logger == nil {
		o.logger = slog.New(slog.DiscardHandler)
	}
	if o.timeout <= 0 {
		o.timeout = 10 * time.Second
	}
	if o.creds == nil {
		o.creds = &runtime.OutputCredentials{}
	}
	q := opts.Queue
	if q.Capacity == 0 && opts.Spec.Queue != nil {
		q.Capacity, q.MaxBatch, q.FlushInterval = opts.Spec.Queue.Capacity, opts.Spec.Queue.MaxBatch, opts.Spec.Queue.FlushInterval.Std()
	}
	q.Name, q.Sender, q.Clock, q.Logger, q.Observer = opts.Name, o, opts.Clock, o.logger, opts.Observer
	o.queue = push.New(q)
	return o
}

// Name implements output.Output.
func (o *Output) Name() string { return o.name }

// Enqueue implements output.Output: the samples, and a staleness marker for
// each stale series.
func (o *Output) Enqueue(b model.Batch) {
	points := make([]push.Point, 0, len(b.Samples)+len(b.Stale))
	o.mu.Lock()
	for _, s := range b.Samples {
		s.Labels = withExternal(s.Labels, o.external)
		o.remember(&s)
		points = append(points, push.Point{Sample: s, Provider: b.Provider, Account: b.Account})
	}
	points = o.staleLocked(points, b.Stale, b.At)
	o.mu.Unlock()
	o.queue.Add(points)
}

// Retire implements output.Output: a staleness marker for each series.
func (o *Output) Retire(_ string, stale []model.Series) {
	o.mu.Lock()
	points := o.staleLocked(nil, stale, o.clock.Now())
	o.mu.Unlock()
	o.queue.Add(points)
}

// remember records the metadata of a sample's family and the type of its
// series; o.mu must be held.
func (o *Output) remember(s *model.Sample) {
	typ := metadataGauge
	if s.Type == model.Counter {
		typ = metadataCounter
	}
	o.families[s.Name] = metadata{typ: typ, name: s.Name, help: s.Help, unit: s.Unit}
	o.types[s.SeriesKey()] = s.Type
}

// staleLocked appends the staleness markers of series; o.mu must be held.
func (o *Output) staleLocked(points []push.Point, stale []model.Series, at time.Time) []push.Point {
	for _, sr := range stale {
		sr.Labels = withExternal(sr.Labels, o.external)
		key := sr.Key()
		typ, ok := o.types[key]
		if !ok {
			typ = model.Gauge
		}
		delete(o.types, key)
		points = append(points, push.Point{
			Sample: model.Sample{Name: sr.Name, Labels: sr.Labels, Type: typ, Timestamp: at}, Stale: true,
		})
	}
	return points
}

// withExternal adds the external labels a sample does not set.
func withExternal(ls, external model.Labels) model.Labels {
	if len(external) == 0 {
		return ls
	}
	out := slices.Clone(ls)
	for _, l := range external {
		if ls.Get(l.Name) == "" {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b model.Label) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Run implements output.Output.
func (o *Output) Run(ctx context.Context) error { return o.queue.Run(ctx) }

// Flush implements output.Output.
func (o *Output) Flush(ctx context.Context) error { return o.queue.Flush(ctx) }

// State implements output.Output.
func (o *Output) State() output.State { return o.queue.State() }

// Send implements push.Sender: the metadata when due, then the samples.
func (o *Output) Send(ctx context.Context, points []push.Point) (push.Result, error) {
	o.sendMetadata(ctx)
	byKey := map[string]*timeSeries{}
	for i := range points {
		p := &points[i]
		key := p.SeriesKey()
		ts, ok := byKey[key]
		if !ok {
			ts = &timeSeries{labels: promLabels(p.Name, p.Labels)}
			byKey[key] = ts
		}
		v := p.Value
		if p.Stale {
			v = staleNaN
		}
		ts.samples = append(ts.samples, sample{value: v, ts: p.Timestamp.UnixMilli()})
	}
	series := make([]timeSeries, 0, len(byKey))
	for _, key := range slices.Sorted(func(yield func(string) bool) {
		for k := range byKey {
			if !yield(k) {
				return
			}
		}
	}) {
		ts := byKey[key]
		slices.SortStableFunc(ts.samples, func(a, b sample) int { return cmp.Compare(a.ts, b.ts) })
		series = append(series, *ts)
	}
	return push.Result{}, o.post(ctx, appendWriteRequest(nil, series, nil))
}

// sendMetadata sends the metadata of every family seen, on the first send
// and every metadataInterval. Failures are logged: the samples are sent
// anyway.
func (o *Output) sendMetadata(ctx context.Context) {
	o.mu.Lock()
	now := o.clock.Now()
	if !o.metaSent.IsZero() && now.Sub(o.metaSent) < metadataInterval {
		o.mu.Unlock()
		return
	}
	o.metaSent = now
	meta := make([]metadata, 0, len(o.families))
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range o.families {
			if !yield(k) {
				return
			}
		}
	}) {
		meta = append(meta, o.families[name])
	}
	o.mu.Unlock()
	if len(meta) == 0 {
		return
	}
	if err := o.post(ctx, appendWriteRequest(nil, nil, meta)); err != nil {
		o.logger.Warn("sending metadata", "component", "output", "output", o.name, "error", err)
	}
}

// promLabels returns the labels of a series with __name__, sorted by name.
func promLabels(name string, ls model.Labels) []label {
	out := make([]label, 0, len(ls)+1)
	out = append(out, label{"__name__", name})
	for _, l := range ls {
		out = append(out, label{l.Name, l.Value})
	}
	slices.SortFunc(out, func(a, b label) int { return strings.Compare(a.name, b.name) })
	return out
}

// post sends a compressed write request.
func (o *Output) post(ctx context.Context, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(snappy.Encode(nil, body)))
	if err != nil {
		return &push.Error{Class: push.ClassRejected, Err: err}
	}
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	if o.agent != "" {
		req.Header.Set("User-Agent", o.agent)
	}
	for name, v := range o.creds.Headers {
		req.Header.Set(name, v.Reveal())
	}
	if !o.creds.Username.IsZero() {
		req.SetBasicAuth(o.creds.Username.Reveal(), o.creds.Password.Reveal())
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return &push.Error{Class: push.ClassTransient, Err: fmt.Errorf("sending: %w", sanitize(err))}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if e := push.HTTPError(resp, data, o.clock.Now(), func(status int) bool { return status >= 500 }); e != nil {
		return e
	}
	return nil
}

// sanitize drops the URL from transport errors: it may carry credentials.
func sanitize(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}
