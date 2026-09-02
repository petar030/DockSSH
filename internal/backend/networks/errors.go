package networks

import (
	"context"
	"errors"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func requireNetworkID(operation, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", &backend.AppError{Code: backend.ErrorInvalidInput, Operation: operation, Resource: "network"}
	}
	return id, nil
}

func requireContainerID(operation, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", &backend.AppError{Code: backend.ErrorInvalidInput, Operation: operation, Resource: "container"}
	}
	return id, nil
}

func classifyDockerError(operation, id string, err error) error {
	if err == nil {
		return nil
	}
	var applicationError *backend.AppError
	if errors.As(err, &applicationError) {
		return err
	}
	code := backend.ErrorInternal
	switch {
	case strings.Contains(strings.ToLower(err.Error()), "active endpoints"):
		// Recent daemons report this lifecycle conflict as HTTP Forbidden.
		// Preserve the stable domain meaning used by the TUI.
		code = backend.ErrorConflict
	case errors.Is(err, context.Canceled), errdefs.IsCanceled(err):
		code = backend.ErrorCanceled
	case errors.Is(err, context.DeadlineExceeded), errdefs.IsDeadlineExceeded(err):
		code = backend.ErrorTimeout
	case errdefs.IsInvalidArgument(err):
		code = backend.ErrorInvalidInput
	case errdefs.IsNotFound(err):
		code = backend.ErrorNotFound
	case errdefs.IsConflict(err), errdefs.IsAlreadyExists(err):
		code = backend.ErrorConflict
	case errdefs.IsPermissionDenied(err):
		code = backend.ErrorPermissionDenied
	case errdefs.IsUnavailable(err):
		code = backend.ErrorDaemonUnavailable
	}
	return &backend.AppError{Code: code, Operation: operation, Resource: "network", ID: id, Err: err}
}
