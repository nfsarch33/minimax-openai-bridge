package bridge

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nfsarch33/runx/pkg/minimaxauth"
)

func TestEmbeddingsTranslateOpenAIRequestToMiniMax(t *testing.T) {
	t.Parallel()
	var got minimaxEmbeddingRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("GroupId") != "group-1" {
			t.Fatalf("GroupId = %q, want group-1", r.URL.Query().Get("GroupId"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Authorization header not translated")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(minimaxEmbeddingResponse{
			Vectors: [][]float64{{0.1, 0.2}, {0.3, 0.4}},
			BaseResp: minimaxBaseResp{
				StatusCode: 0,
			},
		})
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKey:         "test-key",
		GroupID:        "group-1",
		Model:          "embo-01",
		DefaultType:    "db",
		Timeout:        time.Second,
	}, upstream.Client(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"embo-01","input":["alpha","beta"],"user":"query"}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got.Type != "query" {
		t.Fatalf("MiniMax type = %q, want query", got.Type)
	}
	if len(got.Texts) != 2 || got.Texts[0] != "alpha" || got.Texts[1] != "beta" {
		t.Fatalf("texts = %#v", got.Texts)
	}
	var out openAIEmbeddingResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Data) != 2 || out.Data[1].Embedding[0] != 0.3 {
		t.Fatalf("unexpected OpenAI response: %#v", out.Data)
	}
}

func TestEmbeddingsFallbackToSecondKeyOnRateLimit(t *testing.T) {
	t.Parallel()
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if len(auths) == 1 {
			http.Error(w, "rate limit", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(minimaxEmbeddingResponse{
			Vectors:  [][]float64{{0.7, 0.8}},
			BaseResp: minimaxBaseResp{StatusCode: 0},
		})
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKeys:        []string{"key-1", "key-2"},
		Model:          "embo-01",
		DefaultType:    "db",
		Timeout:        time.Second,
	}, upstream.Client(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"alpha"}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(auths) != 2 {
		t.Fatalf("auth calls = %d, want 2", len(auths))
	}
	if auths[0] != "Bearer key-1" || auths[1] != "Bearer key-2" {
		t.Fatalf("auths = %#v", auths)
	}
}

func TestChatCompletionsFallbackToSecondKeyOnQuota(t *testing.T) {
	t.Parallel()
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if len(auths) == 1 {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":"quota exceeded"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ok","choices":[]}`))
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKeys:        []string{"key-1", "key-2"},
		Model:          "MiniMax-M2.1",
		DefaultType:    "db",
		Timeout:        time.Second,
	}, upstream.Client(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"MiniMax-M2.1","messages":[]}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(auths) != 2 || auths[1] != "Bearer key-2" {
		t.Fatalf("auths = %#v", auths)
	}
}

func TestEmbeddingsSharedSelectorRecordsRateLimitAndSuccess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC)
	events := &captureEventWriter{}
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if len(auths) == 1 {
			http.Error(w, "rate limit", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(minimaxEmbeddingResponse{
			Vectors:  [][]float64{{0.7, 0.8}},
			BaseResp: minimaxBaseResp{StatusCode: 0},
		})
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKeys:        []string{"key-1", "key-2"},
		Model:          "embo-01",
		DefaultType:    "db",
		Timeout:        time.Second,
		EventWriter:    events,
		Clock:          func() time.Time { return now },
	}, upstream.Client(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"alpha"}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(auths) != 2 || auths[0] != "Bearer key-1" || auths[1] != "Bearer key-2" {
		t.Fatalf("auths = %#v", auths)
	}
	gotEvents := events.snapshot()
	if len(gotEvents) != 2 {
		t.Fatalf("events = %#v, want rate-limit + success", gotEvents)
	}
	if gotEvents[0].Alias != "minimax-api-1" || gotEvents[0].Kind != minimaxauth.EventRateLimited {
		t.Fatalf("first event = %#v, want minimax-api-1 rate_limited", gotEvents[0])
	}
	if gotEvents[1].Alias != "minimax-api-2" || gotEvents[1].Kind != minimaxauth.EventSuccess {
		t.Fatalf("second event = %#v, want minimax-api-2 success", gotEvents[1])
	}
}

func TestEmbeddingsQuotaExhaustionSkipsAliasOnLaterRequest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC)
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		auths = append(auths, auth)
		if auth == "Bearer key-1" {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":"quota exhausted"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(minimaxEmbeddingResponse{
			Vectors:  [][]float64{{0.7, 0.8}},
			BaseResp: minimaxBaseResp{StatusCode: 0},
		})
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKeys:        []string{"key-1", "key-2"},
		Model:          "embo-01",
		DefaultType:    "db",
		Timeout:        time.Second,
		Clock:          func() time.Time { return now },
	}, upstream.Client(), nil)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"alpha"}`))
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	want := []string{"Bearer key-1", "Bearer key-2", "Bearer key-2"}
	if len(auths) != len(want) {
		t.Fatalf("auths = %#v, want %#v", auths, want)
	}
	for i := range want {
		if auths[i] != want[i] {
			t.Fatalf("auths = %#v, want %#v", auths, want)
		}
	}
}

