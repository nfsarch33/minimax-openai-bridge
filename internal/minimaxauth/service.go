// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrNotConfigured is returned by Service methods that take an alias
// argument when the alias was never seen by Refresh. Catching this
// separately from ErrNoUsableKeys lets consumers tell "you misspelled
// minimax-api-3" apart from "all keys exhausted".
var ErrNotConfigured = errors.New("minimaxauth: alias not configured")

// Service is the public facade for the minimaxauth package. It owns the
// in-memory state map, the selector, and the audit log. The Service
// itself is safe for concurrent use.
//
// The MacBook runx CLI wires Service with a Source (1Password listing)
// and a NoopWriter or NDJSONWriter. SecretLoader is intentionally NOT
// part of this struct: secret bytes never leave fleet bridges.
type Service struct {
	source   Source
	selector Selector
	writer   EventWriter
	clock    func() time.Time

	mu     sync.Mutex
	states map[KeyAlias]State
}

// Option configures a Service via the functional-options pattern.
type Option func(*Service)

// WithClock overrides the time source. Tests inject a fake clock.
func WithClock(fn func() time.Time) Option {
	return func(s *Service) { s.clock = fn }
}

// WithEventWriter overrides the audit log destination. Defaults to
// NoopWriter so unit tests do not write files.
func WithEventWriter(w EventWriter) Option {
	return func(s *Service) { s.writer = w }
}

// NewService constructs a Service. Source and Selector MUST be non-nil.
func NewService(source Source, selector Selector, opts ...Option) (*Service, error) {
	if source == nil {
		return nil, errors.New("minimaxauth: nil Source")
	}
	if selector == nil {
		return nil, errors.New("minimaxauth: nil Selector")
	}
	s := &Service{
		source:   source,
		selector: selector,
		writer:   NoopWriter{},
		clock:    func() time.Time { return time.Now().UTC() },
		states:   make(map[KeyAlias]State),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Refresh re-enumerates aliases from Source and seeds states for any
// new aliases. Existing state is preserved so we do not lose rate-limit
// or quota observations across refreshes.
func (s *Service) Refresh(ctx context.Context) error {
	aliases, err := s.source.KeyAliases(ctx)
	if err != nil {
		return fmt.Errorf("minimaxauth: refresh: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := make(map[KeyAlias]struct{}, len(aliases))
	for _, a := range aliases {
		known[a] = struct{}{}
		if _, ok := s.states[a]; !ok {
			s.states[a] = State{Alias: a, Status: StatusUnknown}
		}
	}
	for a := range s.states {
		if _, ok := known[a]; !ok {
			delete(s.states, a)
		}
	}
	return nil
}

// Aliases returns the configured aliases in sorted order.
func (s *Service) Aliases() []KeyAlias {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KeyAlias, 0, len(s.states))
	for a := range s.states {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}

// Snapshot returns a defensive copy of the current state map. Mutations
// on the returned map MUST NOT affect the Service.
func (s *Service) Snapshot() map[KeyAlias]State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[KeyAlias]State, len(s.states))
	for k, v := range s.states {
		out[k] = v
	}
	return out
}

// Pick returns the next KeyAlias the selector wants to use.
func (s *Service) Pick() (KeyAlias, error) {
	s.mu.Lock()
	statesCopy := make(map[KeyAlias]State, len(s.states))
	for k, v := range s.states {
		statesCopy[k] = v
	}
	s.mu.Unlock()
	return s.selector.Pick(s.clock(), statesCopy)
}

// RecordSuccess marks an alias healthy. eventAt is the time the success
// happened on the bridge; if zero, the Service clock is used.
func (s *Service) RecordSuccess(alias KeyAlias, eventAt time.Time) error {
	now := s.clock()
	if eventAt.IsZero() {
		eventAt = now
	}
	s.mu.Lock()
	st, ok := s.states[alias]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrNotConfigured, string(alias))
	}
	st.Status = StatusAvailable
	st.LastSuccessAt = eventAt
	st.ConsecutiveFailures = 0
	st.LastFailureReason = ""
	st.RateLimitedUntil = time.Time{}
	st.QuotaResetHint = time.Time{}
	s.states[alias] = st
	s.mu.Unlock()
	return s.writer.Write(Event{
		EventAt:    eventAt,
		RecordedAt: now,
		Alias:      alias,
		Kind:       EventSuccess,
	})
}

// RecordRateLimited marks an alias as rate-limited. retryAfter is the
// hint for when the bridge can try again; zero means "use selector
// default backoff".
func (s *Service) RecordRateLimited(alias KeyAlias, eventAt time.Time, retryAfter time.Duration) error {
	now := s.clock()
	if eventAt.IsZero() {
		eventAt = now
	}
	s.mu.Lock()
	st, ok := s.states[alias]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrNotConfigured, string(alias))
	}
	st.Status = StatusRateLimited
	st.LastFailureAt = eventAt
	st.LastFailureReason = "429"
	st.ConsecutiveFailures++
	if retryAfter > 0 {
		st.RateLimitedUntil = eventAt.Add(retryAfter)
	} else {
		st.RateLimitedUntil = time.Time{}
	}
	s.states[alias] = st
	s.mu.Unlock()
	return s.writer.Write(Event{
		EventAt:    eventAt,
		RecordedAt: now,
		Alias:      alias,
		Kind:       EventRateLimited,
		Detail:     fmt.Sprintf("retry_after=%s", retryAfter),
	})
}

// RecordQuotaExhausted marks an alias as quota-exhausted. resetAt is the
// time the quota window resets; zero means "no reset hint, treat as
// indefinitely unavailable".
func (s *Service) RecordQuotaExhausted(alias KeyAlias, eventAt, resetAt time.Time) error {
	now := s.clock()
	if eventAt.IsZero() {
		eventAt = now
	}
	s.mu.Lock()
	st, ok := s.states[alias]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrNotConfigured, string(alias))
	}
	st.Status = StatusQuotaExhausted
	st.LastFailureAt = eventAt
	st.LastFailureReason = "quota_exhausted"
	st.ConsecutiveFailures++
	st.QuotaResetHint = resetAt
	s.states[alias] = st
	s.mu.Unlock()
	return s.writer.Write(Event{
		EventAt:    eventAt,
		RecordedAt: now,
		Alias:      alias,
		Kind:       EventQuotaExhausted,
		Detail:     fmt.Sprintf("reset_at=%s", resetAt.Format(time.RFC3339)),
	})
}

// Reset returns an alias to StatusUnknown. Operators use this from the
// CLI when they have manually verified a key works again before the
// recorded backoff/reset hint expires.
func (s *Service) Reset(alias KeyAlias) error {
	now := s.clock()
	s.mu.Lock()
	st, ok := s.states[alias]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrNotConfigured, string(alias))
	}
	st.Status = StatusUnknown
	st.LastFailureReason = ""
	st.ConsecutiveFailures = 0
	st.RateLimitedUntil = time.Time{}
	st.QuotaResetHint = time.Time{}
	s.states[alias] = st
	s.mu.Unlock()
	return s.writer.Write(Event{
		EventAt:    now,
		RecordedAt: now,
		Alias:      alias,
		Kind:       EventReset,
	})
}

// Close releases the EventWriter. Safe to call multiple times.
func (s *Service) Close() error {
	if s.writer == nil {
		return nil
	}
	return s.writer.Close()
}
