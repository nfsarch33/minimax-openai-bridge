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

func TestNormalizeInputRejectsInvalidShape(t *testing.T) {
	t.Parallel()
	if _, err := normalizeInput(float64(1)); err == nil {
		t.Fatal("expected error for non-string input")
	}
	if _, err := normalizeInput([]any{""}); err == nil {
		t.Fatal("expected error for empty string")
	}
}
