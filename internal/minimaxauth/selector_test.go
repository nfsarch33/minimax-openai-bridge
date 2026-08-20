// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"errors"
	"testing"
	"time"
)

func TestStickyWithFailover_PrefersFirstAvailableInSortedOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	s := &StickyWithFailoverSelector{Backoff: time.Minute}
	states := map[KeyAlias]State{
		"minimax-api-1": {Alias: "minimax-api-1", Status: StatusAvailable},
		"minimax-api-2": {Alias: "minimax-api-2", Status: StatusAvailable},
	}
	got, err := s.Pick(now, states)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got != "minimax-api-1" {
		t.Errorf("Pick = %q, want minimax-api-1 (lowest sorted alias)", got)
	}
}

func TestStickyWithFailover_FailsOverWhenFirstRateLimited(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	s := &StickyWithFailoverSelector{Backoff: time.Minute}
	states := map[KeyAlias]State{
		"minimax-api-1": {
			Alias:            "minimax-api-1",
			Status:           StatusRateLimited,
			RateLimitedUntil: now.Add(30 * time.Second),
		},
		"minimax-api-2": {Alias: "minimax-api-2", Status: StatusAvailable},
	}
	got, err := s.Pick(now, states)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got != "minimax-api-2" {
		t.Errorf("Pick = %q, want minimax-api-2 (failover)", got)
	}
}

func TestStickyWithFailover_FailsOverWhenFirstQuotaExhausted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	s := &StickyWithFailoverSelector{Backoff: time.Minute}
	states := map[KeyAlias]State{
		"minimax-api-1": {
			Alias:          "minimax-api-1",
			Status:         StatusQuotaExhausted,
			QuotaResetHint: now.Add(time.Hour),
		},
		"minimax-api-2": {Alias: "minimax-api-2", Status: StatusAvailable},
	}
	got, err := s.Pick(now, states)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got != "minimax-api-2" {
		t.Errorf("Pick = %q, want minimax-api-2 (quota failover)", got)
	}
}

func TestStickyWithFailover_RecoversWhenBackoffElapses(t *testing.T) {
	t.Parallel()
	earlier := time.Date(2026, 5, 7, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	s := &StickyWithFailoverSelector{Backoff: time.Minute}
	states := map[KeyAlias]State{
		"minimax-api-1": {
			Alias:            "minimax-api-1",
			Status:           StatusRateLimited,
			RateLimitedUntil: earlier,
		},
		"minimax-api-2": {
			Alias:            "minimax-api-2",
			Status:           StatusRateLimited,
			RateLimitedUntil: now.Add(time.Hour),
		},
	}
	got, err := s.Pick(now, states)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got != "minimax-api-1" {
		t.Errorf("Pick = %q, want minimax-api-1 (recovered after backoff)", got)
	}
}

func TestStickyWithFailover_NoUsableKeysReturnsErrNoUsableKeys(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	s := &StickyWithFailoverSelector{Backoff: time.Minute}
	states := map[KeyAlias]State{
		"minimax-api-1": {
			Alias:            "minimax-api-1",
			Status:           StatusRateLimited,
			RateLimitedUntil: now.Add(time.Hour),
		},
		"minimax-api-2": {
			Alias:          "minimax-api-2",
			Status:         StatusQuotaExhausted,
			QuotaResetHint: now.Add(2 * time.Hour),
		},
	}
	_, err := s.Pick(now, states)
	if !errors.Is(err, ErrNoUsableKeys) {
		t.Fatalf("Pick err = %v, want ErrNoUsableKeys", err)
	}
}

func TestStickyWithFailover_EmptyStateMapReturnsErrNoConfiguredKeys(t *testing.T) {
	t.Parallel()
	s := &StickyWithFailoverSelector{Backoff: time.Minute}
	_, err := s.Pick(time.Now(), map[KeyAlias]State{})
	if !errors.Is(err, ErrNoConfiguredKeys) {
		t.Fatalf("Pick err = %v, want ErrNoConfiguredKeys", err)
	}
}

func TestStickyWithFailover_DefaultBackoffWhenZero(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	s := &StickyWithFailoverSelector{} // Backoff zero
	states := map[KeyAlias]State{
		"minimax-api-1": {
			Alias:         "minimax-api-1",
			Status:        StatusRateLimited,
			LastFailureAt: now.Add(-2 * defaultRateLimitBackoff),
		},
		"minimax-api-2": {Alias: "minimax-api-2", Status: StatusAvailable},
	}
	got, err := s.Pick(now, states)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got != "minimax-api-1" {
		t.Errorf("with zero Backoff selector should fall back to defaultRateLimitBackoff and pick minimax-api-1; got %q", got)
	}
}
