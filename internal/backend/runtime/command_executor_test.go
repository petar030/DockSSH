package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestCommandExecutorOwnsAcceptedCommandAfterCallerCancellation(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 2)}
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Refreshes: refreshes, Workers: 1, QueueCapacity: 2, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new Command Executor: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close(context.Background()) })

	started := make(chan context.Context, 1)
	release := make(chan struct{})
	waitCtx, cancelWait := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := executor.Run(waitCtx, testCommand("one", func(ctx context.Context) error {
			started <- ctx
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}))
		done <- runErr
	}()
	operationContext := receiveWithin(t, started)
	cancelWait()
	if err := receiveWithin(t, done); !backend.HasErrorCode(err, backend.ErrorCanceled) {
		t.Fatalf("caller result = %v", err)
	}
	if operationContext.Err() != nil {
		t.Fatalf("caller cancellation reached operation context: %v", operationContext.Err())
	}
	close(release)
	request := receiveWithin(t, refreshes.requests)
	if request.key.Kind != "containers.list" || request.reason != backend.RefreshCommand {
		t.Fatalf("refresh request = %#v", request)
	}
}

func TestCommandExecutorUsesBoundedFIFOQueueAndFixedWorkers(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 8)}
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Refreshes: refreshes, Workers: 1, QueueCapacity: 2, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new Command Executor: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close(context.Background()) })

	started := make(chan string, 3)
	release := make(chan struct{}, 3)
	results := make(chan string, 3)
	for _, id := range []string{"one", "two", "three"} {
		id := id
		go func() {
			result, runErr := executor.Run(context.Background(), testCommand(id, func(ctx context.Context) error {
				started <- id
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}))
			if runErr != nil {
				results <- "error:" + id
				return
			}
			results <- result.OperationID
		}()
		if id == "one" {
			if got := receiveWithin(t, started); got != "one" {
				t.Fatalf("first command = %q", got)
			}
		} else {
			wantQueued := 1
			if id == "three" {
				wantQueued = 2
			}
			deadline := time.Now().Add(time.Second)
			for len(executor.queue) != wantQueued {
				if time.Now().After(deadline) {
					t.Fatalf("%s was not queued", id)
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	for _, want := range []string{"two", "three"} {
		release <- struct{}{}
		if got := receiveWithin(t, started); got != want {
			t.Fatalf("next command = %q, want %q", got, want)
		}
	}
	release <- struct{}{}
	for range 3 {
		if result := receiveWithin(t, results); len(result) < len("command.") || result[:len("command.")] != "command." {
			t.Fatalf("command result = %q", result)
		}
	}
}

func TestCommandExecutorRefreshesAfterAttemptedMutationError(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 1)}
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Refreshes: refreshes, Workers: 1, QueueCapacity: 1, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new Command Executor: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close(context.Background()) })
	wantErr := errors.New("ambiguous Docker failure")
	_, err = executor.Run(context.Background(), testCommand("failed", func(context.Context) error {
		return wantErr
	}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("command error = %v", err)
	}
	request := receiveWithin(t, refreshes.requests)
	if request.key.Kind != "containers.list" || request.reason != backend.RefreshCommand {
		t.Fatalf("refresh after error = %#v", request)
	}
}

func TestCommandExecutorRejectsOverflowAndCancelsOnShutdown(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 2)}
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Refreshes: refreshes, Workers: 1, QueueCapacity: 1, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new Command Executor: %v", err)
	}
	started := make(chan struct{})
	activeDone := make(chan error, 1)
	go func() {
		_, runErr := executor.Run(context.Background(), testCommand("active", func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}))
		activeDone <- runErr
	}()
	receiveWithin(t, started)
	queuedDone := make(chan error, 1)
	go func() {
		_, runErr := executor.Run(context.Background(), testCommand("queued", func(context.Context) error { return nil }))
		queuedDone <- runErr
	}()
	deadline := time.Now().Add(time.Second)
	for len(executor.queue) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("second command was not queued")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := executor.Run(context.Background(), testCommand("overflow", func(context.Context) error { return nil })); !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("overflow result = %v", err)
	}
	if err := executor.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := receiveWithin(t, activeDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("active command result = %v", err)
	}
	if err := receiveWithin(t, queuedDone); !backend.HasErrorCode(err, backend.ErrorCanceled) {
		t.Fatalf("queued command result = %v", err)
	}
}

func testCommand(id string, run func(context.Context) error) backend.CommandRequest {
	return backend.CommandRequest{
		OperationID: "command." + id,
		Operation:   "run " + id,
		Affected:    []backend.AffectedResource{{Kind: "container", ID: id}},
		RefreshKeys: []backend.RefreshKey{{Kind: "containers.list"}},
		Run:         run,
	}
}

type refreshRequest struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type recordingRefreshRequester struct {
	mu       sync.Mutex
	requests chan refreshRequest
	err      error
}

func (requester *recordingRefreshRequester) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	requester.mu.Lock()
	err := requester.err
	requester.mu.Unlock()
	requester.requests <- refreshRequest{key: key, reason: reason}
	return err
}
