package budget

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

// limit is a compiled provider limit.
type limit struct {
	provider string
	name     string
	id       string // provider/name
	kind     config.LimitKind

	// rate
	rate  float64 // tokens per second
	burst float64

	// concurrency and quota
	max        int64
	override   int64 // simulator scenarios
	overridden bool
	auto       bool
	initialMax int64
	fixed      *fixedWindow
	rolling    time.Duration
	sync       *config.QuotaSync

	scope           []string
	endpoints       []string
	methods         []string
	requestsWithVar string
	authRequests    string
}

func compileLimit(provider string, l *config.Limit) (*limit, error) {
	out := &limit{
		provider: provider, name: string(l.Name), id: provider + "/" + string(l.Name), kind: l.Kind,
		initialMax: l.InitialMax, sync: l.Sync, scope: []string{"instance"}, authRequests: "include",
	}
	if l.Scope != nil && len(l.Scope.By) > 0 {
		out.scope = l.Scope.By
	}
	if a := l.AppliesTo; a != nil {
		out.endpoints, out.methods, out.requestsWithVar = a.Endpoints, a.Methods, string(a.RequestsWithVar)
		if a.AuthRequests != "" {
			out.authRequests = a.AuthRequests
		}
	}
	switch l.Kind {
	case config.LimitRate:
		out.rate = l.Rate.PerSecond()
		out.burst = float64(l.Burst)
		if out.burst < 1 {
			out.burst = math.Max(1, math.Ceil(out.rate))
		}
	case config.LimitConcurrency:
		out.max = l.Max.Value
	case config.LimitQuota:
		out.max, out.auto = l.Max.Value, l.Max.Auto
		if l.Window.Rolling != 0 {
			out.rolling = l.Window.Rolling.Std()
		} else {
			fw, err := newFixedWindow(l.Window)
			if err != nil {
				return nil, fmt.Errorf("limit %s: %w", out.id, err)
			}
			out.fixed = &fw
		}
	}
	return out, nil
}

// applies reports whether the limit counts the request.
func (l *limit) applies(d *Descriptor) bool {
	switch {
	case l.authRequests == "only" && !d.AuthRequest, l.authRequests == "exclude" && d.AuthRequest:
		return false
	case len(l.methods) > 0 && !slices.Contains(l.methods, d.Method):
		return false
	case len(l.endpoints) > 0 && !slices.ContainsFunc(l.endpoints, func(p string) bool { return globMatch(p, d.Endpoint) }):
		return false
	case l.requestsWithVar != "" && d.Vars[l.requestsWithVar] == "":
		return false
	}
	for _, dim := range l.scope {
		if name, ok := strings.CutPrefix(dim, "var:"); ok && d.Vars[name] == "" {
			return false
		}
	}
	return true
}

// scopeKey returns the concrete scope of the request for this limit. It
// never contains secrets: credentials appear as their keyed hash.
func (l *limit) scopeKey(d *Descriptor) string {
	parts := make([]string, 0, len(l.scope))
	for _, dim := range l.scope {
		switch dim {
		case "instance":
			parts = append(parts, "instance")
		case "provider":
			parts = append(parts, "provider="+d.Provider)
		case "account":
			parts = append(parts, "account="+d.Account)
		case "target":
			parts = append(parts, "target="+d.Target)
		case "credential":
			parts = append(parts, "credential="+d.Credential)
		case "endpoint":
			parts = append(parts, "endpoint="+d.Endpoint)
		default:
			name := strings.TrimPrefix(dim, "var:")
			parts = append(parts, name+"="+d.Vars[name])
		}
	}
	return strings.Join(parts, ",")
}

// narrowness ranks scopes for 429 penalties: more dimensions is narrower;
// instance and provider count as none.
func (l *limit) narrowness() int {
	n := 0
	for _, dim := range l.scope {
		if dim != "instance" && dim != "provider" {
			n++
		}
	}
	return n
}

// GlobMatch matches a templated path segment by segment: * matches within
// one segment, ** matches zero or more whole segments.
func GlobMatch(pattern, path string) bool { return globMatch(pattern, path) }

func globMatch(pattern, path string) bool {
	return matchSegments(strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 || !matchSegment(pat[0], segs[0]) {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// matchSegment matches one segment with * wildcards.
func matchSegment(pattern, seg string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == seg
	}
	if !strings.HasPrefix(seg, parts[0]) {
		return false
	}
	seg = seg[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(seg, p)
		if i < 0 {
			return false
		}
		seg = seg[i+len(p):]
	}
	return strings.HasSuffix(seg, parts[len(parts)-1])
}
