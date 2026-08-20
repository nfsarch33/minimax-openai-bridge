// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// defaultVault is the only 1Password vault this package recognises. Other
// vaults are rejected so a misconfigured runner cannot leak credentials
// from an unrelated work vault.
const defaultVault = "<vault-name>"

// ErrInvalidAlias is returned when configuration contains an alias that
// does not match KeyAlias.IsValid (e.g. wrong prefix, contains spaces,
// uppercase). Argv-only invariants depend on this rejection.
var ErrInvalidAlias = errors.New("minimaxauth: invalid alias")

// Source enumerates the configured KeyAlias values. It MUST NOT return
// secret bytes. Implementations live behind this interface so tests
// avoid spawning the real `op` binary or reading real config.
type Source interface {
	KeyAliases(ctx context.Context) ([]KeyAlias, error)
}

// SecretLoader fetches the secret bytes for a single alias. This
// interface is intentionally separate from Source: on the MacBook the
// runx CLI never wires a SecretLoader, so the binary cannot exfiltrate
// MiniMax keys even if compromised. Fleet bridges (off-MacBook) wire
// their own implementation when they need to call the MiniMax API.
type SecretLoader interface {
	Load(ctx context.Context, alias KeyAlias) ([]byte, error)
}

// OPRunner is the seam for executing the 1Password CLI. Tests inject a
// fake; production wires the real `op item list --vault <vault>
// --format=json` invocation. The runner returns raw JSON bytes.
type OPRunner func(ctx context.Context, vault string) ([]byte, error)

// OPItemListSource enumerates KeyAlias values from a 1Password vault by
// listing item titles. It never reads item field values, so the source
// cannot return secret bytes by construction.
type OPItemListSource struct {
	// Vault defaults to "<vault-name>" when empty.
	Vault string
	// Runner is the 1Password CLI seam. Must be non-nil.
	Runner OPRunner
}

// KeyAliases returns the set of valid KeyAlias values found in the
// configured vault, sorted ascending. Items with invalid alias names are
// silently dropped so unrelated vault entries do not break enumeration.
func (s *OPItemListSource) KeyAliases(ctx context.Context) ([]KeyAlias, error) {
	if s.Runner == nil {
		return nil, errors.New("minimaxauth: OPItemListSource: nil Runner")
	}
	vault := s.Vault
	if vault == "" {
		vault = defaultVault
	}
	raw, err := s.Runner(ctx, vault)
	if err != nil {
		return nil, fmt.Errorf("minimaxauth: op runner: %w", err)
	}
	var items []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("minimaxauth: decode op output: %w", err)
	}
	seen := make(map[KeyAlias]struct{}, len(items))
	out := make([]KeyAlias, 0, len(items))
	for _, item := range items {
		alias := KeyAlias(item.Title)
		if !alias.IsValid() {
			continue
		}
		if _, dup := seen[alias]; dup {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out, nil
}

// StaticSource is a Source backed by a fixed alias slice. It is useful
// for tests and for offline runs where 1Password CLI is unavailable.
type StaticSource []KeyAlias

// KeyAliases returns the configured aliases, sorted ascending. Any
// invalid alias causes ErrInvalidAlias so misconfiguration fails loudly.
func (s StaticSource) KeyAliases(_ context.Context) ([]KeyAlias, error) {
	out := make([]KeyAlias, 0, len(s))
	for _, a := range s {
		if !a.IsValid() {
			return nil, fmt.Errorf("%w: %q", ErrInvalidAlias, string(a))
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out, nil
}
