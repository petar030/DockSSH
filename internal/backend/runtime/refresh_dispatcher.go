package runtime

import (
	"context"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// PageRefresher is the refresh capability used by the dispatcher worker.
type PageRefresher interface {
	RefreshPage(context.Context, backend.Page, backend.RefreshReason) error
}

// RefreshDispatcher owns bounded, process-lifetime automatic page refreshes.
// Repeated requests for one page collapse into one pending dirty-page entry;
// no Docker resource data is stored here.
type RefreshDispatcher struct {
	refreshes PageRefresher
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	done      chan struct{}

	mu      sync.Mutex
	pending map[backend.Page]backend.RefreshReason
	closed  bool
	close   sync.Once
}

func NewRefreshDispatcher(refreshes PageRefresher) (*RefreshDispatcher, error) {
	if refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create refresh dispatcher"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := &RefreshDispatcher{
		refreshes: refreshes,
		ctx:       ctx,
		cancel:    cancel,
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
		pending:   make(map[backend.Page]backend.RefreshReason),
	}
	go dispatcher.run()
	return dispatcher, nil
}

// RequestPage synchronously records that a page needs a fresh authoritative
// read, then wakes the backend worker without waiting for that read.
func (dispatcher *RefreshDispatcher) RequestPage(page backend.Page, reason backend.RefreshReason) {
	if !page.Valid() {
		return
	}
	dispatcher.mu.Lock()
	if dispatcher.closed {
		dispatcher.mu.Unlock()
		return
	}
	dispatcher.pending[page] = reason
	dispatcher.mu.Unlock()
	select {
	case dispatcher.wake <- struct{}{}:
	default:
	}
}

func (dispatcher *RefreshDispatcher) run() {
	defer close(dispatcher.done)
	for {
		select {
		case <-dispatcher.ctx.Done():
			return
		case <-dispatcher.wake:
		}
		for {
			page, reason, ok := dispatcher.takePending()
			if !ok {
				break
			}
			_ = dispatcher.refreshes.RefreshPage(dispatcher.ctx, page, reason)
			if dispatcher.ctx.Err() != nil {
				return
			}
		}
	}
}

func (dispatcher *RefreshDispatcher) takePending() (backend.Page, backend.RefreshReason, bool) {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	for page, reason := range dispatcher.pending {
		delete(dispatcher.pending, page)
		return page, reason, true
	}
	return "", "", false
}

// Close cancels an active automatic refresh, discards pending work, and waits
// for the single worker goroutine to stop.
func (dispatcher *RefreshDispatcher) Close() error {
	dispatcher.close.Do(func() {
		dispatcher.mu.Lock()
		dispatcher.closed = true
		dispatcher.pending = make(map[backend.Page]backend.RefreshReason)
		dispatcher.cancel()
		dispatcher.mu.Unlock()
		<-dispatcher.done
	})
	return nil
}

var _ backend.PageRefreshRequester = (*RefreshDispatcher)(nil)
