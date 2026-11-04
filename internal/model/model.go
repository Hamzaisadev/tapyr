// Package model is the internal metric model: samples produced by the
// mapping and converted to each output format at the edge (see
// docs/spec/06-mapping-and-outputs.md, "Internal metric model").
package model

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// MetricType is the type of a metric.
type MetricType uint8

// Metric types.
const (
	Gauge MetricType = iota + 1
	Counter
)

// String returns "gauge" or "counter".
func (t MetricType) String() string {
	switch t {
	case Gauge:
		return "gauge"
	case Counter:
		return "counter"
	}
	return "unknown"
}

// Label is a name and value pair.
type Label struct {
	Name  string
	Value string
}

// Labels is a label set sorted by name, without duplicate names or empty
// values.
type Labels []Label

// LabelsFromMap builds a label set, dropping empty values (Prometheus
// semantics).
func LabelsFromMap(m map[string]string) Labels {
	ls := make(Labels, 0, len(m))
	for name, value := range m {
		if value != "" {
			ls = append(ls, Label{Name: name, Value: value})
		}
	}
	slices.SortFunc(ls, func(a, b Label) int { return strings.Compare(a.Name, b.Name) })
	return ls
}

// Get returns the value of a label, or "".
func (ls Labels) Get(name string) string {
	i, found := slices.BinarySearchFunc(ls, name, func(l Label, n string) int { return strings.Compare(l.Name, n) })
	if !found {
		return ""
	}
	return ls[i].Value
}

// Key returns a canonical string identifying the label set.
func (ls Labels) Key() string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Name)
		b.WriteByte(0xff)
		b.WriteString(l.Value)
		b.WriteByte(0xfe)
	}
	return b.String()
}

// Sample is one value of one series.
type Sample struct {
	Name   string
	Type   MetricType
	Help   string
	Unit   string
	Labels Labels
	Value  float64
	// Timestamp is the source timestamp when SourceTimestamp is true, the
	// collection time otherwise.
	Timestamp       time.Time
	SourceTimestamp bool
	// Accumulate marks a window value to sum into a reconstructed counter
	// (accumulate: true); Window identifies its window, so that a window is
	// counted once.
	Accumulate bool
	Window     string
}

// SeriesKey identifies the series of a sample.
func (s *Sample) SeriesKey() string { return s.Series().Key() }

// Series returns the series of a sample.
func (s *Sample) Series() Series { return Series{Name: s.Name, Labels: s.Labels} }

// Series identifies a series: a metric name and a label set.
type Series struct {
	Name   string
	Labels Labels
}

// Key is the identity of the series. Label names and values are valid
// UTF-8, so the 0x00, 0xfe and 0xff separators never appear in them.
func (s Series) Key() string { return s.Name + "\x00" + s.Labels.Key() }

// ParseSeriesKey is the inverse of Series.Key.
func ParseSeriesKey(key string) (Series, error) {
	name, rest, ok := strings.Cut(key, "\x00")
	if !ok {
		return Series{}, fmt.Errorf("invalid series key %q", key)
	}
	s := Series{Name: name}
	for rest != "" {
		pair, tail, ok := strings.Cut(rest, "\xfe")
		if !ok {
			return Series{}, fmt.Errorf("invalid series key %q", key)
		}
		n, v, ok := strings.Cut(pair, "\xff")
		if !ok {
			return Series{}, fmt.Errorf("invalid series key %q", key)
		}
		s.Labels = append(s.Labels, Label{Name: n, Value: v})
		rest = tail
	}
	return s, nil
}

// Batch is the result of one successful run of one job instance, for one
// output.
type Batch struct {
	// Instance identifies the job instance.
	Instance string
	Job      string
	RunID    string
	// Provider and Account of the job instance (OTLP resource).
	Provider, Account string
	// At is the start of the run, the time of the staleness markers.
	At      time.Time
	Samples []Sample
	// Stale are the series of the previous successful run absent from this
	// one: push outputs send a staleness marker for each, the pull output
	// drops them by replacing the series of the instance.
	Stale []Series
}
