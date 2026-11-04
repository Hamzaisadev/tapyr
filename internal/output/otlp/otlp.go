// Package otlp is the OTLP metrics output, over HTTP (protobuf) or gRPC.
// Payloads are built with pdata, not the OpenTelemetry metrics SDK, so
// that points keep their source timestamps (ADR 0008; see "otlp" in
// docs/spec/06-mapping-and-outputs.md).
package otlp

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
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

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/output"
	"github.com/kentrow/tapyr/internal/output/push"
	"github.com/kentrow/tapyr/internal/runtime"
)

// ScopeName is the instrumentation scope of the exported metrics.
const ScopeName = "github.com/kentrow/tapyr"

// Protocols.
const (
	ProtocolHTTP = "http/protobuf"
	ProtocolGRPC = "grpc"
)

// maxResponseBody bounds the response bodies read.
const maxResponseBody = 64 << 10

// Options configures an Output.
type Options struct {
	Name        string
	Spec        config.OutputSpec
	Credentials *runtime.OutputCredentials
	// Client sends the HTTP requests.
	Client *http.Client
	Clock  clock.Clock
	Logger *slog.Logger
	// Observer receives the sends (self-metrics).
	Observer push.Observer
	// Version of Tapyr: instrumentation scope version and User-Agent.
	Version string
	// InstanceID is the service.instance.id resource attribute.
	InstanceID string
	// Queue overrides the sender loop options (tests); Name, Sender, Clock,
	// Logger and Observer are set by New.
	Queue push.Options
}

// Output pushes samples with OTLP.
type Output struct {
	name     string
	protocol string
	endpoint string // HTTP URL, or gRPC target
	secure   bool
	timeout  time.Duration
	creds    *runtime.OutputCredentials
	client   *http.Client
	clock    clock.Clock
	logger   *slog.Logger
	version  string
	resource map[string]string
	external model.Labels
	queue    *push.Queue

	connMu sync.Mutex
	conn   *grpc.ClientConn
	grpc   pmetricotlp.GRPCClient

	mu     sync.Mutex
	series map[string]*seriesInfo
}

// seriesInfo is what the output remembers of a series it sends.
type seriesInfo struct {
	typ model.MetricType
	// provider and account of the resource the series was sent with.
	provider, account string
	// start is the start time of a cumulative sum: the first time the
	// output saw the series (or its earliest timestamp).
	start time.Time
}

var _ output.Output = (*Output)(nil)

