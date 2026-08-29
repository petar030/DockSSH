package refresh

import (
	"context"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

type Requester interface {
	Refresh(context.Context, backend.RefreshKey, backend.RefreshReason) (backend.RefreshResult, error)
}

// Scheduler submits periodic work through the same coordinator used by every
// other refresh trigger.
type Scheduler struct {
	clock     backend.Clock
	requester Requester
	policies  []backend.RefreshPolicy
	mu        sync.Mutex
	running   bool
}

func NewScheduler(
	clock backend.Clock,
	requester Requester,
	policies []backend.RefreshPolicy,
) (*Scheduler, error) {
	if clock == nil || requester == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create refresh scheduler"}
	}
	seen := make(map[backend.RefreshKey]struct{}, len(policies))
	for _, policy := range policies {
		if !policy.Key.Valid() || policy.Interval <= 0 {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create refresh scheduler", Resource: string(policy.Key.Kind), ID: policy.Key.ID}
		}
		if _, exists := seen[policy.Key]; exists {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create refresh scheduler", Resource: string(policy.Key.Kind), ID: policy.Key.ID}
		}
		seen[policy.Key] = struct{}{}
	}
	return &Scheduler{
		clock: clock, requester: requester, policies: append([]backend.RefreshPolicy(nil), policies...),
	}, nil
}

// Run blocks until context cancellation. It may be invoked only once.
func (scheduler *Scheduler) Run(ctx context.Context) error {
	if ctx == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "run refresh scheduler"}
	}
	scheduler.mu.Lock()
	if scheduler.running {
		scheduler.mu.Unlock()
		return &backend.AppError{Code: backend.ErrorConflict, Operation: "run refresh scheduler"}
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
					_, _ = scheduler.requester.Refresh(workerContext, policy.Key, backend.RefreshScheduled)
				}
			}
		}()
	}
	<-ctx.Done()
	cancelWorkers()
	workers.Wait()
	return nil
}
