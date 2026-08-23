package backend

import (
	"context"
	"sync"
	"time"
)

// RefreshPolicy schedules one shared refresh independently of session count.
type RefreshPolicy struct {
	Key      RefreshKey
	Interval time.Duration
}

type RefreshRequester interface {
	Refresh(context.Context, RefreshKey, RefreshReason) (RefreshResult, error)
}

// RefreshScheduler submits scheduled work through the same coordinator used by
// every other refresh trigger.
type RefreshScheduler struct {
	clock     Clock
	requester RefreshRequester
	policies  []RefreshPolicy
	mu        sync.Mutex
	running   bool
}

func NewRefreshScheduler(clock Clock, requester RefreshRequester, policies []RefreshPolicy) (*RefreshScheduler, error) {
	if clock == nil || requester == nil {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "create refresh scheduler"}
	}
	seen := make(map[RefreshKey]struct{}, len(policies))
	for _, policy := range policies {
		if err := validateRefreshKey(policy.Key); err != nil {
			return nil, err
		}
		if policy.Interval <= 0 {
			return nil, &AppError{Code: ErrorInvalidInput, Operation: "create refresh scheduler", Resource: string(policy.Key.Kind), ID: policy.Key.ID}
		}
		if _, exists := seen[policy.Key]; exists {
			return nil, &AppError{Code: ErrorInvalidInput, Operation: "create refresh scheduler", Resource: string(policy.Key.Kind), ID: policy.Key.ID}
		}
		seen[policy.Key] = struct{}{}
	}
	return &RefreshScheduler{
		clock: clock, requester: requester, policies: append([]RefreshPolicy(nil), policies...),
	}, nil
}

// Run blocks until context cancellation. It may be invoked only once.
func (scheduler *RefreshScheduler) Run(ctx context.Context) error {
	if ctx == nil {
		return &AppError{Code: ErrorInvalidInput, Operation: "run refresh scheduler"}
	}
	scheduler.mu.Lock()
	if scheduler.running {
		scheduler.mu.Unlock()
		return &AppError{Code: ErrorConflict, Operation: "run refresh scheduler"}
	}
	scheduler.running = true
	scheduler.mu.Unlock()

	workerContext, cancelWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	for _, policy := range scheduler.policies {
		policy := policy
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := scheduler.clock.NewTicker(policy.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-workerContext.Done():
					return
				case <-ticker.C():
					_, _ = scheduler.requester.Refresh(workerContext, policy.Key, RefreshScheduled)
				}
			}
		}()
	}
	<-ctx.Done()
	cancelWorkers()
	workers.Wait()
	return nil
}
