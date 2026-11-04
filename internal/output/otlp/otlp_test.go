package otlp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/output/push"
	"github.com/kentrow/tapyr/internal/runtime"
	"github.com/kentrow/tapyr/internal/secret"
)

var start = time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)

func newOutput(t *testing.T, endpoint, protocol string, clk clock.Clock) *Output {
	t.Helper()
	o, err := New(Options{
		Name: "collector",
		Spec: config.OutputSpec{
			Type: config.OutputOTLP,
			OTLP: &config.OTLPOutput{
				Endpoint: endpoint, Protocol: protocol, Timeout: config.Duration(2 * time.Second),
				ResourceAttributes: map[string]string{"deployment.environment": "lab"},
			},
			ExternalLabels: map[string]string{"tapyr_instance": "lab"},
			Queue:          &config.Queue{Capacity: 100, MaxBatch: 100, FlushInterval: config.Duration(5 * time.Second)},
		},
		Credentials: &runtime.OutputCredentials{Headers: map[string]secret.Value{"api-key": secret.New("k3y")}},
		Clock:       clk, Version: "1.2.3", InstanceID: "instance-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func labels(kv ...string) model.Labels {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return model.LabelsFromMap(m)
}

// describe flattens metrics into sorted lines.
func describe(md pmetric.Metrics) []string {
	var out []string
	for i := range md.ResourceMetrics().Len() {
		rm := md.ResourceMetrics().At(i)
		var res []string
		rm.Resource().Attributes().Range(func(k string, v pcommon.Value) bool {
			res = append(res, k+"="+v.AsString())
			return true
		})
		slices.Sort(res)
		for j := range rm.ScopeMetrics().Len() {
			sm := rm.ScopeMetrics().At(j)
			for k := range sm.Metrics().Len() {
				m := sm.Metrics().At(k)
				var dps pmetric.NumberDataPointSlice
				kind := "gauge"
				if m.Type() == pmetric.MetricTypeSum {
					dps, kind = m.Sum().DataPoints(), "sum"
					if !m.Sum().IsMonotonic() || m.Sum().AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
						kind = "bad-sum"
					}
				} else {
					dps = m.Gauge().DataPoints()
				}
				for l := range dps.Len() {
					dp := dps.At(l)
					var attrs []string
					dp.Attributes().Range(func(k string, v pcommon.Value) bool {
						attrs = append(attrs, k+"="+v.AsString())
						return true
					})
					slices.Sort(attrs)
					value := fmt.Sprint(dp.DoubleValue())
					if dp.Flags().NoRecordedValue() {
						value = "STALE"
					}
					line := fmt.Sprintf("[%s] %s/%s %s %s{%s} %s@%s", strings.Join(res, ","), sm.Scope().Name(), sm.Scope().Version(),
						kind, m.Name(), strings.Join(attrs, ","), value, dp.Timestamp().AsTime().Sub(start))
					if kind == "sum" {
						line += " start " + dp.StartTimestamp().AsTime().Sub(start).String()
					}
					if m.Description() != "" || m.Unit() != "" {
						line += fmt.Sprintf(" (%s, %s)", m.Description(), m.Unit())
					}
					out = append(out, line)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

func batch() model.Batch {
	return model.Batch{Provider: "meraki", Account: "acme", At: start, Samples: []model.Sample{
		{Name: "device_up", Type: model.Gauge, Help: "Device status.", Labels: labels("serial", "A"), Value: 1, Timestamp: start},
		{Name: "bytes_total", Type: model.Counter, Unit: "By", Labels: labels("serial", "A"), Value: 42, Timestamp: start.Add(-time.Minute)},
	}}
}

func TestHTTP(t *testing.T) {
	var mu sync.Mutex
	var got []pmetric.Metrics
	var headers []http.Header
	var paths []string
	statuses := []int{http.StatusServiceUnavailable}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if len(statuses) > 0 {
			w.WriteHeader(statuses[0])
			statuses = statuses[1:]
			return
		}
		req := pmetricotlp.NewExportRequest()
		if err := req.UnmarshalProto(body); err != nil {
			t.Errorf("decoding: %v", err)
		}
		got, headers, paths = append(got, req.Metrics()), append(headers, r.Header.Clone()), append(paths, r.URL.Path)
		resp := pmetricotlp.NewExportResponse()
		if len(got) == 2 {
			resp.PartialSuccess().SetRejectedDataPoints(1)
			resp.PartialSuccess().SetErrorMessage("one point too old")
		}
		data, _ := resp.MarshalProto()
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	clk := clock.NewFake(start)
	o := newOutput(t, srv.URL, "", clk)

	// The first attempt fails (503): sent again after a backoff.
	b := batch()
	_, err := o.Send(context.Background(), []push.Point{{Sample: b.Samples[0]}})
	if e, ok := errors.AsType[*push.Error](err); !ok || e.Class != push.ClassTransient {
		t.Fatalf("503: %v", err)
	}

	o.Enqueue(b)
	clk.Advance(time.Minute)
	// The counter goes stale; the gauge is withdrawn with its instance.
	o.Enqueue(model.Batch{Provider: "meraki", Account: "acme", At: clk.Now(), Stale: []model.Series{{Name: "bytes_total", Labels: labels("serial", "A")}}})
	o.Retire("devices", []model.Series{{Name: "device_up", Labels: labels("serial", "A")}, {Name: "unknown", Labels: labels("x", "y")}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = o.Run(ctx) }()
	if err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	// A partial success counts the rejected points.
	res2, err := o.Send(context.Background(), []push.Point{{Sample: b.Samples[0]}, {Sample: b.Samples[1]}})
	if err != nil || res2.Rejected != 1 || res2.Message != "one point too old" {
		t.Errorf("partial success = %+v, %v", res2, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("%d requests", len(got))
	}
	res := "deployment.environment=lab,service.instance.id=instance-1,service.name=tapyr"
	scope := ScopeName + "/1.2.3"
	want := []string{
		"[" + res + "] " + scope + " gauge unknown{tapyr_instance=lab,x=y} STALE@1m0s",
		"[" + res + ",tapyr.account=acme,tapyr.provider=meraki] " + scope + " gauge device_up{serial=A,tapyr_instance=lab} 1@0s (Device status., )",
		"[" + res + ",tapyr.account=acme,tapyr.provider=meraki] " + scope + " gauge device_up{serial=A,tapyr_instance=lab} STALE@1m0s (Device status., )",
		"[" + res + ",tapyr.account=acme,tapyr.provider=meraki] " + scope + " sum bytes_total{serial=A,tapyr_instance=lab} 42@-1m0s start -1m0s (, By)",
		"[" + res + ",tapyr.account=acme,tapyr.provider=meraki] " + scope + " sum bytes_total{serial=A,tapyr_instance=lab} STALE@1m0s start -1m0s (, By)",
	}
	slices.Sort(want)
	if diff := cmp.Diff(want, describe(got[0])); diff != "" {
		t.Errorf("metrics (-want +got):\n%s", diff)
	}
	if paths[0] != "/v1/metrics" || headers[0].Get("Content-Type") != "application/x-protobuf" ||
		headers[0].Get("api-key") != "k3y" || headers[0].Get("User-Agent") != "tapyr/1.2.3" {
		t.Errorf("request %s %v", paths[0], headers[0])
	}
	if o.Name() != "collector" || o.State().Sent != 5 {
		t.Errorf("state = %+v", o.State())
	}
}

func TestHTTPErrors(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusTooManyRequests:     push.ClassThrottled,
		http.StatusBadGateway:          push.ClassTransient,
		http.StatusGatewayTimeout:      push.ClassTransient,
		http.StatusInternalServerError: push.ClassRejected,
		http.StatusBadRequest:          push.ClassRejected,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		o := newOutput(t, srv.URL+"/custom/path", "", clock.NewFake(start))
		_, err := o.Send(context.Background(), []push.Point{{Sample: model.Sample{Name: "up", Timestamp: start}}})
		if e, ok := errors.AsType[*push.Error](err); !ok || e.Class != want {
			t.Errorf("%d: %v", status, err)
		}
		srv.Close()
		_, err = o.Send(context.Background(), []push.Point{{Sample: model.Sample{Name: "up", Timestamp: start}}})
		if e, ok := errors.AsType[*push.Error](err); !ok || e.Class != push.ClassTransient || strings.Contains(err.Error(), srv.URL) {
			t.Errorf("closed server: %v", err)
		}
	}
	for _, endpoint := range []string{"collector:4318", "ftp://collector", "http://"} {
		if _, err := New(Options{Name: "x", Spec: config.OutputSpec{OTLP: &config.OTLPOutput{Endpoint: endpoint}}}); err == nil {
			t.Errorf("endpoint %q accepted", endpoint)
		}
	}
}

// grpcServer records the exports it receives.
type grpcServer struct {
	pmetricotlp.UnimplementedGRPCServer
	mu   sync.Mutex
	got  []pmetric.Metrics
	keys []string
	fail error
}

func (s *grpcServer) Export(ctx context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return pmetricotlp.NewExportResponse(), s.fail
	}
	md, _ := metadata.FromIncomingContext(ctx)
	s.got = append(s.got, req.Metrics())
	s.keys = append(s.keys, strings.Join(md.Get("api-key"), ","))
	return pmetricotlp.NewExportResponse(), nil
}

func TestGRPC(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	srv := &grpcServer{}
	pmetricotlp.RegisterGRPCServer(gs, srv)
	go func() { _ = gs.Serve(ln) }()
	defer gs.Stop()

	clk := clock.NewFake(start)
	o := newOutput(t, "http://"+ln.Addr().String(), ProtocolGRPC, clk)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = o.Run(ctx) }()
	o.Enqueue(batch())
	if err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.fail = status.Error(codes.InvalidArgument, "bad")
	srv.mu.Unlock()
	_, err = o.Send(context.Background(), []push.Point{{Sample: model.Sample{Name: "up", Timestamp: start}}})
	if e, ok := errors.AsType[*push.Error](err); !ok || e.Class != push.ClassRejected || !strings.Contains(err.Error(), "InvalidArgument: bad") {
		t.Errorf("rejected export: %v", err)
	}
	cancel()
	<-done
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.got) != 1 || srv.got[0].DataPointCount() != 2 || srv.keys[0] != "k3y" {
		t.Errorf("exports: %d, keys %v", len(srv.got), srv.keys)
	}
}

func TestGRPCError(t *testing.T) {
	withRetry := func(c codes.Code, d time.Duration) error {
		st, err := status.New(c, "busy").WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(d)})
		if err != nil {
			t.Fatal(err)
		}
		return st.Err()
	}
	tests := []struct {
		err       error
		class     string
		retryWait time.Duration
	}{
		{status.Error(codes.Unavailable, "down"), push.ClassTransient, 0},
		{withRetry(codes.Unavailable, 3*time.Second), push.ClassTransient, 3 * time.Second},
		{status.Error(codes.ResourceExhausted, "full"), push.ClassRejected, 0},
		{withRetry(codes.ResourceExhausted, time.Hour), push.ClassThrottled, 5 * time.Minute},
		{status.Error(codes.PermissionDenied, "no"), push.ClassRejected, 0},
		{errors.New("not a status"), push.ClassRejected, 0},
	}
	for _, tt := range tests {
		e := grpcError(tt.err)
		if e.Class != tt.class || e.RetryAfter != tt.retryWait {
			t.Errorf("%v: %s %v", tt.err, e.Class, e.RetryAfter)
		}
	}
}
