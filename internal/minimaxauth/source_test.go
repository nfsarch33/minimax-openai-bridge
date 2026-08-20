// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOPItemListSource_FiltersByPrefix(t *testing.T) {
	t.Parallel()
	runner := func(ctx context.Context, vault string) ([]byte, error) {
		if vault != "<vault-name>" {
			t.Fatalf("vault = %q, want <vault-name>", vault)
		}
		return []byte(`[
			{"id":"a","title":"minimax-api-1"},
			{"id":"b","title":"minimax-api-2"},
			{"id":"c","title":"MiniMax API 1"},
			{"id":"d","title":"openai-key"},
			{"id":"e","title":"minimax-api"}
		]`), nil
	}
	src := &OPItemListSource{Vault: "<vault-name>", Runner: runner}
	got, err := src.KeyAliases(context.Background())
	if err != nil {
		t.Fatalf("KeyAliases: %v", err)
	}
	want := []KeyAlias{"minimax-api", "minimax-api-1", "minimax-api-2"}
	if len(got) != len(want) {
		t.Fatalf("got %d aliases, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("alias[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestOPItemListSource_RejectsInvalidAliases(t *testing.T) {
	t.Parallel()
	runner := func(ctx context.Context, vault string) ([]byte, error) {
		return []byte(`[
			{"id":"a","title":"minimax-api-1"},
			{"id":"x","title":"minimax-api with spaces"},
			{"id":"y","title":"MINIMAX-API-2"}
		]`), nil
	}
	src := &OPItemListSource{Vault: "<vault-name>", Runner: runner}
	got, err := src.KeyAliases(context.Background())
	if err != nil {
		t.Fatalf("KeyAliases: %v", err)
	}
	if len(got) != 1 || got[0] != "minimax-api-1" {
		t.Errorf("got %v, want [minimax-api-1]", got)
	}
}

func TestOPItemListSource_PropagatesRunnerError(t *testing.T) {
	t.Parallel()
	want := errors.New("op: not signed in")
	runner := func(ctx context.Context, vault string) ([]byte, error) { return nil, want }
	src := &OPItemListSource{Vault: "<vault-name>", Runner: runner}
	_, err := src.KeyAliases(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wraps %v", err, want)
	}
}

func TestOPItemListSource_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	runner := func(ctx context.Context, vault string) ([]byte, error) {
		return []byte(`not-json`), nil
	}
	src := &OPItemListSource{Vault: "<vault-name>", Runner: runner}
	_, err := src.KeyAliases(context.Background())
	if err == nil {
		t.Fatal("want error on malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error = %v, want it to mention decode", err)
	}
}

func TestOPItemListSource_DefaultVaultIsCursorIronClaw(t *testing.T) {
	t.Parallel()
	called := false
	runner := func(ctx context.Context, vault string) ([]byte, error) {
		called = true
		if vault != "<vault-name>" {
			t.Errorf("default vault = %q, want <vault-name>", vault)
		}
		return []byte(`[]`), nil
	}
	src := &OPItemListSource{Runner: runner}
	if _, err := src.KeyAliases(context.Background()); err != nil {
		t.Fatalf("KeyAliases: %v", err)
	}
	if !called {
		t.Fatal("runner never called")
	}
}

func TestStaticSource_ReturnsConfiguredAliases(t *testing.T) {
	t.Parallel()
	src := StaticSource{"minimax-api-2", "minimax-api-1"}
	got, err := src.KeyAliases(context.Background())
	if err != nil {
		t.Fatalf("KeyAliases: %v", err)
	}
	want := []KeyAlias{"minimax-api-1", "minimax-api-2"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Errorf("alias[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStaticSource_RejectsInvalidEntries(t *testing.T) {
	t.Parallel()
	src := StaticSource{"minimax-api-1", "MINIMAX-INVALID"}
	_, err := src.KeyAliases(context.Background())
	if err == nil || !errors.Is(err, ErrInvalidAlias) {
		t.Fatalf("err = %v, want wraps ErrInvalidAlias", err)
	}
}
