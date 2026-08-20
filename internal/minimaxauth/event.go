// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventKind identifies the observation that produced an Event. The set
// is closed: consumers MUST NOT introduce free-form strings.
type EventKind string

const (
	EventSuccess        EventKind = "success"
	EventRateLimited    EventKind = "rate_limited"
	EventQuotaExhausted EventKind = "quota_exhausted"
	EventRotated        EventKind = "rotated"
	EventReset          EventKind = "reset"
)

// Event is a single observation persisted to the NDJSON audit log.
// Fields follow the timestamp-discipline rule: both event_at (when the
// observation actually happened) and recorded_at (when it was written
// to the log) are populated.
type Event struct {
	EventAt    time.Time `json:"event_at"`
	RecordedAt time.Time `json:"recorded_at"`
	Alias      KeyAlias  `json:"alias"`
	Kind       EventKind `json:"kind"`
	Detail     string    `json:"detail,omitempty"`
}

// EventWriter persists events to durable storage. Implementations MUST
// be safe for concurrent use.
type EventWriter interface {
	Write(e Event) error
	Close() error
}

// NDJSONWriter appends JSON-per-line events to a file. It is the default
// writer used by `runx minimax`. Logs land in ~/logs/runx/minimax.ndjson
// per the no-shell-leak rule.
type NDJSONWriter struct {
	mu sync.Mutex
	f  *os.File
}

// OpenNDJSONWriter opens or creates the NDJSON log at path with mode 0600.
// The parent directory is created with mode 0700 if missing.
func OpenNDJSONWriter(path string) (*NDJSONWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("minimaxauth: mkdir log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("minimaxauth: open ndjson: %w", err)
	}
	return &NDJSONWriter{f: f}, nil
}

// Write encodes the event as a single JSON line. Concurrent writers are
// serialised so partial lines never interleave.
func (w *NDJSONWriter) Write(e Event) error {
	if w == nil || w.f == nil {
		return io.ErrClosedPipe
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("minimaxauth: marshal event: %w", err)
	}
	line = append(line, '\n')
	if _, err := w.f.Write(line); err != nil {
		return fmt.Errorf("minimaxauth: write event: %w", err)
	}
	return nil
}

// Close flushes and closes the underlying file. Safe to call multiple
// times.
func (w *NDJSONWriter) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.f.Close()
	w.f = nil
	return err
}

// NoopWriter is an EventWriter that discards events. Useful for tests
// and for read-only commands that should not produce audit log entries.
type NoopWriter struct{}

func (NoopWriter) Write(Event) error { return nil }
func (NoopWriter) Close() error      { return nil }
