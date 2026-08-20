// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"testing"
	"time"
)

func TestStatusString(t *testing.T) {
	t.Parallel()
	cases := map[Status]string{
		StatusUnknown:        "unknown",
		StatusAvailable:      "available",
		StatusRateLimited:    "rate_limited",
		StatusQuotaExhausted: "quota_exhausted",
	}
	for s, want := range cases {
		if string(s) != want {
			t.Errorf("Status %v: got %q, want %q", s, string(s), want)
		}
	}
}

func TestKeyAliasIsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		alias KeyAlias
		want  bool
	}{
		{"minimax-api-1", true},
		{"minimax-api-2", true},
		{"minimax-api", true},
		{"", false},
		{"MINIMAX-API-1", false}, // case-sensitive: 1Password titles are lower-kebab
		{"minimax api 1", false}, // no spaces
		{"minimax/api/1", false}, // no slashes
		{"openai-api", false},    // wrong prefix
	}
	for _, tc := range cases {
		if got := tc.alias.IsValid(); got != tc.want {
			t.Errorf("KeyAlias(%q).IsValid() = %v, want %v", tc.alias, got, tc.want)
		}
	}
}

func TestStateIsUsableNow_AvailableKey(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	st := State{
		Alias:  "minimax-api-1",
		Status: StatusAvailable,
	}
	if !st.IsUsableNow(now, time.Minute) {
		t.Fatal("available key should be usable")
	}
}

func TestStateIsUsableNow_RateLimitedWithinBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	st := State{
		Alias:            "minimax-api-1",
		Status:           StatusRateLimited,
		RateLimitedUntil: now.Add(30 * time.Second),
	}
	if st.IsUsableNow(now, time.Minute) {
		t.Fatal("rate-limited key with future RateLimitedUntil should not be usable")
	}
}

func TestStateIsUsableNow_RateLimitedAfterBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	st := State{
		Alias:            "minimax-api-1",
		Status:           StatusRateLimited,
		RateLimitedUntil: now.Add(-time.Second),
	}
	if !st.IsUsableNow(now, time.Minute) {
		t.Fatal("rate-limited key past its backoff should be usable again")
	}
}

func TestStateIsUsableNow_QuotaExhaustedHonoursReset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	stPending := State{
		Alias:          "minimax-api-1",
		Status:         StatusQuotaExhausted,
		QuotaResetHint: now.Add(time.Hour),
	}
	if stPending.IsUsableNow(now, time.Minute) {
		t.Fatal("quota-exhausted key should not be usable before reset hint")
	}
	stReset := stPending
	stReset.QuotaResetHint = now.Add(-time.Second)
	if !stReset.IsUsableNow(now, time.Minute) {
		t.Fatal("quota-exhausted key past its reset hint should be usable for retry")
	}
}

func TestStateIsUsableNow_UnknownTreatedAsAvailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	st := State{Alias: "minimax-api-2", Status: StatusUnknown}
	if !st.IsUsableNow(now, time.Minute) {
		t.Fatal("unknown-status key should be usable (optimistic first attempt)")
	}
}
