package breaker

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker"
)

var ErrOpen = gobreaker.ErrOpenState

type Settings struct {
	Name             string
	FailureThreshold uint32
	HalfOpenMax      uint32
	OpenTimeout      time.Duration
	Interval         time.Duration
	OnStateChange    func(name, from, to string)
}

type Breaker struct {
	cb   *gobreaker.CircuitBreaker
	name string

	failures  atomic.Int64
	successes atomic.Int64
	rejects   atomic.Int64
}

func New(s Settings) *Breaker {
	if s.Interval == 0 {
		s.Interval = 60 * time.Second
	}
	if s.HalfOpenMax == 0 {
		s.HalfOpenMax = 1
	}
	if s.OpenTimeout == 0 {
		s.OpenTimeout = 30 * time.Second
	}

	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        s.Name,
		MaxRequests: s.HalfOpenMax,
		Interval:    s.Interval,
		Timeout:     s.OpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= s.FailureThreshold
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			if s.OnStateChange != nil {
				s.OnStateChange(name, from.String(), to.String())
			}
		},
	})

	return &Breaker{cb: cb, name: s.Name}
}

func (b *Breaker) Execute(ctx context.Context, fn func(ctx context.Context) (any, error)) (any, error) {
	v, err := b.cb.Execute(func() (any, error) {
		out, err := fn(ctx)
		if err != nil {
			b.failures.Add(1)
		} else {
			b.successes.Add(1)
		}
		return out, err
	})
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		b.rejects.Add(1)
	}
	return v, err
}

func (b *Breaker) State() string {
	return b.cb.State().String()
}

func (b *Breaker) Counts() gobreaker.Counts {
	return b.cb.Counts()
}

type Stats struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Failures  int64  `json:"failures"`
	Successes int64  `json:"successes"`
	Rejects   int64  `json:"rejects"`
}

func (b *Breaker) Stats() Stats {
	return Stats{
		Name:      b.name,
		State:     b.State(),
		Failures:  b.failures.Load(),
		Successes: b.successes.Load(),
		Rejects:   b.rejects.Load(),
	}
}
