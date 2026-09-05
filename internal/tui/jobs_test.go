package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

type fakeJob struct {
	id       string
	progress chan backend.ProgressEvent
	result   backend.JobResult
	err      error
	canceled bool
}

func newFakeJob(id string) *fakeJob {
	return &fakeJob{id: id, progress: make(chan backend.ProgressEvent, 4), result: backend.JobResult{JobID: id}}
}
func (job *fakeJob) ID() string                                      { return job.id }
func (job *fakeJob) Progress() <-chan backend.ProgressEvent          { return job.progress }
func (job *fakeJob) Wait(context.Context) (backend.JobResult, error) { return job.result, job.err }
func (job *fakeJob) Cancel() error                                   { job.canceled = true; return nil }

func TestJobTrackerProgressCompletionAndDuplicateRegistration(t *testing.T) {
	tracker := newJobTracker(context.Background())
	job := newFakeJob("job-123")
	job.progress <- backend.ProgressEvent{Status: "unpacking", Message: "layer"}
	command := tracker.Register(job, "pull image")
	if tracker.Register(job, "pull image") != nil || len(tracker.order) != 1 {
		t.Fatal("duplicate job was registered")
	}
	batch := command().(tea.BatchMsg)
	progress := batch[0]().(jobProgressMsg)
	_ = tracker.Update(progress)
	if tracker.jobs[job.id].progress.Status != "unpacking" {
		t.Fatal("job progress was not retained")
	}
	finished := batch[1]().(jobFinishedMsg)
	tracker.Update(finished)
	if !tracker.jobs[job.id].done || tracker.Summary() != "" {
		t.Fatal("job terminal result was not retained")
	}
}

func TestJobTrackerFailureAndConfirmedCancel(t *testing.T) {
	tracker := newJobTracker(context.Background())
	job := newFakeJob("job-fail")
	job.err = errors.New("pull failed")
	command := tracker.Register(job, "pull image")
	batch := command().(tea.BatchMsg)
	tracker.Update(batch[1]())
	if tracker.jobs[job.id].err == nil {
		t.Fatal("job failure was lost")
	}
	if summary := tracker.Summary(); summary != "" {
		t.Fatalf("completed job must not occupy the footer: %q", summary)
	}
	if view := tracker.View(80); !strings.Contains(view, "Error:") || !strings.Contains(view, "pull failed") {
		t.Fatalf("job error is not readable in the jobs overlay: %s", view)
	}
	if prompt := tracker.FailurePrompt(80); !strings.Contains(prompt, "JOB FAILED") || !strings.Contains(prompt, "pull failed") {
		t.Fatalf("job failure prompt is missing: %s", prompt)
	}
	tracker.HandleKey("esc")
	if tracker.FailurePrompt(80) != "" {
		t.Fatal("failure prompt did not dismiss")
	}

	running := newFakeJob("job-running")
	_ = tracker.Register(running, "pull image")
	tracker.Open()
	tracker.HandleKey("c")
	if !tracker.confirmCancel || running.canceled {
		t.Fatal("cancel did not require confirmation")
	}
	cancel := tracker.HandleKey("y")
	tracker.Update(cancel())
	if !running.canceled {
		t.Fatal("confirmed cancel did not reach job handle")
	}
}

func TestJobTrackerOutlivesPageAndSessionContextEndsOnlyWatching(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tracker := newJobTracker(ctx)
	job := newFakeJob("job-live")
	if tracker.Register(job, "pull image") == nil {
		t.Fatal("job was not registered")
	}
	// A page tab change has no tracker operation, so the handle remains.
	if !tracker.Has(job.id) {
		t.Fatal("tab-independent tracker lost job")
	}
	cancel()
	time.Sleep(time.Millisecond)
	if !tracker.Has(job.id) {
		t.Fatal("session watcher cleanup removed retained job state")
	}
}
