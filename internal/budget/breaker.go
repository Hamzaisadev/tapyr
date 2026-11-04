package budget

import (
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

// Circuit states, as exposed by tapyr_circuit_state.
const (
	CircuitClosed   = 0
	CircuitHalfOpen = 1
	CircuitOpen     = 2
)

// breaker is the circuit breaker of an account (see "Circuit breaker" in
// 05-collection.md): it opens after consecutive transient failures, then
// lets one probe through after the cooldown.
type breaker struct {
	state     int
	failures  int
	openings  int
	openUntil time.Time
	probing   bool
}

// breakerKey identifies the breaker of a request: per account, and per
// target for multi-target accounts.
func breakerKey(d *Descriptor) string {
	if d.Target == "" {
		return d.Account
	}
	return d.Account + "/" + d.Target
}

func (e *Engine) breaker(account string) *breaker {
	b, ok := e.breakers[account]
	if !ok {
		b = &breaker{}
		e.breakers[account] = b
	}
	return b
}

// allow reports whether a request may go through, and whether it is the
// half-open probe.
func (b *breaker) allow(now time.Time) (probe, ok bool) {
	switch b.state {
	case CircuitClosed:
		return false, true
	case CircuitOpen:
		if now.Before(b.openUntil) {
			return false, false
		}
		b.state = CircuitHalfOpen
	}
	if b.probing {
		return false, false
	}
	return true, true
}

// record applies the outcome of a request.
func (b *breaker) record(o Outcome, probe bool, now time.Time, cfg config.CircuitBreakerSettings) {
	if probe {
		b.probing = false
	}
	if !o.Transient {
		b.failures = 0
		if b.state != CircuitClosed && (probe || b.state == CircuitHalfOpen) {
			b.state, b.openings = CircuitClosed, 0
		}
		return
	}
	b.failures++
	if cfg.FailureThreshold <= 0 || (!probe && b.failures < cfg.FailureThreshold) {
		return
	}
	b.openings++
	cooldown := cfg.Cooldown.Std() << (b.openings - 1)
	if limit := cfg.MaxCooldown.Std(); limit > 0 && (cooldown > limit || cooldown <= 0) {
		cooldown = limit
	}
	b.state, b.openUntil = CircuitOpen, now.Add(cooldown)
}

// CircuitState returns the state of the breaker of an account (and target).
func (e *Engine) CircuitState(account, target string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.breakers[breakerKey(&Descriptor{Account: account, Target: target})]
	if !ok {
		return CircuitClosed
	}
	if b.state == CircuitOpen && !e.clock.Now().Before(b.openUntil) {
		return CircuitHalfOpen
	}
	return b.state
}
