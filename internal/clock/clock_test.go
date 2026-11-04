package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRealNow(t *testing.T) {
	c := Real()
	before := time.Now()
	got := c.Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("Now() = %v, want between %v and %v", got, before, after)
	}
	if d := c.Since(before); d < 0 {
		t.Fatalf("Since() = %v, want >= 0", d)
	}
	if d := c.Until(before.Add(time.Hour)); d <= 0 {
		t.Fatalf("Until() = %v, want > 0", d)
	}
}

func TestRealTimers(t *testing.T) {
	c := Real()
	receive(t, c.NewTimer(time.Millisecond).C())
	receive(t, c.After(time.Millisecond))

	stopped := c.NewTimer(time.Hour)
	if !stopped.Stop() {
		t.Fatal("Stop() on an active timer returned false")
	}
	if stopped.Reset(time.Millisecond) {
		t.Fatal("Reset() on a stopped timer returned true")
	}
	receive(t, stopped.C())

	ticker := c.NewTicker(time.Millisecond)
	receive(t, ticker.C())
	ticker.Reset(2 * time.Millisecond)
	receive(t, ticker.C())
	ticker.Stop()

	done := make(chan time.Time, 1)
	af := c.AfterFunc(time.Millisecond, func() { done <- time.Now() })
	if af.C() != nil {
		t.Fatal("AfterFunc timer has a non-nil channel")
	}
	receive(t, done)
}

func TestRealSleep(t *testing.T) {
	c := Real()
	if err := c.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("Sleep() = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep() on canceled ctx = %v, want context.Canceled", err)
	}
	if err := c.Sleep(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep(0) on canceled ctx = %v, want context.Canceled", err)
	}
}
