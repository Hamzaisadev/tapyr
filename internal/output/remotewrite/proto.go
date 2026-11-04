package remotewrite

import (
	"math"

	"google.golang.org/protobuf/encoding/protowire"
)

// The Prometheus remote write 1.0 messages, encoded by hand (prompb's
// remote.proto and types.proto):
//
//	WriteRequest   { repeated TimeSeries timeseries = 1; repeated MetricMetadata metadata = 3; }
//	TimeSeries     { repeated Label labels = 1; repeated Sample samples = 2; }
//	Label          { string name = 1; string value = 2; }
//	Sample         { double value = 1; int64 timestamp = 2; }
//	MetricMetadata { MetricType type = 1; string metric_family_name = 2; string help = 4; string unit = 5; }

// staleNaN is the Prometheus staleness marker.
var staleNaN = math.Float64frombits(0x7ff0000000000002)

// MetricMetadata types.
const (
	metadataCounter = 1
	metadataGauge   = 2
)

type label struct{ name, value string }

type sample struct {
	value float64
	ts    int64 // milliseconds
}

type timeSeries struct {
	labels  []label
	samples []sample
}

type metadata struct {
	typ        int
	name       string
	help, unit string
}

func appendWriteRequest(b []byte, series []timeSeries, meta []metadata) []byte {
	for i := range series {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, appendTimeSeries(nil, &series[i]))
	}
	for i := range meta {
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendBytes(b, appendMetadata(nil, &meta[i]))
	}
	return b
}

func appendTimeSeries(b []byte, ts *timeSeries) []byte {
	for _, l := range ts.labels {
		var lb []byte
		lb = appendString(lb, 1, l.name)
		lb = appendString(lb, 2, l.value)
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, lb)
	}
	for _, s := range ts.samples {
		var sb []byte
		sb = protowire.AppendTag(sb, 1, protowire.Fixed64Type)
		sb = protowire.AppendFixed64(sb, math.Float64bits(s.value))
		if s.ts != 0 {
			sb = protowire.AppendTag(sb, 2, protowire.VarintType)
			sb = protowire.AppendVarint(sb, uint64(s.ts)) //nolint:gosec // G115: int64 is encoded as its two's complement varint.
		}
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendBytes(b, sb)
	}
	return b
}

func appendMetadata(b []byte, m *metadata) []byte {
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(m.typ)) //nolint:gosec // G115: small enum value.
	b = appendString(b, 2, m.name)
	if m.help != "" {
		b = appendString(b, 4, m.help)
	}
	if m.unit != "" {
		b = appendString(b, 5, m.unit)
	}
	return b
}

func appendString(b []byte, field protowire.Number, s string) []byte {
	b = protowire.AppendTag(b, field, protowire.BytesType)
	return protowire.AppendString(b, s)
}
