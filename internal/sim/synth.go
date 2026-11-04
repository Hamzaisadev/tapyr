package sim

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"net/url"
	"path"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/mapping"
)

// defaultInventorySeed seeds an inventory that declares no seed.
const defaultInventorySeed = 42

// counterEpoch is the origin of the counter helper: counters grow from it,
// so that their values depend only on the simulated time.
var counterEpoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// inventory is a synthetic inventory: items by set, in generation order.
// An item is a map with name, index, n, key and parent, so that templates
// read .key or .parent.key.
type inventory struct {
	seed uint64
	sets map[string][]any
}

// newInventory generates the items of a Simulation inventory.
func newInventory(inv *config.SimulationInventory) (*inventory, error) {
	out := &inventory{seed: defaultInventorySeed, sets: map[string][]any{}}
	if inv == nil {
		return out, nil
	}
	if inv.Seed != 0 {
		out.seed = inv.Seed
	}
	total := 0
	for _, set := range inv.Sets {
		name := string(set.Name)
		if _, dup := out.sets[name]; dup {
			return nil, fmt.Errorf("inventory: set %s is declared twice", name)
		}
		parents := []any{nil}
		if set.Parent != "" {
			p, ok := out.sets[string(set.Parent)]
			if !ok {
				return nil, fmt.Errorf("inventory: set %s: parent %s is not declared before it", name, set.Parent)
			}
			parents = p
		}
		items := make([]any, 0, len(parents)*set.Count)
		for _, parent := range parents {
			for n := range set.Count {
				index := len(items)
				items = append(items, map[string]any{
					"name": name, "index": index, "n": n, "key": name + "-" + strconv.Itoa(index), "parent": parent,
				})
			}
		}
		if total += len(items); total > 1_000_000 {
			return nil, errors.New("inventory: more than 1000000 items")
		}
		out.sets[name] = items
	}
	return out, nil
}

// hash returns a deterministic 64-bit value of the seed and parts.
func (inv *inventory) hash(parts ...string) uint64 {
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], inv.seed)
	_, _ = h.Write(b[:])
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	// FNV mixes the last bytes poorly: finish with a splitmix64 step.
	x := h.Sum64()
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// unit returns a deterministic value in [0, 1).
func (inv *inventory) unit(parts ...string) float64 {
	return float64(inv.hash(parts...)>>11) / (1 << 53)
}

// keyOf returns the key of an item: an inventory item, or any value used
// as a key (a string or a number from a route variable).
func keyOf(item any) string {
	if m, ok := item.(map[string]any); ok {
		if k, ok := m["key"].(string); ok {
			return k
		}
	}
	return fmt.Sprint(item)
}

// periodOf returns the number of the period of d containing now.
func periodOf(now time.Time, d string) (int64, error) {
	p, err := time.ParseDuration(d)
	if err != nil || p <= 0 {
		return 0, fmt.Errorf("invalid period %q", d)
	}
	return now.Sub(counterEpoch).Nanoseconds() / p.Nanoseconds(), nil
}

// toFloat converts a template number.
func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case float64:
		return n, nil
	case json.Number:
		return n.Float64()
	case string:
		return strconv.ParseFloat(n, 64)
	}
	return 0, fmt.Errorf("%v (%T) is not a number", v, v)
}

func isInt(v any) bool {
	switch v.(type) {
	case int, int64:
		return true
	}
	return false
}

// arith applies op to two template numbers; the result is an int64 when
// both are integers and the result is integral.
func arith(op func(a, b float64) float64) func(a, b any) (any, error) {
	return func(a, b any) (any, error) {
		x, err := toFloat(a)
		if err != nil {
			return nil, err
		}
		y, err := toFloat(b)
		if err != nil {
			return nil, err
		}
		r := op(x, y)
		if isInt(a) && isInt(b) && r == math.Trunc(r) && math.Abs(r) < 1<<53 {
			return int64(r), nil
		}
		return r, nil
	}
}

