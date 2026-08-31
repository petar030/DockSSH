package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
)

func TestJobExecutorOwnsAcceptedOperationAndRefreshesOnCompletion(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 1)}
	executor, err := NewJobExecutor(JobExecutorConfig{Refreshes: refreshes, Publisher: eventhub.New(eventhub.Config{}), Capacity: 1})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close(context.Background()) })

	started := make(chan struct{})
	release := make(chan struct{})
	submitCtx, cancelSubmit := context.WithCancel(context.Background())
	job, err := executor.Start(submitCtx, jobRequest("demo", func(ctx context.Context, report func(backend.ProgressEvent)) error {
		close(started)
		select {
		case <-release:
			report(backend.ProgressEvent{Status: "working", Current: 1, Total: 1})
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	if err != nil {
		t.Fatalf("start job: %v", err)
	}
	<-started
	cancelSubmit()
	close(release)
	result, err := job.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if result.JobID != job.ID() || len(result.RefreshKeys) != 1 || result.CompletedAt.IsZero() {
		t.Fatalf("result = %#v", result)
	}
	request := <-refreshes.requests
	if request.reason != backend.RefreshJobCompleted || request.key.Kind != "compose.projects" {
		t.Fatalf("refresh = %#v", request)
	}
	var statuses []string
	for event := range job.Progress() {
		statuses = append(statuses, event.Status)
	}
	if len(statuses) < 3 || statuses[0] != "started" || statuses[len(statuses)-1] != "completed" {
		t.Fatalf("progress statuses = %#v", statuses)
	}
}

func TestJobExecutorRejectsConflictingProjectJob(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 2)}
	executor, err := NewJobExecutor(JobExecutorConfig{Refreshes: refreshes, Publisher: eventhub.New(eventhub.Config{}), Capacity: 2})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close(context.Background()) })

	release := make(chan struct{})
	first, err := executor.Start(context.Background(), jobRequest("demo", func(ctx context.Context, _ func(backend.ProgressEvent)) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	if err != nil {
		t.Fatalf("start first job: %v", err)
	}
	_, err = executor.Start(context.Background(), jobRequest("demo", func(context.Context, func(backend.ProgressEvent)) error { return nil }))
	if !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("second job error = %v", err)
	}
	close(release)
	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatalf("wait first: %v", err)
	}
}

func TestJobCancelStopsBackendOperation(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 1)}
	executor, err := NewJobExecutor(JobExecutorConfig{Refreshes: refreshes, Publisher: eventhub.New(eventhub.Config{})})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close(context.Background()) })

	job, err := executor.Start(context.Background(), jobRequest("demo", func(ctx context.Context, _ func(backend.ProgressEvent)) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatalf("start job: %v", err)
	}
	if err := job.Cancel(); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = job.Wait(waitCtx)
	if !backend.HasErrorCode(err, backend.ErrorCanceled) {
		t.Fatalf("wait error = %v", err)
	}
}

func TestJobProgressAndCompletionAreBroadcastOnOwningPage(t *testing.T) {
	refreshes := &recordingRefreshRequester{requests: make(chan refreshRequest, 1)}
	hub := eventhub.New(eventhub.Config{})
	subscription, err := hub.Subscribe(context.Background(), backend.PageCompose, backend.EventFilter{
		Types: []backend.EventType{backend.EventJobProgressed, backend.EventJobFinished},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	executor, err := NewJobExecutor(JobExecutorConfig{Refreshes: refreshes, Publisher: hub})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close(context.Background()) })

	job, err := executor.Start(context.Background(), jobRequest("demo", func(_ context.Context, report func(backend.ProgressEvent)) error {
		report(backend.ProgressEvent{Status: "working"})
		return nil
	}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := job.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	foundProgress, foundFinished := false, false
	for !foundProgress || !foundFinished {
		select {
		case event := <-subscription.Events():
			switch payload := event.Payload.(type) {
			case backend.JobProgressed:
				foundProgress = foundProgress || payload.JobID == job.ID()
			case backend.JobFinished:
				foundFinished = payload.Result.JobID == job.ID() && payload.Err == nil
			}
		case <-time.After(time.Second):
			t.Fatalf("job events progress=%v finished=%v", foundProgress, foundFinished)
		}
	}
}

func jobRequest(project string, run func(context.Context, func(backend.ProgressEvent)) error) backend.JobRequest {
	return backend.JobRequest{
		Page: backend.PageCompose, OperationID: "compose.up", Operation: "up Compose project", ConflictKey: "compose:" + project,
		Affected:    []backend.AffectedResource{{Kind: "compose_project", ID: project}},
		RefreshKeys: []backend.RefreshKey{{Kind: "compose.projects"}}, Run: run,
	}
}
