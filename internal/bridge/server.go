package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nfsarch33/minimax-openai-bridge/internal/minimaxauth"
)

const embeddingPath = "/v1/embeddings"

type Server struct {
	cfg     Config
	client  *http.Client
	log     *slog.Logger
	auth    *minimaxauth.Service
	secrets minimaxauth.SecretLoader
	aliases []minimaxauth.KeyAlias
	clock   func() time.Time
}

func NewServer(cfg Config, client *http.Client, log *slog.Logger) *Server {
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	if log == nil {
		log = slog.Default()
	}
	auth := newAuthRuntime(cfg, log)
	return &Server{
		cfg:     cfg,
		client:  client,
		log:     log,
		auth:    auth.service,
		secrets: auth.secrets,
		aliases: auth.aliases,
		clock:   auth.clock,
	}
}

type authRuntime struct {
	service *minimaxauth.Service
	secrets minimaxauth.SecretLoader
	aliases []minimaxauth.KeyAlias
	clock   func() time.Time
}

func newAuthRuntime(cfg Config, log *slog.Logger) authRuntime {
	clock := configClock(cfg)
	bindings := configKeyBindings(cfg)
	aliases := bindingAliases(bindings)
	auth := newAuthService(cfg, aliases, clock, configEventWriter(cfg, log), log)
	secrets := cfg.SecretLoader
	if secrets == nil {
		secrets = newEnvSecretLoader(bindings)
	}
	return authRuntime{service: auth, secrets: secrets, aliases: aliases, clock: clock}
}

func configClock(cfg Config) func() time.Time {
	if cfg.Clock != nil {
		return cfg.Clock
	}
	return func() time.Time { return time.Now().UTC() }
}

func configKeyBindings(cfg Config) []apiKeyBinding {
	if len(cfg.KeyBindings) > 0 {
		return cfg.KeyBindings
	}
	return bindingsFromKeys(apiKeysFromConfig(cfg))
}

func bindingAliases(bindings []apiKeyBinding) []minimaxauth.KeyAlias {
	aliases := make([]minimaxauth.KeyAlias, 0, len(bindings))
	for _, binding := range bindings {
		aliases = append(aliases, binding.alias)
	}
	return aliases
}

func configEventWriter(cfg Config, log *slog.Logger) minimaxauth.EventWriter {
	if cfg.EventWriter != nil {
		return cfg.EventWriter
	}
	if cfg.EventLogPath == "" {
		return minimaxauth.NoopWriter{}
	}
	opened, err := minimaxauth.OpenNDJSONWriter(cfg.EventLogPath)
	if err != nil {
		log.Warn("minimax event log disabled", "err", err)
		return minimaxauth.NoopWriter{}
	}
	return opened
}

func newAuthService(cfg Config, aliases []minimaxauth.KeyAlias, clock func() time.Time, writer minimaxauth.EventWriter, log *slog.Logger) *minimaxauth.Service {
	auth, err := minimaxauth.NewService(
		minimaxauth.StaticSource(aliases),
		&minimaxauth.StickyWithFailoverSelector{Backoff: cfg.SelectorBackoff},
		minimaxauth.WithClock(clock),
		minimaxauth.WithEventWriter(writer),
	)
	if err != nil {
		log.Warn("minimax auth service disabled", "err", err)
		return nil
	}
	if err := auth.Refresh(context.Background()); err != nil {
		log.Warn("minimax auth refresh failed", "err", err)
	}
	return auth
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc(embeddingPath, s.handleEmbeddings)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
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

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if isStreamingRequest(body) {
		s.handleStreamingChat(w, r, body)
		return
	}
	raw, status, err := s.postMiniMax(r.Context(), "chat/completions", body)
	if err != nil {
		s.log.Error("minimax chat completion failed", "err", err)
		http.Error(w, "chat provider failed", http.StatusBadGateway)
		return
	}
	cleaned := StripThinkTagsFromChatResponse(raw)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(cleaned)
}