// funcs returns the template helpers. now is the simulated time of the
// request being rendered.
func (inv *inventory) funcs(now func() time.Time) template.FuncMap {
	return template.FuncMap{
		"items": func(set string) ([]any, error) {
			items, ok := inv.sets[set]
			if !ok {
				return nil, fmt.Errorf("no inventory set %q", set)
			}
			return items, nil
		},
		"children": func(set string, parent any) ([]any, error) {
			items, ok := inv.sets[set]
			if !ok {
				return nil, fmt.Errorf("no inventory set %q", set)
			}
			key := keyOf(parent)
			var out []any
			for _, it := range items {
				if p := it.(map[string]any)["parent"]; p != nil && keyOf(p) == key {
					out = append(out, it)
				}
			}
			return out, nil
		},
		"find": func(set, key string) (any, error) {
			for _, it := range inv.sets[set] {
				if keyOf(it) == key {
					return it, nil
				}
			}
			return nil, fmt.Errorf("no item %q in set %q", key, set)
		},
		"where": func(list []any, field string, value any) []any {
			var out []any
			for _, it := range list {
				if m, ok := it.(map[string]any); ok && fmt.Sprint(m[field]) == fmt.Sprint(value) {
					out = append(out, it)
				}
			}
			return out
		},
		"id": func(item any, prefix string) string {
			const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
			h := inv.hash("id", keyOf(item))
			var b strings.Builder
			b.WriteString(prefix)
			for range 10 {
				b.WriteByte(alphabet[h%36])
				h /= 36
			}
			return b.String()
		},
		"num": func(item any, field string, lo, hi int) int {
			if hi <= lo {
				return lo
			}
			return lo + int(inv.hash("num", keyOf(item), field)%uint64(hi-lo+1)) //nolint:gosec // G115: hi-lo is positive.
		},
		"serial": func(item any, pattern string) string {
			const letters, digits = "ABCDEFGHJKLMNPQRSTUVWXYZ0123456789", "0123456789"
			var b strings.Builder
			for i, c := range pattern {
				h := inv.hash("serial", keyOf(item), strconv.Itoa(i))
				switch c {
				case 'X':
					b.WriteByte(letters[h%uint64(len(letters))])
				case '9':
					b.WriteByte(digits[h%10])
				default:
					b.WriteRune(c)
				}
			}
			return b.String()
		},
		"mac": func(item any) string {
			h := inv.hash("mac", keyOf(item))
			return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", h&0xff, h>>8&0xff, h>>16&0xff, h>>24&0xff, h>>32&0xff)
		},
		"ipv4": func(item any) string {
			h := inv.hash("ipv4", keyOf(item))
			return fmt.Sprintf("10.%d.%d.%d", h&0xff, h>>8&0xff, 1+(h>>16&0xff)%254)
		},
		"pick": func(item any, field string, values ...any) (any, error) {
			if len(values) == 0 {
				return nil, errors.New("pick needs values")
			}
			return values[inv.hash("pick", keyOf(item), field)%uint64(len(values))], nil
		},
		"rand": func(item any, field string, lo, hi any) (float64, error) {
			a, err := toFloat(lo)
			if err != nil {
				return 0, err
			}
			b, err := toFloat(hi)
			if err != nil {
				return 0, err
			}
			return round(a + (b-a)*inv.unit("rand", keyOf(item), field)), nil
		},
		"walk": func(item any, field string, lo, hi any, period string) (float64, error) {
			a, err := toFloat(lo)
			if err != nil {
				return 0, err
			}
			b, err := toFloat(hi)
			if err != nil {
				return 0, err
			}
			k, err := periodOf(now(), period)
			if err != nil {
				return 0, err
			}
			return round(a + (b-a)*inv.unit("walk", keyOf(item), field, strconv.FormatInt(k, 10))), nil
		},
		"flap": func(item any, field, period string, states ...string) (string, error) {
			k, err := periodOf(now(), period)
			if err != nil {
				return "", err
			}
			names, weights := make([]string, len(states)), make([]float64, len(states))
			var total float64
			for i, st := range states {
				name, w, ok := strings.Cut(st, ":")
				f, err := strconv.ParseFloat(w, 64)
				if !ok || err != nil || f < 0 {
					return "", fmt.Errorf("flap state %q is not name:weight", st)
				}
				names[i], weights[i] = name, f
				total += f
			}
			if total <= 0 {
				return "", errors.New("flap needs a positive weight")
			}
			u := inv.unit("flap", keyOf(item), field, strconv.FormatInt(k, 10)) * total
			for i, w := range weights {
				if u < w {
					return names[i], nil
				}
				u -= w
			}
			return names[len(names)-1], nil
		},
		"counter": func(item any, field string, rate any) (int64, error) {
			r, err := toFloat(rate)
			if err != nil {
				return 0, err
			}
			// Each item counts at its own rate, between half and one and a
			// half times the given one.
			factor := 0.5 + inv.unit("counter", keyOf(item), field)
			return int64(r * factor * now().Sub(counterEpoch).Seconds()), nil
		},
		"json": func(v any) (string, error) {
			data, err := json.Marshal(v)
			return string(data), err
		},
		"ts":   func(layout string, t time.Time) string { return t.UTC().Format(layout) },
		"unix": func(t time.Time) int64 { return t.Unix() },
		"ago": func(d string) (time.Time, error) {
			p, err := time.ParseDuration(d)
			if err != nil {
				return time.Time{}, err
			}
			return now().Add(-p), nil
		},
		"add": arith(func(a, b float64) float64 { return a + b }),
		"sub": arith(func(a, b float64) float64 { return a - b }),
		"mul": arith(func(a, b float64) float64 { return a * b }),
		"div": arith(func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		}),
		"mod": func(a, b any) (int64, error) {
			x, err := toFloat(a)
			if err != nil {
				return 0, err
			}
			y, err := toFloat(b)
			if err != nil || y == 0 {
				return 0, err
			}
			return int64(math.Mod(x, y)), nil
		},
		"seq": func(n int) []int {
			out := make([]int, max(n, 0))
			for i := range out {
				out[i] = i
			}
			return out
		},
	}
}

