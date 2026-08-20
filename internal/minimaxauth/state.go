// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"strings"
	"time"
)

// KeyAlias is the stable name of a MiniMax credential. It MUST equal a
// 1Password item title in the <vault-name> vault. Argv anywhere in
// runx accepts only KeyAlias values; secret bytes are never on argv.
type KeyAlias string

// minimumAliasPrefix is the only allowed prefix for KeyAlias values so
// the vault item space stays bounded. Adding new prefixes requires an
// ADR and a config change; this avoids accidental cross-vendor reuse.
const minimumAliasPrefix = "minimax-api"

// IsValid returns true when the alias matches the lower-kebab convention
// used by the 1Password vault: starts with "minimax-api", no spaces, no
// slashes, ASCII only.
func (a KeyAlias) IsValid() bool {
	if a == "" {
		return false
	}
	s := string(a)
	if !strings.HasPrefix(s, minimumAliasPrefix) {
		return false
	}
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return false
		}
		if r == ' ' || r == '/' || r == '\\' || r == '\t' {
			return false
		}
	}
	return true
}

// Status of a key after recent observations from the fleet bridge.
type Status string

const (
	// StatusUnknown is the initial state before any observation; the
	// selector treats it as available so the first attempt happens.
	StatusUnknown Status = "unknown"

	// StatusAvailable means the key is healthy as of the last observation.
	StatusAvailable Status = "available"

	// StatusRateLimited means the bridge saw an HTTP 429. The key may
	// recover after RateLimitedUntil; the selector backs off until then.
	StatusRateLimited Status = "rate_limited"

	// StatusQuotaExhausted means the bridge saw a quota_exceeded error.
	// The key recovers only after QuotaResetHint (when known).
	StatusQuotaExhausted Status = "quota_exhausted"
)

// State is the runtime selection state for a single key. All times are
// UTC. Zero-valued times mean "no observation yet".
type State struct {
	Alias               KeyAlias
	Status              Status
	LastSuccessAt       time.Time
	LastFailureAt       time.Time
	LastFailureReason   string
	ConsecutiveFailures int
	RateLimitedUntil    time.Time
	QuotaResetHint      time.Time
}

// IsUsableNow returns true when the key should be considered for the
// next pick. defaultBackoff is used only when Status is StatusRateLimited
// and RateLimitedUntil is zero.
func (s State) IsUsableNow(now time.Time, defaultBackoff time.Duration) bool {
	switch s.Status {
	case StatusUnknown, StatusAvailable:
		return true
	case StatusRateLimited:
		if s.RateLimitedUntil.IsZero() {
			return now.Sub(s.LastFailureAt) >= defaultBackoff
		}
		return !now.Before(s.RateLimitedUntil)
	case StatusQuotaExhausted:
		if s.QuotaResetHint.IsZero() {
			return false
		}
		return !now.Before(s.QuotaResetHint)
	default:
		return false
	}
}
