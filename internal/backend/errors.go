package backend

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable category suitable for tests and user-facing rendering.
type ErrorCode string

const (
	ErrorInvalidInput        ErrorCode = "invalid_input"
	ErrorNotFound            ErrorCode = "not_found"
	ErrorConflict            ErrorCode = "conflict"
	ErrorPermissionDenied    ErrorCode = "permission_denied"
	ErrorDaemonUnavailable   ErrorCode = "daemon_unavailable"
	ErrorTimeout             ErrorCode = "timeout"
	ErrorCanceled            ErrorCode = "canceled"
	ErrorSnapshotUnavailable ErrorCode = "snapshot_unavailable"
	ErrorStreamClosed        ErrorCode = "stream_closed"
	ErrorUnsupported         ErrorCode = "unsupported"
	ErrorInternal            ErrorCode = "internal"
)

// AppError wraps an implementation-specific error with a stable category.
type AppError struct {
	Code      ErrorCode
	Operation string
	Resource  ResourceType
	ID        string
	Err       error
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}

	message := string(e.Code)
	if e.Operation != "" {
		message = e.Operation + ": " + message
	}
	if e.Resource != "" {
		message += fmt.Sprintf(" (%s", e.Resource)
		if e.ID != "" {
			message += ":" + e.ID
		}
		message += ")"
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// HasErrorCode reports whether err or any wrapped AppError has code.
func HasErrorCode(err error, code ErrorCode) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Code == code
}
