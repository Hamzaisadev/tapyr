package model

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLabelsFromMap(t *testing.T) {
	ls := LabelsFromMap(map[string]string{"zone": "eu", "app": "tapyr", "empty": ""})
	want := Labels{{Name: "app", Value: "tapyr"}, {Name: "zone", Value: "eu"}}
	if diff := cmp.Diff(want, ls); diff != "" {
		t.Fatalf("labels (-want +got):\n%s", diff)
	}
	if ls.Get("zone") != "eu" || ls.Get("empty") != "" || ls.Get("missing") != "" {
		t.Errorf("Get() returned unexpected values")
	}
}

func TestSeriesKey(t *testing.T) {
	a := Sample{Name: "m", Labels: Labels{{"a", "1"}, {"b", "2"}}}
	b := Sample{Name: "m", Labels: Labels{{"a", "1"}, {"b", "2"}}}
	c := Sample{Name: "m", Labels: Labels{{"a", "12"}}}
	d := Sample{Name: "m", Labels: Labels{{"a", "1"}, {"b", "2"}, {"c", "3"}}}
	if a.SeriesKey() != b.SeriesKey() {
		t.Error("equal series have different keys")
	}
	if a.SeriesKey() == c.SeriesKey() || a.SeriesKey() == d.SeriesKey() {
		t.Error("different series share a key")
	}
	if Gauge.String() != "gauge" || Counter.String() != "counter" || MetricType(0).String() != "unknown" {
		t.Error("unexpected MetricType strings")
	}
}

func TestSeriesKeyRoundTrip(t *testing.T) {
	for _, s := range []Series{
		{Name: "up"},
		{Name: "device_up", Labels: LabelsFromMap(map[string]string{"serial": "SIM-1", "site": "a=b,c"})},
	} {
		got, err := ParseSeriesKey(s.Key())
		if err != nil || got.Key() != s.Key() || got.Name != s.Name || len(got.Labels) != len(s.Labels) {
			t.Errorf("ParseSeriesKey(%q) = %+v, %v", s.Key(), got, err)
		}
	}
	for _, bad := range []string{"no-separator", "m\x00name-without-value\xfe", "m\x00n\xffv"} {
		if _, err := ParseSeriesKey(bad); err == nil {
			t.Errorf("ParseSeriesKey(%q) succeeded", bad)
		}
	}
}
