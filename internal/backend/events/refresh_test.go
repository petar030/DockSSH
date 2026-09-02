package events

import (
	"context"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestRefreshHandlerReturnsBoundedNewestFirstCopiedHistory(t *testing.T) {
	attributes := map[string]string{"name": "original"}
	history := &fakeHistory{values: []backend.EventEnvelope{
		{Sequence: 3, Time: time.Unix(3, 0), Payload: backend.DockerEventObserved{OccurredAt: time.Unix(2, 0), Resource: "container", ResourceID: "three", Action: "start", Attributes: attributes}},
		{Sequence: 2, Time: time.Unix(2, 0), Payload: backend.DockerEventObserved{Resource: "image", ResourceID: "two", Action: "tag"}},
	}}
	handler, err := NewRefreshHandler(history, 2)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	payload, err := handler.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindRecent})
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}
	update := payload.(RecentUpdated)
	if history.limit != 2 || len(update.Events) != 2 || update.Events[0].Sequence != 3 || update.Events[0].ResourceID != "three" ||
		update.Events[0].Attributes["name"] != "original" {
		t.Fatalf("recent update = %#v", update)
	}
	attributes["name"] = "changed"
	if update.Events[0].Attributes["name"] != "original" {
		t.Fatal("recent update aliases history payload")
	}
	clone := update.CloneEventPayload().(RecentUpdated)
	update.Events[0].Attributes["name"] = "again"
	if clone.Events[0].Attributes["name"] != "original" {
		t.Fatal("recent update clone aliases source")
	}
}

func TestRefreshHandlerValidation(t *testing.T) {
	if _, err := NewRefreshHandler(nil, 1); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
		t.Fatalf("nil history error = %v", err)
	}
	if _, err := NewRefreshHandler(&fakeHistory{}, -1); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
		t.Fatalf("negative limit error = %v", err)
	}
	handler, err := NewRefreshHandler(&fakeHistory{}, 0)
	if err != nil {
		t.Fatalf("default handler: %v", err)
	}
	for _, key := range []backend.RefreshKey{{Kind: "unknown"}, {Kind: RefreshKindRecent, ID: "bad"}} {
		if _, err := handler.ReadRefresh(context.Background(), key); !backend.HasErrorCode(err, backend.ErrorUnsupported) {
			t.Fatalf("ReadRefresh(%#v) error = %v", key, err)
		}
	}
}

type fakeHistory struct {
	values []backend.EventEnvelope
	filter backend.EventFilter
	limit  int
}

func (history *fakeHistory) Recent(filter backend.EventFilter, limit int) []backend.EventEnvelope {
	history.filter = filter
	history.limit = limit
	return append([]backend.EventEnvelope(nil), history.values...)
}