// New returns an OTLP output. The endpoint is validated: an invalid one is
// an error.
func New(opts Options) (*Output, error) {
	spec := opts.Spec.OTLP
	u, err := url.Parse(spec.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("output %s: endpoint %q must be an http or https URL", opts.Name, spec.Endpoint)
	}
	o := &Output{
		name: opts.Name, protocol: cmp.Or(spec.Protocol, ProtocolHTTP), secure: u.Scheme == "https",
		timeout: spec.Timeout.Std(), creds: opts.Credentials, client: opts.Client, clock: opts.Clock, logger: opts.Logger,
		version: opts.Version, external: model.LabelsFromMap(opts.Spec.ExternalLabels), series: map[string]*seriesInfo{},
		resource: map[string]string{"service.name": "tapyr", "service.instance.id": opts.InstanceID},
	}
	for k, v := range spec.ResourceAttributes {
		o.resource[k] = v
	}
	if o.protocol == ProtocolGRPC {
		o.endpoint = u.Host
	} else {
		if u.Path == "" || u.Path == "/" {
			u.Path = "/v1/metrics"
		}
		o.endpoint = u.String()
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
	return o, nil
}

// Name implements output.Output.
func (o *Output) Name() string { return o.name }

// Enqueue implements output.Output: the samples, and a point flagged
// without a recorded value for each stale series.
func (o *Output) Enqueue(b model.Batch) {
	points := make([]push.Point, 0, len(b.Samples)+len(b.Stale))
	o.mu.Lock()
	for _, s := range b.Samples {
		s.Labels = withExternal(s.Labels, o.external)
		key := s.SeriesKey()
		info, ok := o.series[key]
		if !ok {
			info = &seriesInfo{typ: s.Type, provider: b.Provider, account: b.Account, start: o.clock.Now()}
			o.series[key] = info
		}
		if s.Timestamp.Before(info.start) {
			info.start = s.Timestamp
		}
		points = append(points, push.Point{Sample: s, Provider: b.Provider, Account: b.Account})
	}
	points = o.staleLocked(points, b.Stale, b.At, b.Provider, b.Account)
	o.mu.Unlock()
	o.queue.Add(points)
}

// Retire implements output.Output.
func (o *Output) Retire(_ string, stale []model.Series) {
	o.mu.Lock()
	points := o.staleLocked(nil, stale, o.clock.Now(), "", "")
	o.mu.Unlock()
	o.queue.Add(points)
}

// staleLocked appends the stale points of series, with the type and the
// resource the series was sent with when known; o.mu must be held. The
// series info is dropped when the point is built (build), which needs the
// start time of a stale sum.
func (o *Output) staleLocked(points []push.Point, stale []model.Series, at time.Time, provider, account string) []push.Point {
	for _, sr := range stale {
		sr.Labels = withExternal(sr.Labels, o.external)
		typ, prov, acct := model.Gauge, provider, account
		if info, ok := o.series[sr.Key()]; ok {
			typ, prov, acct = info.typ, info.provider, info.account
		}
		points = append(points, push.Point{
			Sample: model.Sample{Name: sr.Name, Labels: sr.Labels, Type: typ, Timestamp: at}, Stale: true,
			Provider: prov, Account: acct,
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
func (o *Output) Run(ctx context.Context) error {
	defer o.closeConn()
	return o.queue.Run(ctx)
}

// Flush implements output.Output.
func (o *Output) Flush(ctx context.Context) error { return o.queue.Flush(ctx) }

// State implements output.Output.
func (o *Output) State() output.State { return o.queue.State() }

// Send implements push.Sender.
func (o *Output) Send(ctx context.Context, points []push.Point) (push.Result, error) {
	req := pmetricotlp.NewExportRequestFromMetrics(o.build(points))
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	var resp pmetricotlp.ExportResponse
	var err error
	if o.protocol == ProtocolGRPC {
		resp, err = o.sendGRPC(ctx, req)
	} else {
		resp, err = o.sendHTTP(ctx, req)
	}
	if err != nil {
		return push.Result{}, err
	}
	ps := resp.PartialSuccess()
	return push.Result{Rejected: int(ps.RejectedDataPoints()), Message: ps.ErrorMessage()}, nil
}

// build turns points into OTLP metrics: one resource per provider and
// account, one metric per name.
func (o *Output) build(points []push.Point) pmetric.Metrics {
	md := pmetric.NewMetrics()
	type resKey struct{ provider, account string }
	scopes := map[resKey]pmetric.ScopeMetrics{}
	metrics := map[resKey]map[string]pmetric.Metric{}
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range points {
		p := &points[i]
		rk := resKey{p.Provider, p.Account}
		sm, ok := scopes[rk]
		if !ok {
			rm := md.ResourceMetrics().AppendEmpty()
			attrs := rm.Resource().Attributes()
			if p.Provider != "" {
				attrs.PutStr("tapyr.provider", p.Provider)
			}
			if p.Account != "" {
				attrs.PutStr("tapyr.account", p.Account)
			}
			for _, k := range slices.Sorted(func(yield func(string) bool) {
				for k := range o.resource {
					if !yield(k) {
						return
					}
				}
			}) {
				if o.resource[k] != "" {
					attrs.PutStr(k, o.resource[k])
				}
			}
			sm = rm.ScopeMetrics().AppendEmpty()
			sm.Scope().SetName(ScopeName)
			sm.Scope().SetVersion(o.version)
			scopes[rk], metrics[rk] = sm, map[string]pmetric.Metric{}
		}
		m, ok := metrics[rk][p.Name]
		if !ok {
			m = sm.Metrics().AppendEmpty()
			m.SetName(p.Name)
			m.SetDescription(p.Help)
			m.SetUnit(p.Unit)
			if p.Type == model.Counter {
				sum := m.SetEmptySum()
				sum.SetIsMonotonic(true)
				sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
			} else {
				m.SetEmptyGauge()
			}
			metrics[rk][p.Name] = m
		}
		var dp pmetric.NumberDataPoint
		if m.Type() == pmetric.MetricTypeSum {
			dp = m.Sum().DataPoints().AppendEmpty()
		} else {
			dp = m.Gauge().DataPoints().AppendEmpty()
		}
		for _, l := range p.Labels {
			dp.Attributes().PutStr(l.Name, l.Value)
		}
		dp.SetTimestamp(pcommon.NewTimestampFromTime(p.Timestamp))
		key := p.SeriesKey()
		info := o.series[key]
		if m.Type() == pmetric.MetricTypeSum && info != nil {
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(info.start))
		}
		if p.Stale {
			dp.SetFlags(pmetric.DefaultDataPointFlags.WithNoRecordedValue(true))
			delete(o.series, key)
		} else {
			dp.SetDoubleValue(p.Value)
		}
	}
	return md
}

func (o *Output) sendHTTP(ctx context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	resp := pmetricotlp.NewExportResponse()
	body, err := req.MarshalProto()
	if err != nil {
		return resp, &push.Error{Class: push.ClassRejected, Err: err}
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(body))
	if err != nil {
		return resp, &push.Error{Class: push.ClassRejected, Err: err}
	}
	hreq.Header.Set("Content-Type", "application/x-protobuf")
	hreq.Header.Set("User-Agent", "tapyr/"+o.version)
	for name, v := range o.creds.Headers {
		hreq.Header.Set(name, v.Reveal())
	}
	hresp, err := o.client.Do(hreq)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err // the URL may carry credentials
		}
		return resp, &push.Error{Class: push.ClassTransient, Err: fmt.Errorf("sending: %w", err)}
	}
	defer hresp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(hresp.Body, maxResponseBody))
	retryable := func(status int) bool {
		return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
	}
	if e := push.HTTPError(hresp, data, o.clock.Now(), retryable); e != nil {
		return resp, e
	}
	if len(data) > 0 {
		if err := resp.UnmarshalProto(data); err != nil {
			o.logger.Debug("decoding the export response", "component", "output", "output", o.name, "error", err)
		}
	}
	return resp, nil
}

func (o *Output) sendGRPC(ctx context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	client, err := o.grpcClient()
	if err != nil {
		return pmetricotlp.NewExportResponse(), &push.Error{Class: push.ClassRejected, Err: err}
	}
	if len(o.creds.Headers) > 0 {
		md := metadata.MD{}
		for name, v := range o.creds.Headers {
			md.Set(name, v.Reveal())
		}
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	resp, err := client.Export(ctx, req)
	if err != nil {
		return resp, grpcError(err)
	}
	return resp, nil
}

// grpcError classifies a gRPC error (see "Failures" in the OTLP
// specification): RESOURCE_EXHAUSTED is retried only with RetryInfo.
func grpcError(err error) *push.Error {
	st, _ := status.FromError(err)
	e := &push.Error{Class: push.ClassRejected, Err: errors.New(st.Message())}
	var retryInfo *errdetails.RetryInfo
	for _, d := range st.Details() {
		if ri, ok := d.(*errdetails.RetryInfo); ok {
			retryInfo = ri
		}
	}
	if retryInfo != nil && retryInfo.GetRetryDelay() != nil {
		e.RetryAfter = min(5*time.Minute, retryInfo.GetRetryDelay().AsDuration())
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Aborted, codes.Canceled, codes.OutOfRange, codes.DataLoss:
		e.Class = push.ClassTransient
	case codes.ResourceExhausted:
		if retryInfo != nil {
			e.Class = push.ClassThrottled
		}
	}
	e.Err = fmt.Errorf("%s: %w", st.Code(), e.Err)
	return e
}

// grpcClient returns the gRPC client, created on first use (grpc.NewClient
// connects lazily).
func (o *Output) grpcClient() (pmetricotlp.GRPCClient, error) {
	o.connMu.Lock()
	defer o.connMu.Unlock()
	if o.grpc != nil {
		return o.grpc, nil
	}
	creds := insecure.NewCredentials()
	if o.secure {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	conn, err := grpc.NewClient(o.endpoint, grpc.WithTransportCredentials(creds), grpc.WithUserAgent("tapyr/"+o.version))
	if err != nil {
		return nil, fmt.Errorf("gRPC client: %w", err)
	}
	o.conn, o.grpc = conn, pmetricotlp.NewGRPCClient(conn)
	return o.grpc, nil
}

func (o *Output) closeConn() {
	o.connMu.Lock()
	defer o.connMu.Unlock()
	if o.conn != nil {
		if err := o.conn.Close(); err != nil {
			o.logger.Debug("closing the gRPC connection", "component", "output", "output", o.name, "error", err)
		}
		o.conn, o.grpc = nil, nil
	}
}
