package prometheus

import (
	"compress/gzip"
	"context"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/model"
)

var update = flag.Bool("update", false, "rewrite golden files")

var at = time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)

func labels(kv ...string) model.Labels {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return model.LabelsFromMap(m)
}

func fill(o *Output) {
	o.Enqueue(model.Batch{Instance: "devices", Samples: []model.Sample{
		{Name: "example_device_up", Type: model.Gauge, Help: "Device status", Labels: labels("serial", "B"), Value: 0, Timestamp: at},
		{Name: "example_device_up", Type: model.Gauge, Help: "Device status", Labels: labels("serial", "A"), Value: 1, Timestamp: at},
		{
			Name: "example_device_received_bytes_total", Type: model.Counter, Help: "Bytes received", Unit: "bytes",
			Labels: labels("serial", "A"), Value: 1048576, Timestamp: at.Add(-time.Hour),
		},
		{Name: "example_device_clients", Type: model.Gauge, Unit: "clients_wrong", Labels: labels("serial", "A"), Value: 12, Timestamp: at},
		{Name: "example_device_rssi_dbm", Type: model.Gauge, Unit: "dBm", Labels: labels("serial", "A"), Value: -70, Timestamp: at},
		{Name: "example_device_info", Type: model.Gauge, Labels: labels("serial", "A", "model", `SX "100"`), Value: 1, Timestamp: at},
	}})
	// Another instance producing an already exposed series: the first
	// instance in name order wins.
	o.Enqueue(model.Batch{Instance: "duplicate", Samples: []model.Sample{
		{Name: "example_device_up", Type: model.Gauge, Labels: labels("serial", "A"), Value: 42, Timestamp: at},
		{Name: "example_device_up", Type: model.Gauge, Labels: labels("serial", "C"), Value: 1, Timestamp: at},
	}})
}

func scrape(t *testing.T, o *Output, accept string) (string, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	o.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	return rec.Header().Get("Content-Type"), string(body)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file (run go test -update): %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s (-want +got):\n%s", name, diff)
	}
}

func TestExposition(t *testing.T) {
	tests := []struct {
		name       string
		accept     string
		timestamps bool
		golden     string
		wantType   string
	}{
		{name: "text", golden: "text.golden", wantType: "text/plain; version=0.0.4"},
		{name: "text with timestamps", timestamps: true, golden: "text-timestamps.golden", wantType: "text/plain"},
		{
			name:   "openmetrics",
			accept: "application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5",
			golden: "openmetrics.golden", wantType: "application/openmetrics-text; version=1.0.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := New("prom", tt.timestamps, nil)
			fill(o)
			contentType, body := scrape(t, o, tt.accept)
			if !strings.HasPrefix(contentType, tt.wantType) {
				t.Errorf("Content-Type = %q, want prefix %q", contentType, tt.wantType)
			}
			golden(t, tt.golden, body)
		})
	}
}

func TestGzip(t *testing.T) {
	o := New("prom", false, nil)
	fill(o)
	_, plain := scrape(t, o, "")
	tests := []struct {
		name           string
		acceptEncoding string
		wantGzip       bool
	}{
		{name: "no header"},
		{name: "gzip", acceptEncoding: "gzip", wantGzip: true},
		{name: "list with quality", acceptEncoding: "br;q=1.0, GZIP;q=0.5, *;q=0.1", wantGzip: true},
		{name: "gzip refused", acceptEncoding: "gzip;q=0, identity"},
		{name: "other codings only", acceptEncoding: "br, deflate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
			if tt.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			}
			rec := httptest.NewRecorder()
			o.Handler().ServeHTTP(rec, req)
			if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
				t.Errorf("Vary = %q", got)
			}
			var body io.Reader = rec.Body
			if got := rec.Header().Get("Content-Encoding"); (got == "gzip") != tt.wantGzip {
				t.Fatalf("Content-Encoding = %q, want gzip %v", got, tt.wantGzip)
			}
			if tt.wantGzip {
				zr, err := gzip.NewReader(rec.Body)
				if err != nil {
					t.Fatal(err)
				}
				body = zr
			}
			got, err := io.ReadAll(body)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(plain, string(got)); diff != "" {
				t.Errorf("exposition (-plain +got):\n%s", diff)
			}
		})
	}
}

func TestEnqueueReplacesInstanceSeries(t *testing.T) {
	o := New("prom", false, nil)
	o.Enqueue(model.Batch{Instance: "devices", Samples: []model.Sample{
		{Name: "up", Type: model.Gauge, Labels: labels("serial", "A"), Value: 1},
		{Name: "up", Type: model.Gauge, Labels: labels("serial", "B"), Value: 1},
	}})
	o.Enqueue(model.Batch{Instance: "devices", Samples: []model.Sample{
		{Name: "up", Type: model.Gauge, Labels: labels("serial", "A"), Value: 0},
	}})
	_, body := scrape(t, o, "")
	if strings.Contains(body, `serial="B"`) || !strings.Contains(body, `up{serial="A"} 0`) {
		t.Fatalf("exposition:\n%s", body)
	}
	o.Retire("devices", nil)
	o.Retire("unknown", nil)
	if _, body := scrape(t, o, ""); body != "" {
		t.Fatalf("exposition after Retire:\n%s", body)
	}
	if o.Name() != "prom" || o.Flush(context.Background()) != nil {
		t.Error("unexpected Name or Flush result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Run(ctx); err != nil {
		t.Errorf("Run() = %v", err)
	}
}

func TestEmptyExposition(t *testing.T) {
	_, body := scrape(t, New("prom", false, nil), "")
	if body != "" {
		t.Fatalf("body = %q", body)
	}
}
