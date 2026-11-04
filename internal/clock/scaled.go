package clock

import (
	"context"
	"time"
)

// Scaled is a Clock that runs factor times faster than a base clock, from a
// start instant: the simulator --time-scale for demos and manual tests.
type Scaled struct {
	base      Clock
	factor    float64
	baseStart time.Time
	start     time.Time
}

// NewScaled returns a clock showing start now, and moving factor times
// faster than base. A factor <= 0 is treated as 1.
func NewScaled(base Clock, factor float64, start time.Time) *Scaled {
	if factor <= 0 {
		factor = 1
	}
	return &Scaled{base: base, factor: factor, baseStart: base.Now(), start: start}
}

// Now returns the scaled time.
func (s *Scaled) Now() time.Time {
	return s.start.Add(time.Duration(float64(s.base.Since(s.baseStart)) * s.factor))
}

// Since returns the scaled time elapsed since t.
func (s *Scaled) Since(t time.Time) time.Duration { return s.Now().Sub(t) }

// Until returns the scaled duration until t.
func (s *Scaled) Until(t time.Time) time.Duration { return t.Sub(s.Now()) }

// real converts a scaled duration into a base duration.
func (s *Scaled) real(d time.Duration) time.Duration { return time.Duration(float64(d) / s.factor) }

// NewTimer creates a timer firing after the scaled duration d.
func (s *Scaled) NewTimer(d time.Duration) Timer {
	return &scaledTimer{Timer: s.base.NewTimer(s.real(d)), s: s}
}

// After waits for the scaled duration d.
func (s *Scaled) After(d time.Duration) <-chan time.Time { return s.NewTimer(d).C() }

// AfterFunc calls f after the scaled duration d.
func (s *Scaled) AfterFunc(d time.Duration, f func()) Timer {
	return &scaledTimer{Timer: s.base.AfterFunc(s.real(d), f), s: s}
}

// NewTicker returns a ticker with a scaled period.
func (s *Scaled) NewTicker(d time.Duration) Ticker {
	return &scaledTicker{Ticker: s.base.NewTicker(s.real(d)), s: s}
}

// Sleep pauses for the scaled duration d or until ctx is done.
func (s *Scaled) Sleep(ctx context.Context, d time.Duration) error {
	return s.base.Sleep(ctx, s.real(d))
}

// Timer values carry base times; Reset takes scaled durations.
type scaledTimer struct {
	Timer
	s *Scaled
}

func (t *scaledTimer) Reset(d time.Duration) bool { return t.Timer.Reset(t.s.real(d)) }

type scaledTicker struct {
	Ticker
	s *Scaled
}

func (t *scaledTicker) Reset(d time.Duration) { t.Ticker.Reset(t.s.real(d)) }
