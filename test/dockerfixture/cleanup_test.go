package dockerfixture

import (
	"context"
	"reflect"
	"testing"
)

func TestCleanupRunsInDependencyOrderAndOnlyOnce(t *testing.T) {
	var calls []string
	stack := cleanupStack{}
	add := func(kind resourceKind, id string) {
		stack.add(kind, id, func(context.Context) error {
			calls = append(calls, id)
			return nil
		})
	}

	add(volumeResource, "volume-one")
	add(containerResource, "container-one")
	add(imageResource, "image-one")
	add(networkResource, "network-one")
	add(containerResource, "container-two")

	if err := stack.run(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if err := stack.run(context.Background()); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}

	want := []string{"container-two", "container-one", "network-one", "volume-one", "image-one"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("cleanup order = %v, want %v", calls, want)
	}
}

func TestLabelsCannotOverrideFixtureIdentity(t *testing.T) {
	fixture := &Fixture{config: Config{ResourceLabels: map[string]string{testLabel: "true"}}}
	labels := fixture.Labels(map[string]string{testLabel: "false", "purpose": "test"})

	if labels[testLabel] != "true" {
		t.Fatalf("fixture identity label was overridden: %v", labels)
	}
	if labels["purpose"] != "test" {
		t.Fatalf("resource-specific label missing: %v", labels)
	}
}