func (s *Server) handleStreamingChat(w http.ResponseWriter, r *http.Request, body []byte) {
	if s.maxAttempts() == 0 {
		http.Error(w, "missing MiniMax API keys", http.StatusBadGateway)
		return
	}
	alias, err := s.pickAlias()
	if err != nil {
		http.Error(w, "no available API key", http.StatusBadGateway)
		return
	}
	apiKey, err := s.secrets.Load(r.Context(), alias)
	if err != nil {
		http.Error(w, "api key load failed", http.StatusBadGateway)
		return
	}

	endpoint, err := url.JoinPath(strings.TrimRight(s.cfg.MiniMaxBaseURL, "/"), "chat/completions")
	if err != nil {
		http.Error(w, "endpoint error", http.StatusInternalServerError)
		return
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		http.Error(w, "endpoint parse error", http.StatusInternalServerError)
		return
	}
	if s.cfg.GroupID != "" {
		q := u.Query()
		q.Set("GroupId", s.cfg.GroupID)
		u.RawQuery = q.Encode()
	}
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "request build failed", http.StatusInternalServerError)
		return
	}
	upReq.Header.Set("Authorization", "Bearer "+string(apiKey))
	upReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(upReq)
	if err != nil {
		s.log.Error("minimax streaming chat failed", "err", err)
		http.Error(w, "chat provider failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		observation := classifyProviderObservation(resp.StatusCode, raw, nil)
		_ = s.recordObservation(alias, observation, resp.Header)
		http.Error(w, "chat provider failed", http.StatusBadGateway)
		return
	}

	_ = s.auth.RecordSuccess(alias, s.clock())

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	filter := NewThinkFilter()
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		out := filter.ProcessSSELine(line)
		if out == "" {
			continue
		}
		_, _ = fmt.Fprintf(w, "%s\n\n", out)
		if canFlush {
			flusher.Flush()
		}
	}
}

func isStreamingRequest(body []byte) bool {
	var req struct {
		Stream bool `json:"stream"`
	}
	if json.Unmarshal(body, &req) != nil {
		return false
	}
	return req.Stream
}

