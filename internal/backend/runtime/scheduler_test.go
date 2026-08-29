package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

func TestSchedulerSubmitsPoliciesToRefreshManagerInterface(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(100, 0))
	requests := &scheduledRequests{calls: make(chan refreshRequest, 2)}
	key := backend.RefreshKey{Kind: "dashboard.summary"}
	scheduler, err := NewScheduler(clock, requests, []backend.RefreshPolicy{{Key: key, Interval: time.Second}})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := clock.WaitForTickers(waitCtx, 1); err != nil {
		t.Fatalf("wait for ticker: %v", err)
	}
	clock.Advance(time.Second)
	call := receiveWithin(t, requests.calls)
	if call.key != key || call.reason != backend.RefreshScheduled {
		t.Fatalf("scheduled request = %#v", call)
	}
	cancel()
	if err := receiveWithin(t, done); err != nil {
		t.Fatalf("scheduler stopped with error: %v", err)
	}
}

type scheduledRequests struct {
	calls chan refreshRequest
}

func (requests *scheduledRequests) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	requests.calls <- refreshRequest{key: key, reason: reason}
	return nil
}
