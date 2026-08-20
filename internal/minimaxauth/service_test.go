// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
package minimaxauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func newTestService(t *testing.T, src Source, opts ...Option) (*Service, *captureWriter) {
	t.Helper()
	w := &captureWriter{}
	defaults := []Option{WithEventWriter(w)}
	svc, err := NewService(src, &StickyWithFailoverSelector{Backoff: time.Minute}, append(defaults, opts...)...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, w
}

type captureWriter struct {
	mu     sync.Mutex
	events []Event
	closed bool
}

func (w *captureWriter) Write(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, e)
	return nil
}
func (w *captureWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}
func (w *captureWriter) snapshot() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Event(nil), w.events...)
}

func TestService_NewServiceRejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil, &StickyWithFailoverSelector{}); err == nil {
		t.Error("nil Source must error")
	}
	if _, err := NewService(StaticSource{"minimax-api-1"}, nil); err == nil {
		t.Error("nil Selector must error")
	}
}

func TestService_RefreshSeedsUnknownAliases(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, StaticSource{"minimax-api-2", "minimax-api-1"})
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got := svc.Aliases()
	want := []KeyAlias{"minimax-api-1", "minimax-api-2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alias[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, a := range want {
		if svc.Snapshot()[a].Status != StatusUnknown {
			t.Errorf("alias %q seeded with status %q, want unknown", a, svc.Snapshot()[a].Status)
		}
	}
}

func TestService_RefreshDropsRemovedAliases(t *testing.T) {
	t.Parallel()
	src := &mutableSource{aliases: []KeyAlias{"minimax-api-1", "minimax-api-2"}}
	svc, _ := newTestService(t, src)
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	src.aliases = []KeyAlias{"minimax-api-1"}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}
	if got := svc.Aliases(); len(got) != 1 || got[0] != "minimax-api-1" {
		t.Errorf("Aliases after drop = %v, want [minimax-api-1]", got)
	}
}

type mutableSource struct {
	mu      sync.Mutex
	aliases []KeyAlias
}

func (m *mutableSource) KeyAliases(_ context.Context) ([]KeyAlias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]KeyAlias(nil), m.aliases...)
	return out, nil
}

func TestService_RefreshPropagatesSourceError(t *testing.T) {
	t.Parallel()
	want := errors.New("op offline")
	svc, _ := newTestService(t, errSource{err: want})
	err := svc.Refresh(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wraps %v", err, want)
	}
}

type errSource struct{ err error }

func (e errSource) KeyAliases(_ context.Context) ([]KeyAlias, error) { return nil, e.err }

func TestService_RecordSuccessClearsFailureState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	svc, log := newTestService(t,
		StaticSource{"minimax-api-1"},
		WithClock(func() time.Time { return now }),
	)
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := svc.RecordRateLimited("minimax-api-1", now, time.Minute); err != nil {
		t.Fatalf("RecordRateLimited: %v", err)
	}
	if err := svc.RecordSuccess("minimax-api-1", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	st := svc.Snapshot()["minimax-api-1"]
	if st.Status != StatusAvailable {
		t.Errorf("status = %q, want available", st.Status)
	}
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", st.ConsecutiveFailures)
	}
	if !st.RateLimitedUntil.IsZero() {
		t.Errorf("RateLimitedUntil = %v, want zero", st.RateLimitedUntil)
	}
	events := log.snapshot()
	if len(events) != 2 || events[0].Kind != EventRateLimited || events[1].Kind != EventSuccess {
		t.Errorf("events = %+v", events)
	}
}

func TestService_RecordRateLimitedSetsBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t,
		StaticSource{"minimax-api-1"},
		WithClock(func() time.Time { return now }),
	)
	_ = svc.Refresh(context.Background())
	if err := svc.RecordRateLimited("minimax-api-1", now, 30*time.Second); err != nil {
		t.Fatalf("RecordRateLimited: %v", err)
	}
	st := svc.Snapshot()["minimax-api-1"]
	if !st.RateLimitedUntil.Equal(now.Add(30 * time.Second)) {
		t.Errorf("RateLimitedUntil = %v, want %v", st.RateLimitedUntil, now.Add(30*time.Second))
	}
	if st.Status != StatusRateLimited {
		t.Errorf("status = %q", st.Status)
	}
	if st.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", st.ConsecutiveFailures)
	}
}

