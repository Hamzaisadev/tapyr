// Package budgettest provides budget engine doubles for tests. It is never
// imported by production code: every request of Tapyr goes through the
// real engine.
package budgettest

import (
	"context"

	"github.com/kentrow/tapyr/internal/budget"
)

// Passthrough grants every request immediately, for tests that do not
// exercise limits.
type Passthrough struct{}

// Reserve implements budget.Reserver.
func (Passthrough) Reserve(ctx context.Context, _ budget.Descriptor) (budget.Reservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return noopReservation{}, nil
}

type noopReservation struct{}

func (noopReservation) Done(budget.Outcome) {}
