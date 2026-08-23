// Package testkit provides deterministic fakes shared by backend unit tests.
package testkit

import (
	"context"
	"sync"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// ManualClock advances only when the test calls Advance.
type ManualClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers map[*manualTicker]struct{}
	changed chan struct{}
}

func NewManualClock(now time.Time) *ManualClock {
	return &ManualClock{
		now:     now,
		tickers: make(map[*manualTicker]struct{}),
		changed: make(chan struct{}),
	}
}

func (clock *ManualClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *ManualClock) NewTicker(interval time.Duration) backend.Ticker {
	if interval <= 0 {
		panic("testkit: non-positive ticker interval")
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	ticker := &manualTicker{
		clock:    clock,
		interval: interval,
		next:     clock.now.Add(interval),
		values:   make(chan time.Time, 128),
	}
	clock.tickers[ticker] = struct{}{}
	clock.notifyLocked()
	return ticker
}

func (clock *ManualClock) Advance(duration time.Duration) {
	if duration < 0 {
		panic("testkit: cannot move clock backwards")
	}
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	for ticker := range clock.tickers {
		if ticker.stopped {
			continue
		}
		for !ticker.next.After(clock.now) {
			select {
			case ticker.values <- ticker.next:
			default:
				panic("testkit: manual ticker buffer exhausted")
			}
			ticker.next = ticker.next.Add(ticker.interval)
		}
	}
	clock.notifyLocked()
	clock.mu.Unlock()
}

// WaitForTickers waits without sleeps until at least count tickers exist.
func (clock *ManualClock) WaitForTickers(ctx context.Context, count int) error {
	for {
		clock.mu.Lock()
		active := 0
		for ticker := range clock.tickers {
			if !ticker.stopped {
				active++
			}
		}
		changed := clock.changed
		clock.mu.Unlock()
		if active >= count {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (clock *ManualClock) notifyLocked() {
	close(clock.changed)
	clock.changed = make(chan struct{})
}

type manualTicker struct {
	clock    *ManualClock
	interval time.Duration
	next     time.Time
	values   chan time.Time
	stopped  bool
}

func (ticker *manualTicker) C() <-chan time.Time {
	return ticker.values
}

func (ticker *manualTicker) Stop() {
	ticker.clock.mu.Lock()
	defer ticker.clock.mu.Unlock()
	if ticker.stopped {
		return
	}
	ticker.stopped = true
	delete(ticker.clock.tickers, ticker)
	ticker.clock.notifyLocked()
}
