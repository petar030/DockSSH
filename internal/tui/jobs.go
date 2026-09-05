package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

type trackedJob struct {
	id        string
	operation string
	handle    backend.Job
	progress  backend.ProgressEvent
	result    backend.JobResult
	err       error
	done      bool
}

type jobProgressMsg struct {
	id       string
	progress backend.ProgressEvent
	open     bool
}

type jobFinishedMsg struct {
	id     string
	result backend.JobResult
	err    error
}

type jobCancelFinishedMsg struct {
	id  string
	err error
}

// jobTracker owns the handles for jobs started by one SSH session. Page
// deactivation never touches it; the session context only stops local watching.
type jobTracker struct {
	ctx           context.Context
	jobs          map[string]*trackedJob
	order         []string
	overlay       bool
	confirmCancel bool
	failureID     string
	selected      int
	notice        string
}

func newJobTracker(ctx context.Context) *jobTracker {
	if ctx == nil {
		ctx = context.Background()
	}
	return &jobTracker{ctx: ctx, jobs: make(map[string]*trackedJob)}
}

// Register adds an accepted backend job and starts one progress receive and one
// reliable terminal wait. Duplicate handles are ignored by ID.
func (tracker *jobTracker) Register(job backend.Job, operation string) tea.Cmd {
	if tracker == nil || job == nil || strings.TrimSpace(job.ID()) == "" {
		return nil
	}
	id := job.ID()
	if _, exists := tracker.jobs[id]; exists {
		return nil
	}
	tracker.jobs[id] = &trackedJob{id: id, operation: operation, handle: job}
	tracker.order = append(tracker.order, id)
	tracker.selected = len(tracker.order) - 1
	return tea.Batch(waitJobProgress(job), waitJob(job, tracker.ctx))
}

func (tracker *jobTracker) Has(id string) bool {
	if tracker == nil {
		return false
	}
	_, ok := tracker.jobs[id]
	return ok
}

func waitJobProgress(job backend.Job) tea.Cmd {
	return func() tea.Msg {
		progress, open := <-job.Progress()
		return jobProgressMsg{id: job.ID(), progress: progress, open: open}
	}
}

func waitJob(job backend.Job, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		result, err := job.Wait(ctx)
		return jobFinishedMsg{id: job.ID(), result: result, err: err}
	}
}

func (tracker *jobTracker) Update(message tea.Msg) tea.Cmd {
	switch message := message.(type) {
	case jobProgressMsg:
		job := tracker.jobs[message.id]
		if job == nil || job.done || !message.open {
			return nil
		}
		job.progress = message.progress
		return waitJobProgress(job.handle)
	case jobFinishedMsg:
		job := tracker.jobs[message.id]
		if job == nil || job.done {
			return nil
		}
		job.result, job.err, job.done = message.result, message.err, true
		if message.err != nil {
			tracker.selected = indexOfJob(tracker.order, message.id)
			tracker.failureID = message.id
		}
	case jobCancelFinishedMsg:
		if message.err != nil {
			tracker.notice = "Cancel failed: " + message.err.Error()
		} else {
			tracker.notice = "Cancellation requested"
		}
	}
	return nil
}

func (tracker *jobTracker) CapturesInput() bool {
	return tracker != nil && (tracker.overlay || tracker.failureID != "")
}

func (tracker *jobTracker) HandleKey(key string) tea.Cmd {
	if tracker == nil {
		return nil
	}
	if tracker.failureID != "" {
		switch key {
		case "J":
			tracker.failureID = ""
			tracker.overlay = true
		case "enter", "esc":
			tracker.failureID = ""
		}
		return nil
	}
	if !tracker.overlay {
		return nil
	}
	if tracker.confirmCancel {
		switch key {
		case "esc", "n":
			tracker.confirmCancel = false
		case "y", "enter":
			tracker.confirmCancel = false
			job := tracker.selectedJob()
			if job == nil || job.done {
				return nil
			}
			return func() tea.Msg { return jobCancelFinishedMsg{id: job.id, err: job.handle.Cancel()} }
		}
		return nil
	}
	switch key {
	case "esc", "J":
		tracker.overlay = false
	case "up", "k":
		tracker.selected = max(tracker.selected-1, 0)
	case "down", "j":
		tracker.selected = min(tracker.selected+1, max(len(tracker.order)-1, 0))
	case "c":
		if job := tracker.selectedJob(); job != nil && !job.done {
			tracker.confirmCancel = true
		}
	}
	return nil
}

func (tracker *jobTracker) Open() { tracker.overlay = true }

