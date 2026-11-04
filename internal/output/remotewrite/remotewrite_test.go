package remotewrite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/snappy"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/output/push"
	"github.com/kentrow/tapyr/internal/runtime"
	"github.com/kentrow/tapyr/internal/secret"
)

var start = time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)

// decoded is a decoded write request: series as "labels value@ts" lines and
// metadata as "type name help unit" lines.
type decoded struct {
	series   []string
	metadata []string
}

func decode(t *testing.T, body []byte) decoded {
	t.Helper()
	raw, err := snappy.Decode(nil, body)
	if err != nil {
		t.Fatalf("snappy: %v", err)
	}
	var out decoded
	fields(t, raw, func(num protowire.Number, v []byte) {
		switch num {
		case 1:
			var labels []string
			var samples []string
			fields(t, v, func(num protowire.Number, v []byte) {
				switch num {
				case 1:
					var name, value string
					fields(t, v, func(num protowire.Number, v []byte) {
						if num == 1 {
							name = string(v)
						} else {
							value = string(v)
						}
					})
					labels = append(labels, name+"="+value)
				case 2:
					var value float64
					var ts int64
					fieldsRaw(t, v, func(_ protowire.Number, typ protowire.Type, b []byte) int {
						switch typ {
						case protowire.Fixed64Type:
							u, n := protowire.ConsumeFixed64(b)
							if math.Float64bits(math.Float64frombits(u)) == 0x7ff0000000000002 {
								value = -1 // stale marker
							} else {
								value = math.Float64frombits(u)
							}
							return n
						default:
							u, n := protowire.ConsumeVarint(b)
							ts = int64(u)
							return n
						}
					})
					s := fmt.Sprintf("%g@%d", value, ts)
					if value == -1 {
						s = fmt.Sprintf("STALE@%d", ts)
					}
					samples = append(samples, s)
				}
			})
			out.series = append(out.series, strings.Join(labels, ",")+" "+strings.Join(samples, " "))
		case 3:
			var parts [6]string
			fieldsRaw(t, v, func(num protowire.Number, typ protowire.Type, b []byte) int {
				if typ == protowire.VarintType {
					u, n := protowire.ConsumeVarint(b)
					parts[num] = fmt.Sprint(u)
					return n
				}
				s, n := protowire.ConsumeBytes(b)
				parts[num] = string(s)
				return n
			})
			out.metadata = append(out.metadata, strings.Join([]string{parts[1], parts[2], parts[4], parts[5]}, " "))
		}
	})
	return out
}

// fields calls fn with the bytes fields of a message.
func fields(t *testing.T, b []byte, fn func(protowire.Number, []byte)) {
	t.Helper()
	fieldsRaw(t, b, func(num protowire.Number, typ protowire.Type, b []byte) int {
		if typ != protowire.BytesType {
			t.Fatalf("field %d of type %d", num, typ)
		}
		v, n := protowire.ConsumeBytes(b)
		fn(num, v)
		return n
	})
}

func fieldsRaw(t *testing.T, b []byte, fn func(protowire.Number, protowire.Type, []byte) int) {
	t.Helper()
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatal("invalid tag")
		}
		b = b[n:]
		m := fn(num, typ, b)
		if m < 0 {
			t.Fatal("invalid field")
		}
		b = b[m:]
	}
}

type server struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	requests []decoded
	headers  []http.Header
	statuses []int
	got      chan struct{}
}

func newServer(t *testing.T) *server {
	s := &server{t: t, got: make(chan struct{}, 100)}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		status := http.StatusNoContent
		if len(s.statuses) > 0 {
			status, s.statuses = s.statuses[0], s.statuses[1:]
		}
		if status < 300 {
			s.requests = append(s.requests, decode(t, body))
			s.headers = append(s.headers, r.Header.Clone())
		}
		s.mu.Unlock()
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "1")
		}
		w.WriteHeader(status)
		if status == http.StatusBadRequest {
			fmt.Fprint(w, "out of order sample")
		}
		s.got <- struct{}{}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func newOutput(t *testing.T, url string, clk clock.Clock) *Output {
	t.Helper()
	return New(Options{
		Name: "vm",
		Spec: config.OutputSpec{
			Type:           config.OutputRemoteWrite,
			RemoteWrite:    &config.RemoteWriteOutput{URL: url, Timeout: config.Duration(time.Second)},
			ExternalLabels: map[string]string{"tapyr_instance": "lab", "site": "external"},
			Queue:          &config.Queue{Capacity: 100, MaxBatch: 100, FlushInterval: config.Duration(5 * time.Second)},
		},
		Credentials: &runtime.OutputCredentials{
			Headers:  map[string]secret.Value{"X-Scope-OrgID": secret.New("tenant")},
			Username: secret.New("tapyr"), Password: secret.New("s3cr3t"),
		},
		Clock: clk, UserAgent: "tapyr/1.2.3",
	})
}

func labels(kv ...string) model.Labels {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return model.LabelsFromMap(m)
}

