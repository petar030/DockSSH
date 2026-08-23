package backend

import (
	"sync"
	"testing"
	"time"
)

type testProjection struct {
	Names []string `json:"names"`
}

func TestStateStoreVersionsOnlyChangedData(t *testing.T) {
	store := NewStateStore()
	scope := RefreshScope{Resource: ResourceContainer, View: ViewSummary}
	firstTime := time.Unix(100, 0)

	first, err := store.store(scope, testProjection{Names: []string{"one"}}, RefreshStartup, firstTime)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	if first.Version != 1 || !first.Changed {
		t.Fatalf("first result = %#v", first)
	}

	secondTime := firstTime.Add(time.Second)
	second, err := store.store(scope, testProjection{Names: []string{"one"}}, RefreshManual, secondTime)
	if err != nil {
		t.Fatalf("unchanged store: %v", err)
	}
	if second.Version != 1 || second.Changed {
		t.Fatalf("unchanged result = %#v", second)
	}
	meta, err := store.Meta(scope)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.RefreshedAt != secondTime || meta.Reason != RefreshManual {
		t.Fatalf("successful freshness was not updated: %#v", meta)
	}

	third, err := store.store(scope, testProjection{Names: []string{"two"}}, RefreshManual, secondTime)
	if err != nil {
		t.Fatalf("changed store: %v", err)
	}
	if third.Version != 2 || !third.Changed {
		t.Fatalf("changed result = %#v", third)
	}
}

func TestStateStorePreservesAndCopiesLastValidSnapshotOnFailure(t *testing.T) {
	store := NewStateStore()
	scope := RefreshScope{Resource: ResourceImage, View: ViewSummary}
	refreshedAt := time.Unix(200, 0)
	if _, err := store.store(scope, testProjection{Names: []string{"original"}}, RefreshStartup, refreshedAt); err != nil {
		t.Fatalf("store: %v", err)
	}

	failure := &AppError{Code: ErrorDaemonUnavailable}
	failedMeta := store.markFailed(scope, failure)
	if !failedMeta.Stale || failedMeta.LastRefreshErr != failure {
		t.Fatalf("failure metadata = %#v", failedMeta)
	}
	if failedMeta.RefreshedAt != refreshedAt || failedMeta.Reason != RefreshStartup {
		t.Fatalf("failure overwrote last successful freshness: %#v", failedMeta)
	}

	raw, err := store.Read(scope)
	if err != nil {
		t.Fatalf("read after failure: %v", err)
	}
	raw.Data[0] = 'x'
	again, err := store.Read(scope)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	decoded, err := DecodeSnapshot[testProjection](again)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := decoded.Data.Names[0]; got != "original" {
		t.Fatalf("stored data was mutated through read: %q", got)
	}
}

func TestStateStoreConcurrentReadersNeverSeePartialData(t *testing.T) {
	store := NewStateStore()
	scope := RefreshScope{Resource: ResourceVolume, View: ViewSummary}
	if _, err := store.store(scope, testProjection{Names: []string{"0", "0"}}, RefreshStartup, time.Unix(1, 0)); err != nil {
		t.Fatalf("initial store: %v", err)
	}

	const iterations = 200
	var wait sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range iterations {
				raw, err := store.Read(scope)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				value, err := DecodeSnapshot[testProjection](raw)
				if err != nil {
					t.Errorf("decode: %v", err)
					return
				}
				if len(value.Data.Names) != 2 || value.Data.Names[0] != value.Data.Names[1] {
					t.Errorf("partial snapshot: %v", value.Data.Names)
					return
				}
			}
		}()
	}
	for index := 1; index <= iterations; index++ {
		name := string(rune(index))
		if _, err := store.store(scope, testProjection{Names: []string{name, name}}, RefreshManual, time.Unix(int64(index+1), 0)); err != nil {
			t.Fatalf("store %d: %v", index, err)
		}
	}
	wait.Wait()
}

func TestStateStoreRejectsUnavailableAndUnserializableSnapshots(t *testing.T) {
	store := NewStateStore()
	scope := RefreshScope{Resource: ResourceSystem, View: ViewEngine}
	if _, err := store.Read(scope); !HasErrorCode(err, ErrorSnapshotUnavailable) {
		t.Fatalf("missing snapshot error = %v", err)
	}
	if _, err := store.store(scope, make(chan int), RefreshManual, time.Now()); !HasErrorCode(err, ErrorInvalidInput) {
		t.Fatalf("unserializable snapshot error = %v", err)
	}
}