func (tracker *jobTracker) selectedJob() *trackedJob {
	if len(tracker.order) == 0 || tracker.selected < 0 || tracker.selected >= len(tracker.order) {
		return nil
	}
	return tracker.jobs[tracker.order[tracker.selected]]
}

func (tracker *jobTracker) Summary() string {
	if tracker == nil || len(tracker.order) == 0 {
		return ""
	}
	newest := tracker.jobs[tracker.order[len(tracker.order)-1]]
	operation := strings.TrimSpace(newest.operation)
	if !newest.done {
		state := strings.TrimSpace(newest.progress.Status)
		if state == "" {
			state = "starting"
		}
		message := strings.TrimSpace(newest.progress.Message)
		if message != "" {
			state += " — " + message
		}
		return ui.Truncate(operation+": "+ui.SanitizeLine(state)+" · J details", 90)
	}
	return ""
}

func (tracker *jobTracker) FailurePrompt(width int) string {
	if tracker == nil || tracker.failureID == "" {
		return ""
	}
	job := tracker.jobs[tracker.failureID]
	if job == nil || job.err == nil {
		return ""
	}
	modalWidth := max(min(width-12, 92), 48)
	lines := []string{
		"Operation: " + ui.SanitizeLine(job.operation),
		"",
		"Error:",
	}
	lines = append(lines, wrapJobText(job.err.Error(), modalWidth-6)...)
	lines = append(lines, "", "enter/esc dismiss   J open job details")
	return lipgloss.NewStyle().Width(modalWidth).Padding(0, 1).
		Border(lipgloss.RoundedBorder()).BorderForeground(ui.Warning).
		Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Warning).Render("JOB FAILED") + "\n" + strings.Join(lines, "\n"))
}

func indexOfJob(ids []string, target string) int {
	for index, id := range ids {
		if id == target {
			return index
		}
	}
	return 0
}

func (tracker *jobTracker) View(width int) string {
	modalWidth := max(min(width-12, 100), 48)
	lines := []string{}
	if len(tracker.order) == 0 {
		lines = append(lines, "No jobs were started by this session.")
	} else {
		for index, id := range tracker.order {
			job := tracker.jobs[id]
			marker := " "
			if index == tracker.selected {
				marker = ">"
			}
			state := "running"
			if job.done {
				state = "completed"
				if job.err != nil {
					state = "failed"
				}
			} else if job.progress.Status != "" {
				state = job.progress.Status
			}
			line := fmt.Sprintf("%s %-18s %-14s %s", marker, job.operation, shortJobID(job.id), state)
			lines = append(lines, ui.Truncate(ui.SanitizeLine(line), modalWidth-4))
		}
		if job := tracker.selectedJob(); job != nil {
			lines = append(lines, "", "SELECTED JOB", "Operation: "+ui.SanitizeLine(job.operation), "Status: "+jobState(job))
			if job.progress.Message != "" {
				lines = append(lines, "Progress: "+ui.Truncate(ui.SanitizeLine(job.progress.Message), modalWidth-14))
			}
			if job.err != nil {
				lines = append(lines, "Error:")
				lines = append(lines, wrapJobText(job.err.Error(), modalWidth-6)...)
			}
		}
	}
	if tracker.notice != "" {
		lines = append(lines, "", ui.ErrorNotice(tracker.notice, modalWidth-4))
	}
	footer := "j/k select   c cancel running job   esc close"
	if tracker.confirmCancel {
		footer = "Cancel selected job?   y/enter confirm   n/esc cancel"
	}
	lines = append(lines, "", footer)
	panel := lipgloss.NewStyle().Width(modalWidth).Padding(0, 1).
		Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).
		Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render("SESSION JOBS") + "\n" + strings.Join(lines, "\n"))
	return panel
}

func jobState(job *trackedJob) string {
	if job == nil {
		return "unknown"
	}
	if job.done {
		if job.err != nil {
			return "failed"
		}
		return "completed"
	}
	if status := strings.TrimSpace(job.progress.Status); status != "" {
		return status
	}
	return "running"
}

// wrapJobText deliberately keeps errors readable in the jobs overlay without
// adding a separate error screen.
func wrapJobText(text string, width int) []string {
	text = ui.SanitizeLine(text)
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{"  —"}
	}
	width = max(width, 12)
	lines, line := make([]string, 0, 3), "  "
	for _, word := range words {
		candidate := strings.TrimSpace(line + " " + word)
		if len(line) > 2 && lipgloss.Width(candidate) > width {
			lines = append(lines, ui.Truncate(line, width))
			line = "  " + word
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, ui.Truncate(line, width))
	}
	return lines
}

func shortJobID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
