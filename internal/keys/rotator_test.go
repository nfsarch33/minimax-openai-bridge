package keys

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRotator_50CallSmokeAcrossKeys(t *testing.T) {
	t.Parallel()

	var serverCalls atomic.Int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(serverCalls.Add(1))
		if n == 25 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	var evidence bytes.Buffer
	rot := NewRotator(RotatorConfig{
		Keys:        []string{"key-a-test", "key-b-test"},
		BaseURL:     upstream.URL,
		Client:      upstream.Client(),
		EvidenceOut: &evidence,
		Clock:       func() time.Time { return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC) },
	})

	var failures int
	for i := 0; i < 50; i++ {
		status, err := rot.Call(context.Background(), "/v1/chat/completions", []byte(`{"model":"test"}`))
		if err != nil || status != http.StatusOK {
			failures++
		}
	}

	if failures != 0 {
		t.Fatalf("got %d hard failures across 50 calls, want 0", failures)
	}

	raw := bytes.TrimSpace(evidence.Bytes())
	if len(raw) == 0 {
		t.Fatal("no NDJSON evidence lines emitted")
	}
	lines := bytes.Split(raw, []byte("\n"))

	keyIndicesSeen := make(map[int]bool)
	rotationObserved := false
	prevKeyIndex := -1
	var sawRateLimited bool

	for i, line := range lines {
		var entry EvidenceEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("line %d: invalid NDJSON: %v", i, err)
		}
		if entry.Timestamp == "" {
			t.Fatalf("line %d: missing timestamp", i)
		}
		keyIndicesSeen[entry.KeyIndex] = true
		if prevKeyIndex >= 0 && entry.KeyIndex != prevKeyIndex {
			rotationObserved = true
		}
		prevKeyIndex = entry.KeyIndex
		if entry.Status == http.StatusTooManyRequests {
			sawRateLimited = true
		}
	}

	if !keyIndicesSeen[0] || !keyIndicesSeen[1] {
		t.Fatalf("expected both key indices [0,1] seen, got %v", keyIndicesSeen)
	}
	if !rotationObserved {
		t.Fatal("key rotation not observed in NDJSON evidence")
	}
	if !sawRateLimited {
		t.Fatal("expected at least one 429 evidence entry")
	}
}
