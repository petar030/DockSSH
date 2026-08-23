package dockerfixture

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type resourceKind uint8

const (
	containerResource resourceKind = iota
	networkResource
	volumeResource
	imageResource
)

type cleanupEntry struct {
	kind resourceKind
	id   string
	fn   func(context.Context) error
}

type cleanupStack struct {
	mu      sync.Mutex
	entries []cleanupEntry
	done    bool
}

func (stack *cleanupStack) add(kind resourceKind, id string, fn func(context.Context) error) {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.done {
		panic("dockerfixture: resource tracked after cleanup")
	}
	stack.entries = append(stack.entries, cleanupEntry{kind: kind, id: id, fn: fn})
}

func (stack *cleanupStack) run(ctx context.Context) error {
	stack.mu.Lock()
	if stack.done {
		stack.mu.Unlock()
		return nil
	}
	stack.done = true
	entries := append([]cleanupEntry(nil), stack.entries...)
	stack.mu.Unlock()

	var cleanupErrors []error
	// Containers must be removed before their networks and volumes. Images are
	// last. Within a kind, clean up in reverse registration order.
	for kind := containerResource; kind <= imageResource; kind++ {
		for index := len(entries) - 1; index >= 0; index-- {
			entry := entries[index]
			if entry.kind != kind {
				continue
			}
			if err := entry.fn(ctx); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup %s %q: %w", entry.kind, entry.id, err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func (kind resourceKind) String() string {
	switch kind {
	case containerResource:
		return "container"
	case networkResource:
		return "network"
	case volumeResource:
		return "volume"
	case imageResource:
		return "image"
	default:
		return "unknown resource"
	}
}
