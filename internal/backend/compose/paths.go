package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

var managedProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func normalizeAllowedRoots(values []string) ([]string, error) {
	roots := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		root, err := canonicalPath(value)
		if err != nil {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "configure Compose root", ID: value, Err: err}
		}
		roots = append(roots, root)
	}
	return roots, nil
}

func (api *API) validateConfigFiles(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "load Compose project", Resource: "config files"}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		path, err := canonicalPath(value)
		if err != nil {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "load Compose config", ID: value, Err: err}
		}
		if !withinAnyRoot(path, api.roots) {
			return nil, &backend.AppError{Code: backend.ErrorPermissionDenied, Operation: "load Compose config", ID: path}
		}
		result = append(result, path)
	}
	return result, nil
}

func canonicalPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func withinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (api *API) managedConfigPath(projectName string) (string, string, error) {
	projectName = strings.TrimSpace(projectName)
	if !managedProjectName.MatchString(projectName) {
		return "", "", &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "select managed Compose configuration",
			Resource: "compose project", ID: projectName,
			Err: fmt.Errorf("use lowercase letters, digits, hyphens or underscores, beginning with a letter or digit"),
		}
	}
	if len(api.roots) == 0 {
		return "", "", &backend.AppError{
			Code: backend.ErrorPermissionDenied, Operation: "select managed Compose configuration",
			Resource: "configured Compose root", ID: projectName,
		}
	}
	root := api.roots[0]
	path := filepath.Join(root, projectName, "compose.yaml")
	if !withinAnyRoot(path, []string{root}) {
		return "", "", &backend.AppError{
			Code: backend.ErrorPermissionDenied, Operation: "select managed Compose configuration", ID: projectName,
		}
	}
	return projectName, path, nil
}

func openManagedRoot(root string) (*os.Root, error) {
	value, err := os.OpenRoot(root)
	if err != nil {
		return nil, &backend.AppError{
			Code: backend.ErrorPermissionDenied, Operation: "open configured Compose root", ID: root, Err: err,
		}
	}
	return value, nil
}
