// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNDJSONWriter_AppendsOnePerLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "minimax.ndjson")
	w, err := OpenNDJSONWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	now := time.Date(2026, 5, 7, 10, 30, 0, 0, time.UTC)
	events := []Event{
		{EventAt: now, RecordedAt: now, Alias: "minimax-api-1", Kind: EventSuccess},
		{EventAt: now.Add(time.Second), RecordedAt: now.Add(time.Second), Alias: "minimax-api-1", Kind: EventRateLimited, Detail: "429"},
		{EventAt: now.Add(2 * time.Second), RecordedAt: now.Add(2 * time.Second), Alias: "minimax-api-2", Kind: EventRotated},
	}
	for _, e := range events {
		if err := w.Write(e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open back: %v", err)
	}
	defer func() { _ = f.Close() }()

	var got []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("decode line %q: %v", sc.Text(), err)
		}
		got = append(got, e)
	}
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d", len(got), len(events))
	}
	for i, e := range events {
		if got[i].Alias != e.Alias || got[i].Kind != e.Kind || got[i].Detail != e.Detail {
			t.Errorf("event[%d] = %+v, want alias %q kind %q detail %q",
				i, got[i], e.Alias, e.Kind, e.Detail)
		}
	}
}

func TestNDJSONWriter_FillsRecordedAtWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w, err := OpenNDJSONWriter(filepath.Join(dir, "log.ndjson"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	before := time.Now().UTC()
	if err := w.Write(Event{Alias: "minimax-api-1", Kind: EventSuccess}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	after := time.Now().UTC()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "log.ndjson"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var e Event
	if err := json.Unmarshal(data[:len(data)-1], &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.RecordedAt.Before(before) || e.RecordedAt.After(after) {
		t.Errorf("RecordedAt %v not in [%v, %v]", e.RecordedAt, before, after)
	}
}

func TestNDJSONWriter_WritesAreSerialised(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w, err := OpenNDJSONWriter(filepath.Join(dir, "log.ndjson"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	const writers = 8
	const perWriter = 50
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_ = w.Write(Event{Alias: KeyAlias("minimax-api-1"), Kind: EventSuccess, Detail: "x"})
			}
		}(i)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(filepath.Join(dir, "log.ndjson"))
	if err != nil {
		t.Fatalf("open back: %v", err)
	}
	defer func() { _ = f.Close() }()
	count := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("decode line %q: %v", sc.Text(), err)
		}
		count++
	}
	if count != writers*perWriter {
		t.Errorf("got %d events, want %d (concurrent writes lost or interleaved)",
			count, writers*perWriter)
	}
}

func TestNDJSONWriter_ClosedWriterReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w, _ := OpenNDJSONWriter(filepath.Join(dir, "log.ndjson"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Write(Event{Alias: "minimax-api-1", Kind: EventSuccess}); err == nil {
		t.Error("Write after Close should error")
	}
}

func TestNoopWriter_DropsEvents(t *testing.T) {
	t.Parallel()
	var w NoopWriter
	if err := w.Write(Event{Alias: "minimax-api-1", Kind: EventSuccess}); err != nil {
		t.Errorf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
