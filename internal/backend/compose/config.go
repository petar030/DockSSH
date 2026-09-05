package compose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/compose-spec/compose-go/v2/schema"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"go.yaml.in/yaml/v4"
)

const maxConfigBytes = 1 << 20

func validateConfigContent(content string) error {
	if strings.TrimSpace(content) == "" || len(content) > maxConfigBytes {
		return configInputError(errors.New("configuration must contain between 1 byte and 1 MiB"))
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return configInputError(err)
	}
	if err := schema.Validate(document); err != nil {
		return configInputError(err)
	}
	return nil
}

func configInputError(err error) error {
	return &backend.AppError{
		Code: backend.ErrorInvalidInput, Operation: "validate Compose configuration",
		Resource: "compose.yaml", Err: errors.New(boundedDiagnostic(err)),
	}
}

func boundedDiagnostic(err error) string {
	if err == nil {
		return "invalid configuration"
	}
	value := strings.Map(func(value rune) rune {
		if unicode.IsControl(value) && value != '\n' && value != '\t' {
			return -1
		}
		return value
	}, err.Error())
	value = strings.Join(strings.Fields(value), " ")
	const limit = 512
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit]) + "…"
	}
	return value
}

func (api *API) writeConfig(projectName, content string) error {
	projectName, _, err := api.managedConfigPath(projectName)
	if err != nil {
		return err
	}
	if err := validateConfigContent(content); err != nil {
		return err
	}
	root, err := openManagedRoot(api.roots[0])
	if err != nil {
		return err
	}
	defer root.Close()

	if err := root.Mkdir(projectName, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return configWriteError(projectName, err)
	}
	projectInfo, err := root.Lstat(projectName)
	if err != nil || !projectInfo.IsDir() || projectInfo.Mode()&os.ModeSymlink != 0 {
		return configWriteError(projectName, errors.Join(err, errors.New("project path must be a real directory")))
	}
	target := filepath.Join(projectName, "compose.yaml")
	if info, statErr := root.Lstat(target); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return configWriteError(projectName, errors.New("compose.yaml must be a regular file"))
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return configWriteError(projectName, statErr)
	}

	temporary, temporaryName, err := createManagedTemp(root, projectName)
	if err != nil {
		return configWriteError(projectName, err)
	}
	defer root.Remove(temporaryName)
	if _, err = io.WriteString(temporary, content); err == nil {
		err = temporary.Sync()
	}
	err = errors.Join(err, temporary.Close())
	if err != nil {
		return configWriteError(projectName, err)
	}
	if err := root.Rename(temporaryName, target); err != nil {
		return configWriteError(projectName, err)
	}
	return nil
}

func createManagedTemp(root *os.Root, projectName string) (*os.File, string, error) {
	for range 10 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := filepath.Join(projectName, ".compose-"+hex.EncodeToString(random[:])+".tmp")
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("could not allocate temporary Compose file")
}

func (api *API) readConfig(ctx context.Context, projectName string) (ConfigDocument, error) {
	if ctx == nil {
		return ConfigDocument{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "read Compose configuration"}
	}
	projectName, path, err := api.managedConfigPath(projectName)
	if err != nil {
		return ConfigDocument{}, err
	}
	if err := ctx.Err(); err != nil {
		return ConfigDocument{}, &backend.AppError{Code: backend.ErrorCanceled, Operation: "read Compose configuration", Err: err}
	}
	root, err := openManagedRoot(api.roots[0])
	if err != nil {
		return ConfigDocument{}, err
	}
	defer root.Close()
	relative := filepath.Join(projectName, "compose.yaml")
	info, err := root.Lstat(relative)
	if err != nil {
		code := backend.ErrorInternal
		if errors.Is(err, os.ErrNotExist) {
			code = backend.ErrorNotFound
		}
		return ConfigDocument{}, &backend.AppError{Code: code, Operation: "read Compose configuration", Resource: "compose.yaml", ID: projectName, Err: err}
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ConfigDocument{}, &backend.AppError{Code: backend.ErrorPermissionDenied, Operation: "read Compose configuration", Resource: "compose.yaml", ID: projectName}
	}
	file, err := root.Open(relative)
	if err != nil {
		return ConfigDocument{}, configReadError(projectName, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return ConfigDocument{}, configReadError(projectName, err)
	}
	if len(content) > maxConfigBytes {
		return ConfigDocument{}, configInputError(fmt.Errorf("configuration exceeds 1 MiB"))
	}
	return ConfigDocument{ProjectName: projectName, Path: path, Content: string(content)}, nil
}

func (api *API) managedProjects() ([]ProjectSummary, error) {
	if len(api.roots) == 0 {
		return nil, nil
	}
	root, err := openManagedRoot(api.roots[0])
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	projects := make([]ProjectSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !managedProjectName.MatchString(entry.Name()) {
			continue
		}
		relative := filepath.Join(entry.Name(), "compose.yaml")
		info, statErr := root.Lstat(relative)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		projects = append(projects, ProjectSummary{
			Name: entry.Name(), Status: "not started",
			ConfigFiles: []string{filepath.Join(api.roots[0], relative)},
		})
	}
	return projects, nil
}

func configWriteError(projectName string, err error) error {
	return &backend.AppError{Code: backend.ErrorPermissionDenied, Operation: "save Compose configuration", Resource: "compose.yaml", ID: projectName, Err: err}
}

func configReadError(projectName string, err error) error {
	return &backend.AppError{Code: backend.ErrorPermissionDenied, Operation: "read Compose configuration", Resource: "compose.yaml", ID: projectName, Err: err}
}