func TestEmbeddingsRateLimitedAliasRecoversAfterBackoff(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC)
	now := start
	key1Failures := 0
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		auths = append(auths, auth)
		if auth == "Bearer key-1" && key1Failures == 0 {
			key1Failures++
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(minimaxEmbeddingResponse{
			Vectors:  [][]float64{{0.7, 0.8}},
			BaseResp: minimaxBaseResp{StatusCode: 0},
		})
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL:  upstream.URL + "/v1",
		APIKeys:         []string{"key-1", "key-2"},
		Model:           "embo-01",
		DefaultType:     "db",
		Timeout:         time.Second,
		SelectorBackoff: time.Minute,
		Clock:           func() time.Time { return now },
	}, upstream.Client(), nil)

	for _, advance := range []time.Duration{0, 30 * time.Second, 90 * time.Second} {
		now = start.Add(advance)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"alpha"}`))
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("advance %s status = %d, body=%s", advance, rec.Code, rec.Body.String())
		}
	}

	want := []string{"Bearer key-1", "Bearer key-2", "Bearer key-2", "Bearer key-1"}
	if len(auths) != len(want) {
		t.Fatalf("auths = %#v, want %#v", auths, want)
	}
	for i := range want {
		if auths[i] != want[i] {
			t.Fatalf("auths = %#v, want %#v", auths, want)
		}
	}
}

func TestEmbeddingsRateLimitedAliasHonorsHTTPDateRetryAfter(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC)
	now := start
	key1Failures := 0
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		auths = append(auths, auth)
		if auth == "Bearer key-1" && key1Failures == 0 {
			key1Failures++
			w.Header().Set("Retry-After", start.Add(2*time.Minute).Format(http.TimeFormat))
			http.Error(w, "rate limit", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(minimaxEmbeddingResponse{
			Vectors:  [][]float64{{0.7, 0.8}},
			BaseResp: minimaxBaseResp{StatusCode: 0},
		})
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL:  upstream.URL + "/v1",
		APIKeys:         []string{"key-1", "key-2"},
		Model:           "embo-01",
		DefaultType:     "db",
		Timeout:         time.Second,
		SelectorBackoff: time.Second,
		Clock:           func() time.Time { return now },
	}, upstream.Client(), nil)

	for _, advance := range []time.Duration{0, time.Minute, 3 * time.Minute} {
		now = start.Add(advance)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"alpha"}`))
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("advance %s status = %d, body=%s", advance, rec.Code, rec.Body.String())
		}
	}

	want := []string{"Bearer key-1", "Bearer key-2", "Bearer key-2", "Bearer key-1"}
	if len(auths) != len(want) {
		t.Fatalf("auths = %#v, want %#v", auths, want)
	}
	for i := range want {
		if auths[i] != want[i] {
			t.Fatalf("auths = %#v, want %#v", auths, want)
		}
	}
}

func TestChatCompletionsRecordsQuotaEventsWhenAllAliasesExhausted(t *testing.T) {
	t.Parallel()

	events := &captureEventWriter{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"quota exhausted"}`))
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKeys:        []string{"key-1", "key-2"},
		Model:          "MiniMax-M2.1",
		DefaultType:    "db",
		Timeout:        time.Second,
		EventWriter:    events,
	}, upstream.Client(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"MiniMax-M2.1","messages":[]}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	gotEvents := events.snapshot()
	if len(gotEvents) != 2 {
		t.Fatalf("events = %#v, want one quota event per alias", gotEvents)
	}
	for i, event := range gotEvents {
		if event.Kind != minimaxauth.EventQuotaExhausted {
			t.Fatalf("event %d = %#v, want quota_exhausted", i, event)
		}
	}
}

func TestNormalizeInputRejectsInvalidShape(t *testing.T) {
	t.Parallel()
	if _, err := normalizeInput(float64(1)); err == nil {
		t.Fatal("expected error for non-string input")
	}
	if _, err := normalizeInput([]any{""}); err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestLoadAPIKeysFiltersPlaceholdersAndDuplicates(t *testing.T) {
	t.Setenv("MINIMAX_API_KEYS", "key-1, TODO_PLACEHOLDER, key-2")
	t.Setenv("MINIMAX_API_KEY_1", "key-1")
	t.Setenv("MINIMAX_API_KEY_2", "key-3")
	t.Setenv("MINIMAX_API_KEY", "key-2")

	got := loadAPIKeys()
	want := []string{"key-1", "key-2", "key-3"}
	if len(got) != len(want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys = %#v, want %#v", got, want)
		}
	}
}

func TestLoadAPIKeyBindingsPreservesEnvKeySupport(t *testing.T) {
	t.Setenv("MINIMAX_API_KEYS", "key-1, TODO_PLACEHOLDER, key-2")
	t.Setenv("MINIMAX_API_KEY_1", "key-1")
	t.Setenv("MINIMAX_API_KEY_2", "key-3")
	t.Setenv("MINIMAX_API_KEY", "key-2")

	got := loadAPIKeyBindings()
	want := []apiKeyBinding{
		{alias: "minimax-api-1", key: "key-1"},
		{alias: "minimax-api-2", key: "key-2"},
		{alias: "minimax-api-3", key: "key-3"},
	}
	if len(got) != len(want) {
		t.Fatalf("bindings = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bindings = %#v, want %#v", got, want)
		}
	}
}

type captureEventWriter struct {
	mu     sync.Mutex
	events []minimaxauth.Event
}

func (w *captureEventWriter) Write(e minimaxauth.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.Alias == "" || e.Kind == "" {
		return errors.New("event missing alias or kind")
	}
	w.events = append(w.events, e)
	return nil
}

func (w *captureEventWriter) Close() error { return nil }

func (w *captureEventWriter) snapshot() []minimaxauth.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]minimaxauth.Event(nil), w.events...)
}
