package mapping

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// functions is the library of custom CEL functions (see the table in
// 06-mapping-and-outputs.md).
type functions struct{}

// LibraryName implements cel.SingletonLibrary.
func (functions) LibraryName() string { return "tapyr" }

// ProgramOptions implements cel.Library.
func (functions) ProgramOptions() []cel.ProgramOption { return nil }

// CompileOptions implements cel.Library.
func (functions) CompileOptions() []cel.EnvOption {
	str := func(f func(string) (ref.Val, error)) cel.OverloadOpt {
		return cel.UnaryBinding(func(v ref.Val) ref.Val {
			s, ok := v.(types.String)
			if !ok {
				return types.NewErr("expected a string, got %s", v.Type().TypeName())
			}
			out, err := f(string(s))
			if err != nil {
				return types.WrapErr(err)
			}
			return out
		})
	}
	return []cel.EnvOption{
		cel.Function("default",
			cel.Overload("default_dyn_dyn", []*cel.Type{cel.DynType, cel.DynType}, cel.DynType,
				cel.OverloadIsNonStrict(),
				cel.BinaryBinding(defaultValue))),
		cel.Function("toFloat",
			cel.Overload("toFloat_dyn", []*cel.Type{cel.DynType}, cel.DoubleType,
				cel.UnaryBinding(toFloat))),
		cel.Function("parseTimestamp",
			cel.Overload("parseTimestamp_string", []*cel.Type{cel.StringType}, cel.TimestampType,
				str(func(s string) (ref.Val, error) { return parseTimestamp(s, time.RFC3339Nano) })),
			cel.Overload("parseTimestamp_string_string", []*cel.Type{cel.StringType, cel.StringType}, cel.TimestampType,
				cel.BinaryBinding(func(s, layout ref.Val) ref.Val {
					ss, ok1 := s.(types.String)
					ls, ok2 := layout.(types.String)
					if !ok1 || !ok2 {
						return types.NewErr("parseTimestamp expects strings")
					}
					out, err := parseTimestamp(string(ss), string(ls))
					if err != nil {
						return types.WrapErr(err)
					}
					return out
				}))),
		cel.Function("fromUnix",
			cel.Overload("fromUnix_dyn", []*cel.Type{cel.DynType}, cel.TimestampType,
				cel.UnaryBinding(func(v ref.Val) ref.Val { return fromEpoch(v, time.Second) }))),
		cel.Function("fromUnixMillis",
			cel.Overload("fromUnixMillis_dyn", []*cel.Type{cel.DynType}, cel.TimestampType,
				cel.UnaryBinding(func(v ref.Val) ref.Val { return fromEpoch(v, time.Millisecond) }))),
		cel.Function("parseDuration",
			cel.Overload("parseDuration_string", []*cel.Type{cel.StringType}, cel.DoubleType,
				str(func(s string) (ref.Val, error) {
					d, err := parseHumanDuration(s)
					return types.Double(d), err
				}))),
		cel.Function("parseBytes",
			cel.Overload("parseBytes_string", []*cel.Type{cel.StringType}, cel.DoubleType,
				str(func(s string) (ref.Val, error) {
					b, err := parseBytes(s)
					return types.Double(b), err
				}))),
		cel.Function("macToString",
			cel.Overload("macToString_string", []*cel.Type{cel.StringType}, cel.StringType,
				str(func(s string) (ref.Val, error) {
					m, err := normalizeMAC(s)
					return types.String(m), err
				}))),
		cel.Function("lower",
			cel.MemberOverload("string_lower", []*cel.Type{cel.StringType}, cel.StringType,
				str(func(s string) (ref.Val, error) { return types.String(strings.ToLower(s)), nil }))),
		cel.Function("upper",
			cel.MemberOverload("string_upper", []*cel.Type{cel.StringType}, cel.StringType,
				str(func(s string) (ref.Val, error) { return types.String(strings.ToUpper(s)), nil }))),
	}
}

// defaultValue returns fallback when v is an error (for example a missing
// field), null or an empty optional. The overload is non-strict so errors
// reach it.
func defaultValue(v, fallback ref.Val) ref.Val {
	switch {
	case types.IsUnknownOrError(v), v.Type() == types.NullType:
		return fallback
	}
	if opt, ok := v.(*types.Optional); ok {
		if !opt.HasValue() {
			return fallback
		}
		return opt.GetValue()
	}
	return v
}

