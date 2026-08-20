// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"errors"
	"sort"
	"time"
)

// defaultRateLimitBackoff is used when a Selector is constructed with
// zero Backoff. 60s matches MiniMax's documented "wait at least one
// minute" guidance for 429 responses.
const defaultRateLimitBackoff = 60 * time.Second

// ErrNoUsableKeys signals that every configured key is currently in a
// non-usable state (rate-limited, quota-exhausted, or otherwise unhealthy)
// and the consumer must back off.
var ErrNoUsableKeys = errors.New("minimaxauth: no usable keys")

// ErrNoConfiguredKeys signals that the state map is empty, i.e. nothing
// has been loaded from the Source. This is a configuration problem, not
// a transient one.
var ErrNoConfiguredKeys = errors.New("minimaxauth: no configured keys")

// Selector chooses the next KeyAlias to try given current state. It
// MUST be deterministic for the same inputs so behaviour is testable
// and auditable.
type Selector interface {
	Pick(now time.Time, states map[KeyAlias]State) (KeyAlias, error)
}

// StickyWithFailoverSelector prefers the first usable key in sorted alias
// order. The selector is "sticky" in the sense that once a key is healthy
// it stays selected; rotation only happens on rate-limit or quota
// exhaustion. This is more cache-friendly for embedding workloads than a
// strict round-robin.
type StickyWithFailoverSelector struct {
	// Backoff is the minimum wait after a rate-limit observation before
	// a key is reconsidered. Zero falls back to defaultRateLimitBackoff.
	Backoff time.Duration
}

// Pick returns the lowest-sorted usable alias, or ErrNoUsableKeys when
// all configured keys are unhealthy.
func (s *StickyWithFailoverSelector) Pick(now time.Time, states map[KeyAlias]State) (KeyAlias, error) {
	if len(states) == 0 {
		return "", ErrNoConfiguredKeys
	}
	backoff := s.Backoff
	if backoff <= 0 {
		backoff = defaultRateLimitBackoff
	}
	for _, alias := range sortedAliases(states) {
		st := states[alias]
		if st.IsUsableNow(now, backoff) {
			return alias, nil
		}
	}
	return "", ErrNoUsableKeys
}

func sortedAliases(states map[KeyAlias]State) []KeyAlias {
	out := make([]KeyAlias, 0, len(states))
	for k := range states {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}