func TestSend(t *testing.T) {
	s := newServer(t)
	clk := clock.NewFake(start)
	o := newOutput(t, s.srv.URL, clk)
	t1, t2 := start.Add(-time.Minute), start
	o.Enqueue(model.Batch{Instance: "devices", At: t2, Samples: []model.Sample{
		{Name: "device_up", Type: model.Gauge, Help: "Device status.", Labels: labels("serial", "B", "site", "north"), Value: 1, Timestamp: t2},
		{Name: "device_up", Type: model.Gauge, Help: "Device status.", Labels: labels("serial", "A"), Value: 0, Timestamp: t2},
		{Name: "device_up", Type: model.Gauge, Help: "Device status.", Labels: labels("serial", "A"), Value: 1, Timestamp: t1},
		{Name: "bytes_total", Type: model.Counter, Unit: "bytes", Labels: labels("serial", "A"), Value: 42, Timestamp: t2},
	}})
	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	if err := o.Flush(short); err == nil {
		t.Fatal("Flush without a sender loop succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()
	if err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A stale series and a retired instance: markers (gauge unknown after
	// the first, counter remembered).
	o.Enqueue(model.Batch{Instance: "devices", At: start.Add(time.Minute), Stale: []model.Series{{Name: "device_up", Labels: labels("serial", "B", "site", "north")}}})
	o.Retire("devices", []model.Series{{Name: "bytes_total", Labels: labels("serial", "A")}})
	if err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ms := start.UnixMilli()
	want := []decoded{
		{metadata: []string{"1 bytes_total  bytes", "2 device_up Device status. "}},
		{series: []string{
			fmt.Sprintf("__name__=bytes_total,serial=A,site=external,tapyr_instance=lab 42@%d", ms),
			fmt.Sprintf("__name__=device_up,serial=A,site=external,tapyr_instance=lab 1@%d 0@%d", ms-60000, ms),
			fmt.Sprintf("__name__=device_up,serial=B,site=north,tapyr_instance=lab 1@%d", ms),
		}},
		{series: []string{
			fmt.Sprintf("__name__=bytes_total,serial=A,site=external,tapyr_instance=lab STALE@%d", ms),
			fmt.Sprintf("__name__=device_up,serial=B,site=north,tapyr_instance=lab STALE@%d", ms+60000),
		}},
	}
	if diff := cmp.Diff(want, s.requests, cmp.AllowUnexported(decoded{})); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}
	h := s.headers[1]
	user, pass, _ := (&http.Request{Header: h}).BasicAuth()
	if h.Get("Content-Encoding") != "snappy" || h.Get("Content-Type") != "application/x-protobuf" ||
		h.Get("X-Prometheus-Remote-Write-Version") != "0.1.0" || h.Get("User-Agent") != "tapyr/1.2.3" ||
		h.Get("X-Scope-OrgID") != "tenant" || user != "tapyr" || pass != "s3cr3t" {
		t.Errorf("headers = %v", h)
	}
	if st := o.State(); st.Sent != 6 || st.Queue != 0 {
		t.Errorf("state = %+v", st)
	}
	if o.Name() != "vm" {
		t.Error("name")
	}
}

func TestMetadataInterval(t *testing.T) {
	s := newServer(t)
	clk := clock.NewFake(start)
	o := newOutput(t, s.srv.URL, clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()
	send := func() {
		o.Enqueue(model.Batch{Samples: []model.Sample{{Name: "up", Type: model.Gauge, Value: 1, Timestamp: clk.Now()}}})
		if err := o.Flush(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	send()
	clk.Advance(30 * time.Second)
	send()
	clk.Advance(30 * time.Second)
	send()
	s.mu.Lock()
	defer s.mu.Unlock()
	var kinds []string
	for _, r := range s.requests {
		if r.metadata != nil {
			kinds = append(kinds, "meta")
		} else {
			kinds = append(kinds, "data")
		}
	}
	if !cmp.Equal(kinds, []string{"meta", "data", "data", "meta", "data"}) {
		t.Errorf("requests = %v", kinds)
	}
}

func TestSendErrors(t *testing.T) {
	s := newServer(t)
	clk := clock.NewFake(start)
	o := newOutput(t, s.srv.URL, clk)
	points := []push.Point{{Sample: model.Sample{Name: "up", Value: 1, Timestamp: start}}}
	o.metaSent = start // no metadata request
	for status, want := range map[int]string{
		http.StatusTooManyRequests:     push.ClassThrottled,
		http.StatusServiceUnavailable:  push.ClassTransient,
		http.StatusBadRequest:          push.ClassOutOfOrder,
		http.StatusUnauthorized:        push.ClassRejected,
		http.StatusInternalServerError: push.ClassTransient,
	} {
		s.mu.Lock()
		s.statuses = []int{status}
		s.mu.Unlock()
		_, err := o.Send(context.Background(), points)
		if e, ok := errors.AsType[*push.Error](err); !ok || e.Class != want || e.Status != status {
			t.Errorf("%d: %v", status, err)
		}
	}
	s.srv.Close()
	_, err := o.Send(context.Background(), points)
	if e, ok := errors.AsType[*push.Error](err); !ok || e.Class != push.ClassTransient || strings.Contains(err.Error(), s.srv.URL) {
		t.Errorf("closed server: %v", err)
	}
}
