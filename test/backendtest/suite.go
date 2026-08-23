package backendtest

import (
	"context"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// RunCoreConformance executes behavior shared by all backend domains. It is not
// invoked by this package itself: the production backend's integration test
// supplies its factory once that implementation exists.
func RunCoreConformance(t *testing.T, factory BackendFactory, env IntegrationEnvironment) {
	t.Helper()
	if factory == nil {
		t.Fatal("backendtest: nil BackendFactory")
	}
	if env.Timeout <= 0 {
		env.Timeout = 30 * time.Second
	}

	t.Run("startup synchronizes base snapshots", func(t *testing.T) {
		instance := openBackend(t, factory, env)
		scope := backend.RefreshScope{
			Resource: backend.ResourceContainer,
			View:     backend.ViewSummary,
		}

		ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
		defer cancel()
		meta, err := instance.GetSnapshotVersion(ctx, scope)
		if err != nil {
			t.Fatalf("get startup snapshot metadata: %v", err)
		}
		if meta.Scope != scope {
			t.Fatalf("snapshot scope = %#v, want %#v", meta.Scope, scope)
		}
		if meta.Version == 0 {
			t.Fatal("startup snapshot version must be non-zero")
		}
		if meta.RefreshedAt.IsZero() {
			t.Fatal("startup snapshot has no refresh time")
		}
		if meta.Stale {
			t.Fatal("successful startup snapshot is stale")
		}
	})

	t.Run("manual refresh returns coherent metadata", func(t *testing.T) {
		instance := openBackend(t, factory, env)
		scope := backend.RefreshScope{
			Resource: backend.ResourceContainer,
			View:     backend.ViewSummary,
		}

		ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
		defer cancel()
		before, err := instance.GetSnapshotVersion(ctx, scope)
		if err != nil {
			t.Fatalf("get metadata before refresh: %v", err)
		}
		result, err := instance.Refresh(ctx, scope, backend.RefreshManual)
		if err != nil {
			t.Fatalf("manual refresh: %v", err)
		}
		if result.Scope != scope {
			t.Fatalf("result scope = %#v, want %#v", result.Scope, scope)
		}
		if result.Version < before.Version {
			t.Fatalf("snapshot version regressed from %d to %d", before.Version, result.Version)
		}
		if result.RefreshedAt.IsZero() {
			t.Fatal("refresh result has no refresh time")
		}
	})

	t.Run("subscription context cancellation closes events", func(t *testing.T) {
		instance := openBackend(t, factory, env)
		subscriptionContext, cancelSubscription := context.WithCancel(context.Background())
		subscription, err := instance.SubscribeStateChanges(subscriptionContext, backend.EventFilter{})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		cancelSubscription()

		timer := time.NewTimer(env.Timeout)
		defer timer.Stop()
		select {
		case _, open := <-subscription.Events():
			if open {
				t.Fatal("subscription delivered an event after cancellation")
			}
		case <-timer.C:
			t.Fatal("subscription channel remained open after cancellation")
		}

		if err := subscription.Close(); err != nil {
			t.Fatalf("idempotent subscription close: %v", err)
		}
	})
}

func openBackend(t *testing.T, factory BackendFactory, env IntegrationEnvironment) backend.Backend {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
	defer cancel()

	instance, err := factory(ctx, env)
	if err != nil {
		t.Fatalf("construct backend: %v", err)
	}
	if instance == nil {
		t.Fatal("factory returned a nil backend without an error")
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), env.Timeout)
		defer closeCancel()
		if err := instance.Close(closeCtx); err != nil {
			t.Errorf("close backend: %v", err)
		}
	})
	return instance
}
