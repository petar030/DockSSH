package backend

import (
	"errors"
	"fmt"
	"testing"
)

func TestRefreshScopeIsComparable(t *testing.T) {
	scope := RefreshScope{Resource: ResourceContainer, View: ViewSummary}
	set := map[RefreshScope]struct{}{scope: {}}

	if _, ok := set[scope]; !ok {
		t.Fatal("refresh scope cannot be used as a map key")
	}
}

func TestHasErrorCodeFindsWrappedApplicationError(t *testing.T) {
	cause := errors.New("connection refused")
	err := fmt.Errorf("startup: %w", &AppError{
		Code:      ErrorDaemonUnavailable,
		Operation: "docker ping",
		Err:       cause,
	})

	if !HasErrorCode(err, ErrorDaemonUnavailable) {
		t.Fatal("expected daemon-unavailable category")
	}
	if !errors.Is(err, cause) {
		t.Fatal("application error did not preserve its cause")
	}
}

func TestHasErrorCodeFindsNestedApplicationError(t *testing.T) {
	err := &AppError{Code: ErrorInternal, Err: &AppError{Code: ErrorNotFound}}
	if !HasErrorCode(err, ErrorNotFound) {
		t.Fatal("expected nested not-found category")
	}
}