func TestService_RecordQuotaExhaustedSetsReset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	svc, _ := newTestService(t,
		StaticSource{"minimax-api-1"},
		WithClock(func() time.Time { return now }),
	)
	_ = svc.Refresh(context.Background())
	if err := svc.RecordQuotaExhausted("minimax-api-1", now, reset); err != nil {
		t.Fatalf("RecordQuotaExhausted: %v", err)
	}
	st := svc.Snapshot()["minimax-api-1"]
	if !st.QuotaResetHint.Equal(reset) {
		t.Errorf("QuotaResetHint = %v, want %v", st.QuotaResetHint, reset)
	}
}

func TestService_RecordOnUnknownAliasErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, StaticSource{"minimax-api-1"})
	_ = svc.Refresh(context.Background())
	for _, fn := range []func() error{
		func() error { return svc.RecordSuccess("minimax-api-9", time.Time{}) },
		func() error { return svc.RecordRateLimited("minimax-api-9", time.Time{}, time.Minute) },
		func() error { return svc.RecordQuotaExhausted("minimax-api-9", time.Time{}, time.Time{}) },
		func() error { return svc.Reset("minimax-api-9") },
	} {
		if err := fn(); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("err = %v, want wraps ErrNotConfigured", err)
		}
	}
}

func TestService_PickFailoverThenRecover(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	clk := now
	svc, _ := newTestService(t,
		StaticSource{"minimax-api-1", "minimax-api-2"},
		WithClock(func() time.Time { return clk }),
	)
	_ = svc.Refresh(context.Background())

	got, err := svc.Pick()
	if err != nil || got != "minimax-api-1" {
		t.Fatalf("first pick = %q,%v; want minimax-api-1", got, err)
	}

	_ = svc.RecordRateLimited("minimax-api-1", clk, 10*time.Minute)

	got, err = svc.Pick()
	if err != nil || got != "minimax-api-2" {
		t.Fatalf("after rate-limit pick = %q,%v; want minimax-api-2", got, err)
	}

	clk = now.Add(11 * time.Minute)
	got, err = svc.Pick()
	if err != nil || got != "minimax-api-1" {
		t.Fatalf("after recovery pick = %q,%v; want minimax-api-1", got, err)
	}
}

func TestService_PickReturnsErrNoUsableKeysWhenAllExhausted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t,
		StaticSource{"minimax-api-1", "minimax-api-2"},
		WithClock(func() time.Time { return now }),
	)
	_ = svc.Refresh(context.Background())
	_ = svc.RecordQuotaExhausted("minimax-api-1", now, now.Add(time.Hour))
	_ = svc.RecordQuotaExhausted("minimax-api-2", now, now.Add(2*time.Hour))
	if _, err := svc.Pick(); !errors.Is(err, ErrNoUsableKeys) {
		t.Fatalf("err = %v, want ErrNoUsableKeys", err)
	}
}

func TestService_ConcurrentRecordsAreSafe(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t,
		StaticSource{"minimax-api-1", "minimax-api-2"},
		WithClock(func() time.Time { return now }),
	)
	_ = svc.Refresh(context.Background())
	const goroutines = 8
	const iterations = 200
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = svc.RecordSuccess("minimax-api-1", now)
				_ = svc.RecordRateLimited("minimax-api-2", now, time.Minute)
			}
		}(i)
	}
	wg.Wait()
	st := svc.Snapshot()["minimax-api-2"]
	if st.ConsecutiveFailures < goroutines*iterations {
		t.Errorf("ConsecutiveFailures = %d, want >= %d", st.ConsecutiveFailures, goroutines*iterations)
	}
}

func TestService_SnapshotIsIndependent(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, StaticSource{"minimax-api-1"})
	_ = svc.Refresh(context.Background())
	snap := svc.Snapshot()
	snap["minimax-api-1"] = State{Status: StatusQuotaExhausted}
	if svc.Snapshot()["minimax-api-1"].Status == StatusQuotaExhausted {
		t.Error("Snapshot must return defensive copy")
	}
}

func TestService_CloseClosesWriter(t *testing.T) {
	t.Parallel()
	svc, w := newTestService(t, StaticSource{"minimax-api-1"})
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !w.closed {
		t.Error("Close did not close writer")
	}
}