func (s *Server) embed(ctx context.Context, model, embedType string, texts []string) ([][]float64, error) {
	payload := minimaxEmbeddingRequest{
		Model: model,
		Type:  embedType,
		Texts: texts,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < s.maxAttempts(); attempt++ {
		alias, err := s.pickAlias()
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		vectors, retry, err := s.tryEmbedding(ctx, body, texts, alias)
		if err == nil {
			return vectors, nil
		}
		if retry && attempt+1 < s.maxAttempts() {
			lastErr = err
			continue
		}
		return nil, err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("missing MiniMax API keys")
}

func (s *Server) tryEmbedding(ctx context.Context, body []byte, texts []string, alias minimaxauth.KeyAlias) ([][]float64, bool, error) {
	raw, status, header, err := s.postMiniMaxWithAlias(ctx, "embeddings", body, alias)
	if err != nil {
		return nil, s.recordObservation(alias, classifyProviderObservation(status, raw, err), header), err
	}
	parsed, err := decodeEmbeddingResponse(raw)
	if err != nil {
		return nil, s.recordObservation(alias, classifyProviderObservation(status, raw, err), header), err
	}
	if err := validateEmbeddingResponse(parsed, len(texts)); err != nil {
		return nil, s.recordObservation(alias, classifyProviderObservation(status, raw, err), header), err
	}
	_ = s.auth.RecordSuccess(alias, s.clock())
	return parsed.Vectors, false, nil
}

func validateEmbeddingResponse(parsed minimaxEmbeddingResponse, wantVectors int) error {
	if parsed.BaseResp.StatusCode != 0 {
		return fmt.Errorf("minimax base_resp %d: %s", parsed.BaseResp.StatusCode, parsed.BaseResp.StatusMsg)
	}
	if len(parsed.Vectors) != wantVectors {
		return fmt.Errorf("vector count %d != input count %d", len(parsed.Vectors), wantVectors)
	}
	return nil
}

func (s *Server) postMiniMax(ctx context.Context, path string, body []byte) ([]byte, int, error) {
	if s.maxAttempts() == 0 {
		return nil, 0, errors.New("missing MiniMax API keys")
	}
	var lastErr error
	var lastStatus int
	var lastBody []byte
	for attempt := 0; attempt < s.maxAttempts(); attempt++ {
		alias, err := s.pickAlias()
		if err != nil {
			if lastErr != nil {
				return lastBody, lastStatus, lastErr
			}
			return nil, 0, err
		}
		raw, status, header, err := s.postMiniMaxWithAlias(ctx, path, body, alias)
		observation := classifyProviderObservation(status, raw, err)
		if err == nil && observation == providerObservationSuccess {
			_ = s.auth.RecordSuccess(alias, s.clock())
			return raw, status, nil
		}
		if err == nil {
			err = fmt.Errorf("minimax retryable response body")
		}
		lastErr, lastStatus, lastBody = err, status, raw
		if !s.recordObservation(alias, observation, header) || attempt+1 == s.maxAttempts() {
			break
		}
	}
	return lastBody, lastStatus, lastErr
}

func (s *Server) postMiniMaxWithAlias(ctx context.Context, path string, body []byte, alias minimaxauth.KeyAlias) ([]byte, int, http.Header, error) {
	apiKey, err := s.secrets.Load(ctx, alias)
	if err != nil {
		return nil, 0, nil, err
	}
	return s.postMiniMaxWithKey(ctx, path, body, string(apiKey))
}

func (s *Server) postMiniMaxWithKey(ctx context.Context, path string, body []byte, apiKey string) ([]byte, int, http.Header, error) {
	endpoint, err := url.JoinPath(strings.TrimRight(s.cfg.MiniMaxBaseURL, "/"), path)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("endpoint: %w", err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("parse endpoint: %w", err)
	}
	if s.cfg.GroupID != "" {
		q := u.Query()
		q.Set("GroupId", s.cfg.GroupID)
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, resp.StatusCode, resp.Header, fmt.Errorf("minimax status %d", resp.StatusCode)
	}
	return raw, resp.StatusCode, resp.Header, nil
}

func (s *Server) apiKeys() []string {
	return apiKeysFromConfig(s.cfg)
}

func apiKeysFromConfig(cfg Config) []string {
	if len(cfg.APIKeys) > 0 {
		return cfg.APIKeys
	}
	if cfg.APIKey == "" {
		return nil
	}
	return []string{cfg.APIKey}
}

func (s *Server) maxAttempts() int {
	return len(s.aliases)
}

func (s *Server) pickAlias() (minimaxauth.KeyAlias, error) {
	if s.auth == nil {
		return "", errors.New("minimax auth service unavailable")
	}
	return s.auth.Pick()
}

type providerObservation int

const (
	providerObservationSuccess providerObservation = iota
	providerObservationRateLimited
	providerObservationQuotaExhausted
	providerObservationFailure
)

func (s *Server) recordObservation(alias minimaxauth.KeyAlias, observation providerObservation, header http.Header) bool {
	switch observation {
	case providerObservationRateLimited:
		_ = s.auth.RecordRateLimited(alias, s.clock(), parseRetryAfter(header, s.clock()))
		return true
	case providerObservationQuotaExhausted:
		_ = s.auth.RecordQuotaExhausted(alias, s.clock(), time.Time{})
		return true
	default:
		return false
	}
}

func classifyProviderObservation(status int, raw []byte, err error) providerObservation {
	if status == http.StatusTooManyRequests {
		return providerObservationRateLimited
	}
	if status == http.StatusPaymentRequired || status == http.StatusForbidden {
		return providerObservationQuotaExhausted
	}
	lower := strings.ToLower(string(raw))
	if err != nil {
		lower += " " + strings.ToLower(err.Error())
	}
	switch {
	case strings.Contains(lower, "quota"),
		strings.Contains(lower, "insufficient balance"),
		strings.Contains(lower, "payment required"):
		return providerObservationQuotaExhausted
	case strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "rate_limit"),
		strings.Contains(lower, "too many requests"):
		return providerObservationRateLimited
	case err != nil:
		return providerObservationFailure
	default:
		return providerObservationSuccess
	}
}

func parseRetryAfter(header http.Header, now time.Time) time.Duration {
	if header == nil {
		return 0
	}
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

type envSecretLoader struct {
	keys map[minimaxauth.KeyAlias][]byte
}

func newEnvSecretLoader(bindings []apiKeyBinding) *envSecretLoader {
	keys := make(map[minimaxauth.KeyAlias][]byte, len(bindings))
	for _, binding := range bindings {
		keys[binding.alias] = []byte(binding.key)
	}
	return &envSecretLoader{keys: keys}
}

func (l *envSecretLoader) Load(_ context.Context, alias minimaxauth.KeyAlias) ([]byte, error) {
	key, ok := l.keys[alias]
	if !ok {
		return nil, fmt.Errorf("missing MiniMax API key for alias %s", alias)
	}
	return append([]byte(nil), key...), nil
}

func decodeEmbeddingResponse(raw []byte) (minimaxEmbeddingResponse, error) {
	var parsed minimaxEmbeddingResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return parsed, fmt.Errorf("decode: %w", err)
	}
	return parsed, nil
}

func isProviderRetryable(status int, raw []byte, err error) bool {
	observation := classifyProviderObservation(status, raw, err)
	return observation == providerObservationRateLimited || observation == providerObservationQuotaExhausted
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
