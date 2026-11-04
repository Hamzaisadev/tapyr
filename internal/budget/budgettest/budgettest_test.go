package budgettest

import (
	"context"
	"errors"
	"testing"

	"github.com/kentrow/tapyr/internal/budget"
)

func TestPassthrough(t *testing.T) {
	r, err := Passthrough{}.Reserve(context.Background(), budget.Descriptor{Endpoint: "/devices"})
	if err != nil || r == nil {
		t.Fatalf("Reserve() = %v, %v", r, err)
	}
	r.Done(budget.Outcome{Status: 200})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Passthrough{}).Reserve(ctx, budget.Descriptor{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reserve() on canceled ctx = %v", err)
	}
}