func toFloat(v ref.Val) ref.Val {
	switch v := v.(type) {
	case types.Double:
		return v
	case types.Int:
		return types.Double(v)
	case types.Uint:
		return types.Double(v)
	case types.String:
		f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
		if err != nil {
			return types.NewErr("toFloat: %q is not a number", string(v))
		}
		return types.Double(f)
	}
	return types.NewErr("toFloat: cannot convert %s to a number", v.Type().TypeName())
}

func parseTimestamp(s, layout string) (ref.Val, error) {
	t, err := time.Parse(layout, strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("parseTimestamp: invalid timestamp %q", s)
	}
	return types.Timestamp{Time: t.UTC()}, nil
}

func fromEpoch(v ref.Val, unit time.Duration) ref.Val {
	var f float64
	switch n := v.(type) {
	case types.Int:
		return types.Timestamp{Time: time.Unix(0, 0).Add(time.Duration(n) * unit).UTC()}
	case types.Uint:
		f = float64(n)
	case types.Double:
		f = float64(n)
	default:
		return types.NewErr("expected an epoch number, got %s", v.Type().TypeName())
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return types.NewErr("invalid epoch value")
	}
	return types.Timestamp{Time: time.Unix(0, 0).Add(time.Duration(f * float64(unit))).UTC()}
}

var (
	clockRe    = regexp.MustCompile(`^(?:(\d+)[:])?(\d+):(\d{1,2})(?:\.(\d+))?$`)
	durationRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(ns|us|µs|ms|s|m|h|d|w)`)
)

// parseHumanDuration parses "3d 4h", "1h30m", "01:02:03", "02:03" or a
// number of seconds, and returns seconds.
func parseHumanDuration(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	if m := clockRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.ParseFloat(m[1]+"0", 64)
		mi, _ := strconv.ParseFloat(m[2], 64)
		sec, _ := strconv.ParseFloat(m[3], 64)
		if m[1] == "" {
			// mm:ss
			return mi*60 + sec + fraction(m[4]), nil
		}
		return h/10*3600 + mi*60 + sec + fraction(m[4]), nil
	}
	units := map[string]float64{
		"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3, "s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800,
	}
	matches := durationRe.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("parseDuration: invalid duration %q", s)
	}
	var total float64
	rest := s
	for _, m := range matches {
		n, _ := strconv.ParseFloat(s[m[2]:m[3]], 64)
		total += n * units[s[m[4]:m[5]]]
		rest = strings.Replace(rest, s[m[0]:m[1]], "", 1)
	}
	if strings.Trim(rest, " ,") != "" {
		return 0, fmt.Errorf("parseDuration: invalid duration %q", s)
	}
	return total, nil
}

func fraction(digits string) float64 {
	if digits == "" {
		return 0
	}
	f, _ := strconv.ParseFloat("0."+digits, 64)
	return f
}

var bytesRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([a-zA-Z]*)$`)

// parseBytes parses sizes such as "1.5 GB" (SI, powers of 1000), "512 KiB"
// (IEC, powers of 1024) or "2048".
func parseBytes(s string) (float64, error) {
	m := bytesRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("parseBytes: invalid size %q", s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	unit := strings.ToLower(m[2])
	mult := map[string]float64{
		"": 1, "b": 1,
		"k": 1e3, "kb": 1e3, "m": 1e6, "mb": 1e6, "g": 1e9, "gb": 1e9, "t": 1e12, "tb": 1e12, "p": 1e15, "pb": 1e15,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "pib": 1 << 50,
	}
	f, ok := mult[unit]
	if !ok {
		return 0, fmt.Errorf("parseBytes: unknown unit %q in %q", m[2], s)
	}
	return n * f, nil
}

// normalizeMAC returns a MAC address as lowercase colon-separated hex
// ("aa:bb:cc:dd:ee:ff"), from colon, hyphen, Cisco dot or bare formats.
func normalizeMAC(s string) (string, error) {
	hex := strings.Map(func(r rune) rune {
		switch r {
		case ':', '-', '.':
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(s))
	if len(hex) != 12 || strings.IndexFunc(hex, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) >= 0 {
		return "", fmt.Errorf("macToString: invalid MAC address %q", s)
	}
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = hex[2*i : 2*i+2]
	}
	return strings.Join(parts, ":"), nil
}
