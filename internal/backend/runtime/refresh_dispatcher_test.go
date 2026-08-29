package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestRefreshDispatcherCoalescesMatchingPendingPagesAndBoundsConcurrency(t *testing.T) {
	refreshes := newBlockingPageRefresher()
	dispatcher, err := NewRefreshDispatcher(refreshes)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })

	dispatcher.RequestPage(backend.PageContainers, backend.RefreshDockerEvent)
	first := waitRefreshCall(t, refreshes.started)
	if first.page != backend.PageContainers {
		t.Fatalf("first refresh = %#v", first)
	}
	for range 100 {
		dispatcher.RequestPage(backend.PageContainers, backend.RefreshCommand)
	}
	refreshes.release <- struct{}{}
	second := waitRefreshCall(t, refreshes.started)
	if second.page != backend.PageContainers || second.reason != backend.RefreshCommand {
		t.Fatalf("second refresh = %#v", second)
	}
	refreshes.release <- struct{}{}

	deadline := time.Now().Add(time.Second)
	for {
		calls, active, maximum := refreshes.snapshot()
		if calls == 2 && active == 0 {
			if maximum != 1 {
				t.Fatalf("maximum active refreshes = %d", maximum)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refresh state = calls %d, active %d, maximum %d", calls, active, maximum)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRefreshDispatcherUsesBackendContextAndStopsCleanly(t *testing.T) {
	refreshes := newBlockingPageRefresher()
	dispatcher, err := NewRefreshDispatcher(refreshes)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	dispatcher.RequestPage(backend.PageDashboard, backend.RefreshCommand)
	_ = waitRefreshCall(t, refreshes.started)
	closed := make(chan struct{})
	go func() {
		_ = dispatcher.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("dispatcher Close did not cancel the active refresh")
	}
	dispatcher.RequestPage(backend.PageContainers, backend.RefreshCommand)
	if calls, _, _ := refreshes.snapshot(); calls != 1 {
		t.Fatalf("refreshes after Close = %d", calls)
	}
}

type pageRefreshCall struct {
	page   backend.Page
	reason backend.RefreshReason
}

type blockingPageRefresher struct {
	started chan pageRefreshCall
	release chan struct{}
	mu      sync.Mutex
	calls   int
	active  int
	maximum int
}

func newBlockingPageRefresher() *blockingPageRefresher {
	return &blockingPageRefresher{
		started: make(chan pageRefreshCall, 4),
		release: make(chan struct{}, 4),
	}
}

func (refresher *blockingPageRefresher) RefreshPage(ctx context.Context, page backend.Page, reason backend.RefreshReason) error {
	refresher.mu.Lock()
	refresher.calls++
	refresher.active++
	if refresher.active > refresher.maximum {
		refresher.maximum = refresher.active
	}
	refresher.mu.Unlock()
	refresher.started <- pageRefreshCall{page: page, reason: reason}
	select {
	case <-refresher.release:
	case <-ctx.Done():
	}
	refresher.mu.Lock()
	refresher.active--
	refresher.mu.Unlock()
	return nil
}

func (refresher *blockingPageRefresher) snapshot() (calls, active, maximum int) {
	refresher.mu.Lock()
	defer refresher.mu.Unlock()
	return refresher.calls, refresher.active, refresher.maximum
}

func waitRefreshCall(t *testing.T, calls <-chan pageRefreshCall) pageRefreshCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("wait for dispatched refresh")
		return pageRefreshCall{}
	}
}
