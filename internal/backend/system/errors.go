package system

import (
	"context"
	"errors"

	"github.com/containerd/errdefs"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func classifyDockerError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var applicationError *backend.AppError
	if errors.As(err, &applicationError) {
		return err
	}
	code := backend.ErrorInternal
	switch {
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
	return &backend.AppError{Code: code, Operation: operation, Resource: "system", Err: err}
}
