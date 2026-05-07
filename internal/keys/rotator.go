package keys

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// RotatorConfig configures the dual-key rotator.
type RotatorConfig struct {
	Keys        []string
	BaseURL     string
	Client      *http.Client
	EvidenceOut io.Writer
	Clock       func() time.Time
}

// EvidenceEntry is a single NDJSON line emitted for each HTTP attempt.
// Key bytes are never included; only the index into the key slice.
type EvidenceEntry struct {
	Timestamp string  `json:"ts"`
	KeyIndex  int     `json:"key_index"`
	Status    int     `json:"status"`
	LatencyMs float64 `json:"latency_ms"`
	Rotated   bool    `json:"rotated,omitempty"`
}

// Rotator cycles through API keys and emits NDJSON evidence for every call.
type Rotator struct {
	cfg     RotatorConfig
	mu      sync.Mutex
	current int
	enc     *json.Encoder
}

// NewRotator returns a rotator ready to distribute calls across keys.
func NewRotator(cfg RotatorConfig) *Rotator {
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &Rotator{cfg: cfg}
}

// Call posts body to baseURL+path and returns the final HTTP status.
// On a 429 it rotates to the next key and retries once.
func (r *Rotator) Call(ctx context.Context, path string, body []byte) (int, error) {
	keyIdx := r.pickKey()

	status, err := r.doCall(ctx, path, body, keyIdx, false)
	if err != nil {
		return status, err
	}
	if status == http.StatusTooManyRequests && len(r.cfg.Keys) > 1 {
		newIdx := r.rotateKey()
		status, err = r.doCall(ctx, path, body, newIdx, true)
	}
	return status, err
}

func (r *Rotator) pickKey() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

func (r *Rotator) rotateKey() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = (r.current + 1) % len(r.cfg.Keys)
	return r.current
}

func (r *Rotator) doCall(ctx context.Context, path string, body []byte, keyIdx int, rotated bool) (int, error) {
	start := r.clock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Keys[keyIdx])
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.cfg.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	elapsed := time.Since(start)

	r.emitEvidence(EvidenceEntry{
		Timestamp: r.clock().Format(time.RFC3339),
		KeyIndex:  keyIdx,
		Status:    resp.StatusCode,
		LatencyMs: float64(elapsed.Milliseconds()),
		Rotated:   rotated,
	})

	return resp.StatusCode, nil
}

func (r *Rotator) clock() time.Time {
	if r.cfg.Clock != nil {
		return r.cfg.Clock()
	}
	return time.Now().UTC()
}

func (r *Rotator) emitEvidence(entry EvidenceEntry) {
	if r.cfg.EvidenceOut == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.enc == nil {
		r.enc = json.NewEncoder(r.cfg.EvidenceOut)
	}
	_ = r.enc.Encode(entry)
}
