package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nfsarch33/minimax-openai-bridge/internal/bridge"
)

// TestVersionConstantIsSet sanity-checks that the version constant
// shipped in this binary is non-empty so the --version flag never
// prints a blank line. This guards against silent breakage when the
// release script forgets to bump the constant.
func TestVersionConstantIsSet(t *testing.T) {
	if strings.TrimSpace(version) == "" {
		t.Fatal("version constant is empty")
	}
}

// TestLoadConfigDefaultsAreSensible verifies that when no env vars are
// set, LoadConfig returns the documented defaults. Other config tests
// live in internal/bridge/config_test.go (env-var driven); this test
// covers the cmd-level wiring that main() relies on.
func TestLoadConfigDefaultsAreSensible(t *testing.T) {
	t.Setenv("BRIDGE_ADDR", "")
	t.Setenv("MINIMAX_BASE_URL", "")
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("MINIMAX_API_KEY_1", "")
	t.Setenv("MINIMAX_API_KEY_2", "")
	t.Setenv("MINIMAX_API_KEYS", "")
	t.Setenv("MINIMAX_GROUP_ID", "")

	cfg := bridge.LoadConfig()
	if cfg.ListenAddr != "127.0.0.1:8500" {
		t.Errorf("ListenAddr = %q, want 127.0.0.1:8500", cfg.ListenAddr)
	}
	if cfg.MiniMaxBaseURL != "https://api.minimax.chat/v1" {
		t.Errorf("MiniMaxBaseURL = %q, want default minimax api", cfg.MiniMaxBaseURL)
	}
	if cfg.Model != "embo-01" {
		t.Errorf("Model = %q, want embo-01", cfg.Model)
	}
}

// TestServerHandlerHealthzRoundtrip is the simple round-trip smoke
// test the v2.6.0 plan calls for. We construct a Server with the
// default Config, mount its Handler in an httptest.Server, hit
// /healthz, and verify a 200 + body == "ok\n". This exercises the
// production wiring main() uses (Config -> Server.NewServer ->
// Server.Handler) without going through ListenAndServe.
func TestServerHandlerHealthzRoundtrip(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "TODO_test_only_does_not_call_minimax")
	cfg := bridge.LoadConfig()
	server := bridge.NewServer(cfg, &http.Client{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "ok\n" {
		t.Fatalf("body = %q, want \"ok\\n\"", string(body))
	}
}
