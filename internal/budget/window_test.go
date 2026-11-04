package budget

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestFixedWindowBounds(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	ny := mustLoc(t, "America/New_York")
	kathmandu := mustLoc(t, "Asia/Kathmandu")
	tests := []struct {
		name       string
		window     config.Window
		at         time.Time
		start, end time.Time
	}{
		{
			name:   "minute",
			window: config.Window{Period: "minute"},
			at:     time.Date(2026, 11, 2, 12, 34, 56, 7, time.UTC),
			start:  time.Date(2026, 11, 2, 12, 34, 0, 0, time.UTC), end: time.Date(2026, 11, 2, 12, 35, 0, 0, time.UTC),
		},
		{
			name:   "hour in a +05:45 timezone",
			window: config.Window{Period: "hour", Timezone: "Asia/Kathmandu"},
			at:     time.Date(2026, 11, 2, 12, 10, 0, 0, kathmandu),
			start:  time.Date(2026, 11, 2, 12, 0, 0, 0, kathmandu), end: time.Date(2026, 11, 2, 13, 0, 0, 0, kathmandu),
		},
		{
			name:   "day at midnight UTC",
			window: config.Window{Period: "day", ResetAt: "00:00", Timezone: "UTC"},
			at:     time.Date(2026, 11, 2, 23, 59, 59, 0, time.UTC),
			start:  time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), end: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "day before the reset time belongs to the previous window",
			window: config.Window{Period: "day", ResetAt: "08:00", Timezone: "Europe/Paris"},
			at:     time.Date(2026, 11, 2, 7, 59, 0, 0, paris),
			start:  time.Date(2026, 11, 1, 8, 0, 0, 0, paris), end: time.Date(2026, 11, 2, 8, 0, 0, 0, paris),
		},
		{
			name:   "spring forward: 23-hour day",
			window: config.Window{Period: "day", ResetAt: "00:00", Timezone: "Europe/Paris"},
			at:     time.Date(2026, 3, 29, 12, 0, 0, 0, paris),
			start:  time.Date(2026, 3, 29, 0, 0, 0, 0, paris), end: time.Date(2026, 3, 30, 0, 0, 0, 0, paris),
		},
		{
			name:   "fall back: 25-hour day",
			window: config.Window{Period: "day", ResetAt: "00:00", Timezone: "America/New_York"},
			at:     time.Date(2026, 11, 1, 12, 0, 0, 0, ny),
			start:  time.Date(2026, 11, 1, 0, 0, 0, 0, ny), end: time.Date(2026, 11, 2, 0, 0, 0, 0, ny),
		},
		{
			name:   "reset time in the DST gap is normalized forward",
			window: config.Window{Period: "day", ResetAt: "02:30", Timezone: "Europe/Paris"},
			at:     time.Date(2026, 3, 29, 12, 0, 0, 0, paris),
			start:  time.Date(2026, 3, 29, 3, 30, 0, 0, paris), end: time.Date(2026, 3, 30, 2, 30, 0, 0, paris),
		},
		{
			name:   "week starting monday",
			window: config.Window{Period: "week", ResetAt: "00:00", WeekStart: "monday", Timezone: "UTC"},
			at:     time.Date(2026, 11, 8, 23, 0, 0, 0, time.UTC), // a Sunday
			start:  time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), end: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "week before the reset on its first day",
			window: config.Window{Period: "week", ResetAt: "06:00", WeekStart: "sunday", Timezone: "UTC"},
			at:     time.Date(2026, 11, 8, 5, 0, 0, 0, time.UTC), // Sunday 05:00
			start:  time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC), end: time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC),
		},
		{
			name:   "month on the first",
			window: config.Window{Period: "month", ResetAt: "00:00", MonthDay: 1, Timezone: "UTC"},
			at:     time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),
			start:  time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), end: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "monthDay 31 clamps to the end of february (leap year)",
			window: config.Window{Period: "month", ResetAt: "00:00", MonthDay: 31, Timezone: "UTC"},
			at:     time.Date(2028, 3, 15, 0, 0, 0, 0, time.UTC),
			start:  time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC), end: time.Date(2028, 3, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "monthDay 31 in a common year",
			window: config.Window{Period: "month", ResetAt: "00:00", MonthDay: 31, Timezone: "UTC"},
			at:     time.Date(2027, 2, 10, 0, 0, 0, 0, time.UTC),
			start:  time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC), end: time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "month across a DST change",
			window: config.Window{Period: "month", ResetAt: "00:00", MonthDay: 1, Timezone: "Europe/Paris"},
			at:     time.Date(2026, 3, 30, 0, 0, 0, 0, paris),
			start:  time.Date(2026, 3, 1, 0, 0, 0, 0, paris), end: time.Date(2026, 4, 1, 0, 0, 0, 0, paris),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := newFixedWindow(&tt.window)
			if err != nil {
				t.Fatal(err)
			}
			start, end := w.bounds(tt.at)
			if !start.Equal(tt.start) || !end.Equal(tt.end) {
				t.Fatalf("bounds(%v) = [%v, %v), want [%v, %v)", tt.at, start, end, tt.start, tt.end)
			}
		})
	}
}

