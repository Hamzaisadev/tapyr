// Package budget is the budget engine: every outbound request, retries,
// pages and token requests included, goes through a reservation (see
// docs/spec/04-budget-engine.md).
package budget

import (
	"context"
	"net/http"
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

// Descriptor describes a request for the budget engine.
type Descriptor struct {
	Provider string
	Account  string
	Target   string
	// Credential is the keyed hash of the account secrets (scope
	// dimension credential).
	Credential string
	// Endpoint is the templated path, never the resolved URL.
	Endpoint string
	Method   string
	Vars     map[string]string
	Job      string
	Priority config.Priority
	// Retry marks retries, which consume from the retry reserve.
	Retry bool
	// AuthRequest marks token requests issued by authenticators.
	AuthRequest bool
	// Deadline of the run, on the injected clock; zero means none. A wait
	// that cannot end before it is refused at once.
	Deadline time.Time
}

// Outcome is reported once the response is received (or the request failed).
type Outcome struct {
	Status int
	Header http.Header
	Err    error
	// Transient marks failures of the transient class (network errors,
	// timeouts, retryable statuses), which feed the circuit breakers.
	Transient bool
}

// Reservation is a grant to send one request.
type Reservation interface {
	// Done releases the reservation and reports the outcome (quota header
	// sync, penalties).
	Done(Outcome)
}

// Reserver grants reservations. Reserve blocks while waiting for rate or
// concurrency limits, honoring ctx, and returns an error when the request
// is refused.
type Reserver interface {
	Reserve(ctx context.Context, d Descriptor) (Reservation, error)
}
