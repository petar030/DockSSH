package backend

import (
	"errors"
	"fmt"
	"testing"
)

func TestRefreshKeyIsValidAndComparable(t *testing.T) {
	key := RefreshKey{Kind: "containers.list"}
	set := map[RefreshKey]struct{}{key: {}}

	if !key.Valid() {
		t.Fatal("named refresh key is invalid")
	}
	if _, ok := set[key]; !ok {
		t.Fatal("refresh key cannot be used as a map key")
	}
	if (RefreshKey{}).Valid() {
		t.Fatal("empty refresh key is valid")
	}
}

func TestPageValidation(t *testing.T) {
	for _, page := range []Page{
		PageDashboard, PageContainers, PageCompose, PageImages,
		PageVolumes, PageNetworks, PageEvents, PageSystem,
	} {
		if !page.Valid() {
			t.Fatalf("page %q is invalid", page)
		}
	}
	if Page("unknown").Valid() {
		t.Fatal("unknown page is valid")
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
