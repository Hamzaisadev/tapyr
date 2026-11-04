package clock

import (
	"context"
	"testing"
	"time"
)

func TestScaled(t *testing.T) {
	base := NewFake(epoch)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewScaled(base, 60, start)
	if !s.Now().Equal(start) {
		t.Fatalf("Now() = %v", s.Now())
	}
	base.Advance(time.Second)
	if got := s.Now(); !got.Equal(start.Add(time.Minute)) {
		t.Fatalf("after 1s of base time: %v, want one scaled minute", got)
	}
	if s.Since(start) != time.Minute || s.Until(start.Add(2*time.Minute)) != time.Minute {
		t.Fatal("Since or Until do not scale")
	}

	timer := s.NewTimer(time.Hour) // one minute of base time
	base.Advance(59 * time.Second)
	assertEmpty(t, timer.C())
	base.Advance(time.Second)
	receive(t, timer.C())
	timer.Reset(time.Hour)
	base.Advance(time.Minute)
	receive(t, timer.C())

	ticker := s.NewTicker(time.Hour)
	base.Advance(time.Minute)
	receive(t, ticker.C())
	ticker.Reset(2 * time.Hour)
	base.Advance(2 * time.Minute)
	receive(t, ticker.C())
	ticker.Stop()

	done := make(chan struct{}, 1)
	s.AfterFunc(30*time.Minute, func() { done <- struct{}{} })
	base.Advance(30 * time.Second)
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("AfterFunc did not run")
	}
	receiveAfter := s.After(time.Minute)
	base.Advance(time.Second)
	receive(t, receiveAfter)

	errc := make(chan error, 1)
	go func() { errc <- s.Sleep(context.Background(), time.Hour) }()
	if err := base.BlockUntil(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	base.Advance(time.Minute)
	if err := waitErr(t, errc); err != nil {
		t.Fatal(err)
	}
	if NewScaled(base, 0, start).factor != 1 {
		t.Error("non-positive factor not treated as 1")
	}
}
