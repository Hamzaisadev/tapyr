// Package clock abstracts time so that every time-dependent component
// (budget windows, scheduling, backoff, token expiry, staleness) can be driven
// by a fake clock in tests.
//
// Production code must obtain time exclusively through a Clock. The time
// package functions that read or wait on the wall clock (time.Now, time.Sleep,
// time.After, time.NewTimer, ...) are forbidden outside this package by the
// linter configuration.
package clock

import (
	"context"
	"time"
)

// Clock provides the current time and timers.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Since returns the time elapsed since t.
	Since(t time.Time) time.Duration
	// Until returns the duration until t.
	Until(t time.Time) time.Duration
	// NewTimer creates a Timer that sends the current time on its channel
	// after at least duration d.
	NewTimer(d time.Duration) Timer
	// NewTicker returns a Ticker that sends the time on its channel every
	// period d. It panics if d <= 0.
	NewTicker(d time.Duration) Ticker
	// After waits for the duration to elapse and then sends the current time
	// on the returned channel.
	After(d time.Duration) <-chan time.Time
	// AfterFunc waits for the duration to elapse and then calls f in its own
	// goroutine. The returned Timer has a nil channel.
	AfterFunc(d time.Duration, f func()) Timer
	// Sleep pauses for at least duration d, or until ctx is done. It returns
	// ctx.Err() when the context ends first.
	Sleep(ctx context.Context, d time.Duration) error
}

// Timer is the Clock equivalent of time.Timer. Stop and Reset follow the Go
// 1.23+ semantics: after they return, no stale value is received from C.
type Timer interface {
	// C returns the channel on which the time is delivered, or nil for timers
	// created by AfterFunc.
	C() <-chan time.Time
	// Stop prevents the timer from firing. It returns true if the call stops
	// the timer, false if the timer has already expired or been stopped.
	Stop() bool
	// Reset changes the timer to expire after duration d. It returns true if
	// the timer had been active.
	Reset(d time.Duration) bool
}

// Ticker is the Clock equivalent of time.Ticker.
type Ticker interface {
	// C returns the channel on which the ticks are delivered.
	C() <-chan time.Time
	// Stop turns off the ticker. No more ticks are sent after it returns.
	Stop()
	// Reset stops the ticker and resets its period to d. It panics if d <= 0.
	Reset(d time.Duration)
}

// Real returns a Clock backed by the time package.
func Real() Clock {
	return realClock{}
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (realClock) Until(t time.Time) time.Duration        { return time.Until(t) }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (realClock) NewTimer(d time.Duration) Timer {
	t := time.NewTimer(d)
	return &realTimer{t: t, c: t.C}
}

func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{t: time.AfterFunc(d, f)}
}

func (realClock) NewTicker(d time.Duration) Ticker {
	return realTicker{time.NewTicker(d)}
}

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type realTimer struct {
	t *time.Timer
	c <-chan time.Time
}

func (r *realTimer) C() <-chan time.Time        { return r.c }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time   { return r.t.C }
func (r realTicker) Stop()                 { r.t.Stop() }
func (r realTicker) Reset(d time.Duration) { r.t.Reset(d) }
