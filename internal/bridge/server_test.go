package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
