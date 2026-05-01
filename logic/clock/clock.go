// Package clock supplies an injectable wall-clock so Logic-layer code stays
// deterministic. Production wires System; tests wire Fixed.
package clock

import "time"

// Clock returns the current time. The Logic layer never calls time.Now()
// directly — every package that needs the current moment receives a Clock.
type Clock interface {
	Now() time.Time
}

// System is the production clock; its Now wraps time.Now in the local zone.
type System struct{}

func (System) Now() time.Time { return time.Now() }

// Fixed returns T on every call. Useful in tests that need to advance time
// deterministically; rebind the field between operations.
type Fixed struct {
	T time.Time
}

func (f Fixed) Now() time.Time { return f.T }