// TestFixedWindowProperties checks, over random instants, that the window
// contains the instant and that consecutive windows are contiguous.
func TestFixedWindowProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	windows := []config.Window{
		{Period: "minute", Timezone: "Asia/Kathmandu"},
		{Period: "hour", Timezone: "Australia/Lord_Howe"}, // 30-minute DST shift
		{Period: "day", ResetAt: "02:30", Timezone: "Europe/Paris"},
		{Period: "day", ResetAt: "00:00", Timezone: "America/New_York"},
		{Period: "week", ResetAt: "01:00", WeekStart: "wednesday", Timezone: "America/Sao_Paulo"},
		{Period: "month", ResetAt: "00:00", MonthDay: 30, Timezone: "Europe/Paris"},
		{Period: "month", ResetAt: "23:00", MonthDay: 31, Timezone: "UTC"},
	}
	for _, cw := range windows {
		w, err := newFixedWindow(&cw)
		if err != nil {
			t.Fatal(err)
		}
		for range 2000 {
			at := time.Unix(from+r.Int64N(to-from), 0)
			start, end := w.bounds(at)
			if at.Before(start) || !at.Before(end) {
				t.Fatalf("%+v: %v not in [%v, %v)", cw, at, start, end)
			}
			if nextStart, _ := w.bounds(end); !nextStart.Equal(end) {
				t.Fatalf("%+v: window after [%v, %v) starts at %v", cw, start, end, nextStart)
			}
			if prevStart, prevEnd := w.bounds(start.Add(-time.Nanosecond)); !prevEnd.Equal(start) || !prevStart.Before(start) {
				t.Fatalf("%+v: window before [%v, %v) is [%v, %v)", cw, start, end, prevStart, prevEnd)
			}
		}
	}
}

func TestNewFixedWindowErrors(t *testing.T) {
	if _, err := newFixedWindow(&config.Window{Period: "day", Timezone: "Mars/Olympus"}); err == nil {
		t.Error("unknown timezone accepted")
	}
	if _, err := newFixedWindow(&config.Window{Period: "fortnight"}); err == nil {
		t.Error("unknown period accepted")
	}
}

func TestRollingWindow(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 30, 0, time.UTC)
	r := newRollingWindow(10 * time.Minute)
	r.add(now, 3)
	r.add(now.Add(4*time.Minute), 2)
	if got := r.used(now.Add(5 * time.Minute)); got != 5 {
		t.Fatalf("used = %d, want 5", got)
	}
	// The first bucket (minute 12:00) leaves the window at 12:10.
	if got, want := r.nextExpiry(now.Add(5*time.Minute)), time.Date(2026, 11, 2, 12, 10, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("nextExpiry = %v, want %v", got, want)
	}
	if got := r.used(time.Date(2026, 11, 2, 12, 10, 0, 0, time.UTC)); got != 2 {
		t.Fatalf("used after expiry = %d, want 2", got)
	}
	if got := r.used(now.Add(time.Hour)); got != 0 || len(r.buckets) != 0 {
		t.Fatalf("used much later = %d, buckets %v", got, r.buckets)
	}
}
