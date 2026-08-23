package testkit

import "sync"

// RecordingCloser records ownership and returns a configurable error.
type RecordingCloser struct {
	mu    sync.Mutex
	count int
	err   error
}

func NewRecordingCloser(err error) *RecordingCloser {
	return &RecordingCloser{err: err}
}

func (closer *RecordingCloser) Close() error {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	closer.count++
	return closer.err
}

func (closer *RecordingCloser) Count() int {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	return closer.count
}