// round keeps 3 decimals, so that rendered values stay short.
func round(f float64) float64 { return math.Round(f*1000) / 1000 }

// parsed returns a template fixture, parsed once. Its helpers read the
// simulated time when they run, during the request.
func (s *Server) parsed(file string) (*template.Template, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.templates[file]; ok {
		return t, nil
	}
	data, err := fs.ReadFile(s.pkg.FS, path.Join("sim", file))
	if err != nil {
		return nil, fmt.Errorf("fixture %s: %w", file, unwrapPathError(err))
	}
	t, err := template.New(file).Option("missingkey=error").Funcs(s.inv.funcs(s.clock.Now)).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("fixture %s: %w", file, err)
	}
	s.templates[file] = t
	return t, nil
}

// render executes a template fixture and decodes its JSON.
func (s *Server) render(file string, vars map[string]string, query url.Values) (any, error) {
	t, err := s.parsed(file)
	if err != nil {
		return nil, err
	}
	q := make(map[string]string, len(query))
	for k := range query {
		q[k] = query.Get(k)
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]any{"vars": vars, "query": q, "now": s.clock.Now().UTC(), "inventory": s.inv.sets})
	if err != nil {
		return nil, fmt.Errorf("fixture %s: %w", file, err)
	}
	v, err := mapping.DecodeJSON(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("fixture %s renders invalid JSON: %w", file, err)
	}
	return v, nil
}

// splitField splits a dotted itemsField.
func splitField(field string) []string {
	if field == "" {
		return nil
	}
	return strings.Split(field, ".")
}

// listAt returns the list at a dotted path of a document.
func listAt(doc any, field []string) ([]any, bool) {
	for _, k := range field {
		m, ok := doc.(map[string]any)
		if !ok {
			return nil, false
		}
		doc = m[k]
	}
	list, ok := doc.([]any)
	return list, ok
}

// withList returns a copy of a document with the list at a dotted path
// replaced; the maps on the path are copied, the document is not changed.
func withList(doc any, field []string, list []any) any {
	if len(field) == 0 {
		return list
	}
	m := doc.(map[string]any)
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	out[field[0]] = withList(m[field[0]], field[1:], list)
	return out
}
