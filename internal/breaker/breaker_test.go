package breaker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreaker_ClosedOnSuccess(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 3, OpenTimeout: 100 * time.Millisecond})

	for i := 0; i < 5; i++ {
		_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
	}
	if b.State() != "closed" {
		t.Errorf("state = %q, want 'closed'", b.State())
	}
}

func TestBreaker_OpensAfterConsecutiveFailures(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 3, OpenTimeout: 100 * time.Millisecond})

	failing := func(ctx context.Context) (any, error) {
		return nil, errors.New("downstream error")
	}

	for i := 0; i < 3; i++ {
		_, err := b.Execute(context.Background(), failing)
		if err == nil {
			t.Fatalf("expected error on failure %d, got nil", i)
		}
	}

	if b.State() != "open" {
		t.Errorf("state = %q, want 'open'", b.State())
	}

	_, err := b.Execute(context.Background(), failing)
	if !errors.Is(err, ErrOpen) {
		t.Errorf("expected ErrOpen when circuit is open, got: %v", err)
	}
}

func TestBreaker_OpenRejectsFast(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 2, OpenTimeout: 1 * time.Second})

	failing := func(ctx context.Context) (any, error) {
		return nil, errors.New("fail")
	}
	for i := 0; i < 2; i++ {
		_, _ = b.Execute(context.Background(), failing)
	}

	if b.State() != "open" {
		t.Fatalf("expected open state, got %q", b.State())
	}

	calls := 0
	rejected := func(ctx context.Context) (any, error) {
		calls++
		return "ok", nil
	}

	for i := 0; i < 5; i++ {
		_, err := b.Execute(context.Background(), rejected)
		if !errors.Is(err, ErrOpen) {
			t.Errorf("call %d: expected ErrOpen, got %v", i, err)
		}
	}
	if calls != 0 {
		t.Errorf("expected 0 calls to reach downstream, got %d", calls)
	}
}

func TestBreaker_HalfOpenAllowsProbe(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 2, OpenTimeout: 50 * time.Millisecond})

	failing := func(ctx context.Context) (any, error) {
		return nil, errors.New("fail")
	}
	for i := 0; i < 2; i++ {
		_, _ = b.Execute(context.Background(), failing)
	}

	if b.State() != "open" {
		t.Fatalf("expected open, got %q", b.State())
	}

	time.Sleep(80 * time.Millisecond)

	if b.State() != "half-open" {
		t.Fatalf("expected half-open after timeout, got %q", b.State())
	}

	_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Errorf("half-open probe should succeed, got %v", err)
	}

	if b.State() != "closed" {
		t.Errorf("state after successful probe = %q, want 'closed'", b.State())
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 2, OpenTimeout: 50 * time.Millisecond})

	failing := func(ctx context.Context) (any, error) {
		return nil, errors.New("still down")
	}
	for i := 0; i < 2; i++ {
		_, _ = b.Execute(context.Background(), failing)
	}

	time.Sleep(80 * time.Millisecond)
	if b.State() != "half-open" {
		t.Fatalf("expected half-open, got %q", b.State())
	}

	_, _ = b.Execute(context.Background(), failing)

	if b.State() != "open" {
		t.Errorf("state = %q, want 'open' after probe failure", b.State())
	}
}

func TestBreaker_OnStateChange(t *testing.T) {
	transitions := []string{}
	b := New(Settings{
		Name:             "test",
		FailureThreshold: 2,
		OpenTimeout:      50 * time.Millisecond,
		OnStateChange: func(name, from, to string) {
			transitions = append(transitions, from+"->"+to)
		},
	})

	failing := func(ctx context.Context) (any, error) {
		return nil, errors.New("fail")
	}
	for i := 0; i < 2; i++ {
		_, _ = b.Execute(context.Background(), failing)
	}

	if len(transitions) == 0 || transitions[0] != "closed->open" {
		t.Errorf("expected first transition closed->open, got %v", transitions)
	}
}

func TestBreaker_Stats(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 5})

	for i := 0; i < 3; i++ {
		_, _ = b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			return "ok", nil
		})
	}
	for i := 0; i < 2; i++ {
		_, _ = b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			return nil, errors.New("fail")
		})
	}

	stats := b.Stats()
	if stats.Successes != 3 {
		t.Errorf("Successes = %d, want 3", stats.Successes)
	}
	if stats.Failures != 2 {
		t.Errorf("Failures = %d, want 2", stats.Failures)
	}
	if stats.State != "closed" {
		t.Errorf("State = %q, want 'closed'", stats.State)
	}
}
