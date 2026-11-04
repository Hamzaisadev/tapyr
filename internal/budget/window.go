package budget

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

// fixedWindow computes calendar windows in a timezone.
type fixedWindow struct {
	period    string
	loc       *time.Location
	resetHour int
	resetMin  int
	weekStart time.Weekday
	monthDay  int
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

func newFixedWindow(w *config.Window) (fixedWindow, error) {
	tz := w.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fixedWindow{}, fmt.Errorf("window timezone: %w", err)
	}
	fw := fixedWindow{period: w.Period, loc: loc, weekStart: time.Monday, monthDay: 1}
	if w.ResetAt != "" {
		h, m, _ := strings.Cut(w.ResetAt, ":")
		fw.resetHour, _ = strconv.Atoi(h)
		fw.resetMin, _ = strconv.Atoi(m)
	}
	if w.WeekStart != "" {
		fw.weekStart = weekdays[w.WeekStart]
	}
	if w.MonthDay != 0 {
		fw.monthDay = w.MonthDay
	}
	switch fw.period {
	case "minute", "hour", "day", "week", "month":
	default:
		return fixedWindow{}, fmt.Errorf("unknown window period %q", fw.period)
	}
	return fw, nil
}

// bounds returns the window [start, end) containing t.
func (w fixedWindow) bounds(t time.Time) (start, end time.Time) {
	lt := t.In(w.loc)
	switch w.period {
	case "minute":
		return truncateLocal(lt, time.Minute)
	case "hour":
		return truncateLocal(lt, time.Hour)
	case "day":
		start = w.at(lt.Year(), lt.Month(), lt.Day())
		if lt.Before(start) {
			start = w.at(lt.Year(), lt.Month(), lt.Day()-1)
		}
		return start, w.at(start.Year(), start.Month(), start.Day()+1)
	case "week":
		back := (int(lt.Weekday()) - int(w.weekStart) + 7) % 7
		start = w.at(lt.Year(), lt.Month(), lt.Day()-back)
		if lt.Before(start) {
			start = w.at(lt.Year(), lt.Month(), lt.Day()-back-7)
		}
		return start, w.at(start.Year(), start.Month(), start.Day()+7)
	default: // month
		start = w.monthStart(lt.Year(), lt.Month())
		if lt.Before(start) {
			start = w.monthStart(lt.Year(), lt.Month()-1)
		}
		return start, w.monthStart(start.Year(), start.Month()+1)
	}
}

// at returns the reset time on a calendar day; a reset time that does not
// exist (DST gap) is normalized forward by time.Date.
func (w fixedWindow) at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, w.resetHour, w.resetMin, 0, 0, w.loc)
}

// monthStart returns the reset time of a month, monthDay clamped to the
// last day of short months.
func (w fixedWindow) monthStart(y int, m time.Month) time.Time {
	first := time.Date(y, m, 1, 0, 0, 0, 0, w.loc)
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, w.loc).Day()
	return w.at(first.Year(), first.Month(), min(w.monthDay, last))
}

// truncateLocal aligns minute and hour windows on the local clock, also for
// timezones whose offset is not a whole hour.
func truncateLocal(t time.Time, d time.Duration) (time.Time, time.Time) {
	_, off := t.Zone()
	offset := time.Duration(off) * time.Second
	start := t.Add(offset).Truncate(d).Add(-offset)
	return start, start.Add(d)
}

// rollingWindow counts events in per-minute buckets over a rolling duration.
type rollingWindow struct {
	minutes int64
	buckets map[int64]int64 // minute index -> count
}

func newRollingWindow(d time.Duration) *rollingWindow {
	return &rollingWindow{minutes: int64(d / time.Minute), buckets: map[int64]int64{}}
}

func minuteIndex(t time.Time) int64 { return t.Unix() / 60 }

// prune drops the buckets that left the window.
func (r *rollingWindow) prune(now time.Time) {
	first := minuteIndex(now) - r.minutes + 1
	for m := range r.buckets {
		if m < first {
			delete(r.buckets, m)
		}
	}
}

func (r *rollingWindow) used(now time.Time) int64 {
	r.prune(now)
	var n int64
	for _, c := range r.buckets {
		n += c
	}
	return n
}

func (r *rollingWindow) add(now time.Time, n int64) {
	r.buckets[minuteIndex(now)] += n
}

// nextExpiry returns when the oldest bucket leaves the window.
func (r *rollingWindow) nextExpiry(now time.Time) time.Time {
	r.prune(now)
	oldest := minuteIndex(now)
	for m := range r.buckets {
		oldest = min(oldest, m)
	}
	return time.Unix((oldest+r.minutes)*60, 0)
}
