package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// StoredSnapshot is the immutable serialized representation held by
// StateStore. Data is always copied on reads.
type StoredSnapshot struct {
	Data json.RawMessage
	Meta SnapshotMeta
}

type snapshotEntry struct {
	data  []byte
	meta  SnapshotMeta
	valid bool
}

// StateStore holds atomically replaced, versioned JSON projections. Docker and
// event publication are deliberately outside this type.
type StateStore struct {
	mu      sync.RWMutex
	entries map[RefreshScope]snapshotEntry
}

func NewStateStore() *StateStore {
	return &StateStore{entries: make(map[RefreshScope]snapshotEntry)}
}

// Store atomically records a successful refresh. Versions increment only when
// the canonical JSON projection changes.
func (store *StateStore) store(
	scope RefreshScope,
	data any,
	reason RefreshReason,
	refreshedAt time.Time,
) (RefreshResult, error) {
	if err := validateScope(scope); err != nil {
		return RefreshResult{}, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return RefreshResult{}, &AppError{
			Code:      ErrorInvalidInput,
			Operation: "encode snapshot",
			Resource:  scope.Resource,
			ID:        scope.ID,
			Err:       err,
		}
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	previous := store.entries[scope]
	changed := !previous.valid || !bytes.Equal(previous.data, encoded)
	version := previous.meta.Version
	if changed {
		version++
	}
	meta := SnapshotMeta{
		Scope:       scope,
		Version:     version,
		RefreshedAt: refreshedAt,
		Reason:      reason,
	}
	store.entries[scope] = snapshotEntry{
		data:  append([]byte(nil), encoded...),
		meta:  meta,
		valid: true,
	}
	return RefreshResult{
		Scope:       scope,
		Version:     version,
		Changed:     changed,
		RefreshedAt: refreshedAt,
	}, nil
}

// MarkFailed marks an existing snapshot stale while preserving its last valid
// data, version, successful refresh time, and successful refresh reason.
func (store *StateStore) markFailed(scope RefreshScope, refreshErr error) SnapshotMeta {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry := store.entries[scope]
	entry.meta.Scope = scope
	entry.meta.Stale = true
	entry.meta.LastRefreshErr = refreshErr
	store.entries[scope] = entry
	return entry.meta
}

// Read returns a copy of one valid snapshot.
func (store *StateStore) Read(scope RefreshScope) (StoredSnapshot, error) {
	store.mu.RLock()
	entry, ok := store.entries[scope]
	store.mu.RUnlock()
	if !ok || !entry.valid {
		return StoredSnapshot{}, snapshotUnavailable(scope)
	}
	return StoredSnapshot{
		Data: append(json.RawMessage(nil), entry.data...),
		Meta: entry.meta,
	}, nil
}

// Meta returns freshness metadata for a valid snapshot.
func (store *StateStore) Meta(scope RefreshScope) (SnapshotMeta, error) {
	store.mu.RLock()
	entry, ok := store.entries[scope]
	store.mu.RUnlock()
	if !ok || !entry.valid {
		return SnapshotMeta{}, snapshotUnavailable(scope)
	}
	return entry.meta, nil
}

// DecodeSnapshot decodes a copied stored projection into its domain DTO.
func DecodeSnapshot[T any](snapshot StoredSnapshot) (Snapshot[T], error) {
	var value T
	if err := json.Unmarshal(snapshot.Data, &value); err != nil {
		return Snapshot[T]{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return Snapshot[T]{Data: value, Meta: snapshot.Meta}, nil
}

func snapshotUnavailable(scope RefreshScope) error {
	return &AppError{
		Code:      ErrorSnapshotUnavailable,
		Operation: "read snapshot",
		Resource:  scope.Resource,
		ID:        scope.ID,
	}
}

func validateScope(scope RefreshScope) error {
	if scope.Resource == "" || scope.View == "" {
		return &AppError{
			Code:      ErrorInvalidInput,
			Operation: "validate refresh scope",
			Resource:  scope.Resource,
			ID:        scope.ID,
		}
	}
	return nil
}
