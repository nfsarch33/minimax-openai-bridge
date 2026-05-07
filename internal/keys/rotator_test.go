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

	upstream := newRateLimitOnceServer(25)
	defer upstream.Close()

	var evidence bytes.Buffer
	rot := NewRotator(RotatorConfig{
		Keys:        []string{"key-a-test", "key-b-test"},
		BaseURL:     upstream.URL,
		Client:      upstream.Client(),
		EvidenceOut: &evidence,
		Clock:       func() time.Time { return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC) },
	})

	if failures := callRotator(t, rot, 50); failures != 0 {
		t.Fatalf("got %d hard failures across 50 calls, want 0", failures)
	}

	assertRotationEvidence(t, evidence.Bytes())
}

func newRateLimitOnceServer(rateLimitCall int) *httptest.Server {
	var serverCalls atomic.Int32

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(serverCalls.Add(1))
		if n == rateLimitCall {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
}

func callRotator(t *testing.T, rot *Rotator, calls int) int {
	t.Helper()

	failures := 0
	for i := 0; i < calls; i++ {
		status, err := rot.Call(context.Background(), "/v1/chat/completions", []byte(`{"model":"test"}`))
		if err != nil || status != http.StatusOK {
			failures++
		}
	}
	return failures
}

func assertRotationEvidence(t *testing.T, evidence []byte) {
	t.Helper()

	raw := bytes.TrimSpace(evidence)
	if len(raw) == 0 {
		t.Fatal("no NDJSON evidence lines emitted")
	}

	keyIndicesSeen := make(map[int]bool)
	rotationObserved := false
	prevKeyIndex := -1
	var sawRateLimited bool

	for i, line := range bytes.Split(raw, []byte("\n")) {
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
