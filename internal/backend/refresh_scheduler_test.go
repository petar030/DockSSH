package backend_test

import (
	"context"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

type recordingRequester struct {
	calls chan scheduledCall
}

type scheduledCall struct {
	scope  backend.RefreshScope
	reason backend.RefreshReason
}

func (requester *recordingRequester) Refresh(
	_ context.Context,
	scope backend.RefreshScope,
	reason backend.RefreshReason,
) (backend.RefreshResult, error) {
	requester.calls <- scheduledCall{scope: scope, reason: reason}
	return backend.RefreshResult{Scope: scope}, nil
}

func TestRefreshSchedulerUsesInjectedClockAndStopsCleanly(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(5_000, 0))
	scope := backend.RefreshScope{Resource: backend.ResourceSystem, View: backend.ViewEngine}
	requester := &recordingRequester{calls: make(chan scheduledCall, 2)}
	scheduler, err := backend.NewRefreshScheduler(clock, requester, []backend.RefreshPolicy{{
		Scope: scope, Interval: 10 * time.Second,
	}})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := clock.WaitForTickers(waitCtx, 1); err != nil {
		t.Fatalf("wait for scheduler ticker: %v", err)
	}

	clock.Advance(9 * time.Second)
	select {
	case call := <-requester.calls:
		t.Fatalf("early scheduled call: %#v", call)
	default:
	}
	clock.Advance(time.Second)
	select {
	case call := <-requester.calls:
		if call.scope != scope || call.reason != backend.RefreshScheduled {
			t.Fatalf("scheduled call = %#v", call)
		}
	case <-waitCtx.Done():
		t.Fatal("scheduled refresh was not requested")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("scheduler stopped with error: %v", err)
		}
	case <-waitCtx.Done():
		t.Fatal("scheduler did not stop")
	}
}

func TestRefreshSchedulerRejectsInvalidPolicies(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(1, 0))
	requester := &recordingRequester{calls: make(chan scheduledCall, 1)}
	scope := backend.RefreshScope{Resource: backend.ResourceSystem, View: backend.ViewEngine}
	if _, err := backend.NewRefreshScheduler(clock, requester, []backend.RefreshPolicy{{Scope: scope}}); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
		t.Fatalf("zero interval error = %v", err)
	}
	if _, err := backend.NewRefreshScheduler(clock, requester, []backend.RefreshPolicy{
		{Scope: scope, Interval: time.Second},
		{Scope: scope, Interval: 2 * time.Second},
	}); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
		t.Fatalf("duplicate scope error = %v", err)
	}
}
