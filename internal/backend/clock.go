// This file defines a small adapter boundary around Go's standard time package.
// It does not maintain an independent clock or store time itself. Production
// uses realClock, which delegates directly to time.Now and time.NewTicker, while
// tests can provide a manually controlled Clock to verify scheduling and event
// timestamps without waiting for real time to pass.
package backend

import "time"

// Clock makes refresh timing and scheduling deterministic in tests.
type Clock interface {
	Now() time.Time
	NewTicker(time.Duration) Ticker
}

// Ticker is the minimal periodic timer contract used by RefreshScheduler.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) NewTicker(interval time.Duration) Ticker {
	return realTicker{Ticker: time.NewTicker(interval)}
}

type realTicker struct {
	*time.Ticker
}

func (ticker realTicker) C() <-chan time.Time {
	return ticker.Ticker.C
}
