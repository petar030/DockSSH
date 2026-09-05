package compose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const validConfig = "services:\n  app:\n    image: nginx:latest\n"

func TestManagedConfigPathUsesFirstRootAndRejectsUnsafeNames(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	api := newTestAPI(t, &fakeComposeClient{}, &recordingRefreshRequester{}, first, second)
	path, err := api.ConfigPath("project-one")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(first, "project-one", "compose.yaml")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	for _, name := range []string{"", ".", "..", "../escape", "/absolute", "a/b", "Uppercase", "space here"} {
		if _, err := api.ConfigPath(name); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("ConfigPath(%q) error = %v", name, err)
		}
	}
	withoutRoot := newTestAPI(t, &fakeComposeClient{}, &recordingRefreshRequester{})
	if _, err := withoutRoot.ConfigPath("demo"); !backend.HasErrorCode(err, backend.ErrorPermissionDenied) {
		t.Fatalf("missing root error = %v", err)
	}
}

func TestSaveAndReadManagedConfigAtomically(t *testing.T) {
	root := t.TempDir()
	api := newTestAPI(t, &fakeComposeClient{}, &recordingRefreshRequester{}, root)
	result, err := api.SaveConfig(context.Background(), SaveConfigOptions{ProjectName: "demo", Content: validConfig})
	if err != nil {
		t.Fatal(err)
	}
	if result.OperationID != "compose.config.save" || len(result.RefreshKeys) != 1 || result.RefreshKeys[0].Kind != RefreshKindList {
		t.Fatalf("command result = %#v", result)
	}
	document, err := api.ReadConfig(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if document.Content != validConfig || document.Path != filepath.Join(root, "demo", "compose.yaml") {
		t.Fatalf("document = %#v", document)
	}
	info, err := os.Stat(document.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	replacement := "services:\n  worker:\n    image: nginx:latest\n"
	if _, err := api.SaveConfig(context.Background(), SaveConfigOptions{ProjectName: "demo", Content: replacement}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(document.Path)
	if err != nil || string(content) != replacement {
		t.Fatalf("replacement = %q, err=%v", content, err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "demo", ".compose-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %v, err=%v", matches, err)
	}
}

func TestSaveRejectsInvalidComposeBeforeWriting(t *testing.T) {
	root := t.TempDir()
	api := newTestAPI(t, &fakeComposeClient{}, &recordingRefreshRequester{}, root)
	for _, content := range []string{"", "services: [", "services: []\n", "unknown-key: true\n"} {
		_, err := api.SaveConfig(context.Background(), SaveConfigOptions{ProjectName: "demo", Content: content})
		if !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("content %q error = %v", content, err)
		}
		if _, statErr := os.Stat(filepath.Join(root, "demo")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid content created a project directory: %v", statErr)
		}
	}
}

func TestManagedConfigRejectsSymlinkBoundaryAndNonRegularTarget(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	api := newTestAPI(t, &fakeComposeClient{}, &recordingRefreshRequester{}, root)
	if _, err := api.SaveConfig(context.Background(), SaveConfigOptions{ProjectName: "linked", Content: validConfig}); !backend.HasErrorCode(err, backend.ErrorPermissionDenied) {
		t.Fatalf("symlink project error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "compose.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside target was touched: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, "blocked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "blocked", "compose.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := api.SaveConfig(context.Background(), SaveConfigOptions{ProjectName: "blocked", Content: validConfig}); !backend.HasErrorCode(err, backend.ErrorPermissionDenied) {
		t.Fatalf("directory target error = %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "blocked", ".compose-*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("failed save left temporary files: %v", matches)
	}
}

func TestReadManagedConfigErrorsAreStableAndBounded(t *testing.T) {
	api := newTestAPI(t, &fakeComposeClient{}, &recordingRefreshRequester{}, t.TempDir())
	if _, err := api.ReadConfig(context.Background(), "missing"); !backend.HasErrorCode(err, backend.ErrorNotFound) {
		t.Fatalf("missing file error = %v", err)
	}
	if _, err := api.ReadConfig(nil, "demo"); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
		t.Fatalf("nil context error = %v", err)
	}
	longInvalid := "services: [" + strings.Repeat("x", 2000)
	_, err := api.SaveConfig(context.Background(), SaveConfigOptions{ProjectName: "demo", Content: longInvalid})
	if err == nil || len(err.Error()) > 700 {
		t.Fatalf("unbounded diagnostic length = %d: %v", len(err.Error()), err)
	}
}

func TestConcurrentManagedSavesProduceOneCompleteDocument(t *testing.T) {
	root := t.TempDir()
	api := newTestAPI(t, &fakeComposeClient{}, &recordingRefreshRequester{}, root)
	first := "services:\n  first:\n    image: nginx:latest\n"
	second := "services:\n  second:\n    image: nginx:latest\n"
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for _, content := range []string{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := api.SaveConfig(context.Background(), SaveConfigOptions{ProjectName: "concurrent", Content: content})
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(root, "concurrent", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != first && string(content) != second {
		t.Fatalf("concurrent save produced partial content: %q", content)
	}
}
