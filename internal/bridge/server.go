package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

const embeddingPath = "/v1/embeddings"

type Server struct {
	cfg    Config
	client *http.Client
	log    *slog.Logger
}

func NewServer(cfg Config, client *http.Client, log *slog.Logger) *Server {
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, client: client, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc(embeddingPath, s.handleEmbeddings)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req openAIEmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	inputs, err := normalizeInput(req.Input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	embedType := req.User
	if embedType == "" {
		embedType = s.cfg.DefaultType
	}
	if embedType != "db" && embedType != "query" {
		http.Error(w, "user field must be db or query when used as MiniMax embedding type", http.StatusBadRequest)
		return
	}
	model := req.Model
	if model == "" {
		model = s.cfg.Model
	}
	vectors, err := s.embed(r.Context(), model, embedType, inputs)
	if err != nil {
		s.log.Error("minimax embedding failed", "err", err)
		http.Error(w, "embedding provider failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, toOpenAIResponse(model, vectors))
}

func (s *Server) embed(ctx context.Context, model, embedType string, texts []string) ([][]float64, error) {
	if s.cfg.APIKey == "" {
		return nil, errors.New("missing MINIMAX_API_KEY")
	}
	endpoint, err := url.JoinPath(strings.TrimRight(s.cfg.MiniMaxBaseURL, "/"), "embeddings")
	if err != nil {
		return nil, fmt.Errorf("endpoint: %w", err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	if s.cfg.GroupID != "" {
		q := u.Query()
		q.Set("GroupId", s.cfg.GroupID)
		u.RawQuery = q.Encode()
	}
	payload := minimaxEmbeddingRequest{
		Model: model,
		Type:  embedType,
		Texts: texts,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("minimax status %d", resp.StatusCode)
	}
	var parsed minimaxEmbeddingResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if parsed.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("minimax base_resp %d: %s", parsed.BaseResp.StatusCode, parsed.BaseResp.StatusMsg)
	}
	if len(parsed.Vectors) != len(texts) {
		return nil, fmt.Errorf("vector count %d != input count %d", len(parsed.Vectors), len(texts))
	}
	return parsed.Vectors, nil
}

func normalizeInput(input any) ([]string, error) {
	switch value := input.(type) {
	case string:
		if value == "" {
			return nil, errors.New("input must not be empty")
		}
		return []string{value}, nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok || text == "" {
				return nil, errors.New("input array must contain non-empty strings")
			}
			out = append(out, text)
		}
		if len(out) == 0 {
			return nil, errors.New("input must not be empty")
		}
		return out, nil
	default:
		return nil, errors.New("input must be a string or array of strings")
	}
}

func toOpenAIResponse(model string, vectors [][]float64) openAIEmbeddingResponse {
	data := make([]openAIEmbedding, 0, len(vectors))
	totalTokens := 0
	for i, vector := range vectors {
		data = append(data, openAIEmbedding{
			Object:    "embedding",
			Index:     i,
			Embedding: vector,
		})
		totalTokens += len(vector)
	}
	return openAIEmbeddingResponse{
		Object: "list",
		Model:  model,
		Data:   data,
		Usage: openAIUsage{
			PromptTokens: totalTokens,
			TotalTokens:  totalTokens,
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
