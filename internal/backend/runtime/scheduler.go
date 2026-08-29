package runtime

import (
	"context"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

type scheduledRefreshRequester interface {
	Request(backend.RefreshKey, backend.RefreshReason) error
}

// Scheduler periodically submits refresh keys to the same Refresh Manager used
// by TUI, command and Docker-event triggers.
type Scheduler struct {
	clock    backend.Clock
	requests scheduledRefreshRequester
	policies []backend.RefreshPolicy
	mu       sync.Mutex
	running  bool
}

func NewScheduler(clock backend.Clock, requests scheduledRefreshRequester, policies []backend.RefreshPolicy) (*Scheduler, error) {
	if clock == nil || requests == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create refresh scheduler"}
	}
	seen := make(map[backend.RefreshKey]struct{}, len(policies))
	for _, policy := range policies {
		if !policy.Key.Valid() || policy.Interval <= 0 {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create refresh scheduler", Resource: string(policy.Key.Kind), ID: policy.Key.ID}
		}
		if _, exists := seen[policy.Key]; exists {
			return nil, &backend.AppError{Code: backend.ErrorConflict, Operation: "create refresh scheduler", Resource: string(policy.Key.Kind), ID: policy.Key.ID}
		}
		seen[policy.Key] = struct{}{}
	}
	return &Scheduler{clock: clock, requests: requests, policies: append([]backend.RefreshPolicy(nil), policies...)}, nil
}

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
				case <-ctx.Done():
					return
				case <-ticker.C():
					_ = scheduler.requests.Request(policy.Key, backend.RefreshScheduled)
				}
			}
		}()
	}
	<-ctx.Done()
	workers.Wait()
	return nil
}
