package main

import (
	"errors"
	"time"
)

type TimeProvider interface {
	Now() time.Time
}

type RealTimeTimeProvider struct{}

func (RealTimeTimeProvider) Now() time.Time {
	return time.Now()
}

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

var (
	ErrTooManyRequests = errors.New("too many requests")
	ErrOpenState       = errors.New("state is open")
)

type Option func(*CircuitBreaker)

func WithTimeout(timeout time.Duration) Option {
	return func(cb *CircuitBreaker) {
		cb.timeout = timeout
	}
}

func WithMaxRequests(maxRequests uint32) Option {
	return func(cb *CircuitBreaker) {
		cb.maxRequests = maxRequests
	}
}

func WithReadyToTrip(readyToTrip func(counts Counts) bool) Option {
	return func(cb *CircuitBreaker) {
		cb.readyToTrip = readyToTrip
	}
}

func WithTimeProvider(timeProvider TimeProvider) Option {
	return func(cb *CircuitBreaker) {
		cb.timeProvider = timeProvider
	}
}

func NewCircuitBreaker(options ...Option) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:       StateClosed,
		maxRequests: 5,
		timeout:     10 * time.Second,
		readyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
		counts:       Counts{},
		timeProvider: &RealTimeTimeProvider{},
	}

	for _, opt := range options {
		opt(cb)
	}

	return cb
}

type (
	Request func() (interface{}, error)

	CircuitBreaker struct {
		// Максимальное кол-во запросов которые может пропустить через себя Circuit Breaker
		// пока находится в состоянии Half-Open.
		maxRequests uint32
		// Период нахождения Circuit Breaker в состоянии Open до перехода в Half-Open
		timeout time.Duration
		// Стратегия перехода из состояния Closed в Open.
		// Например, если было больше 5 ошибок подряд:
		// func defaultReadyToTrip(counts Counts) bool {
		//   return counts.ConsecutiveFailures > 5
		// }
		readyToTrip func(counts Counts) bool

		state        State
		counts       Counts
		expiry       time.Time
		timeProvider TimeProvider
	}
)

func (cb *CircuitBreaker) onSuccess() {
	switch cb.state {
	case StateClosed:
		cb.counts.onSuccess()
	case StateHalfOpen:
		cb.counts.onSuccess()
		if cb.counts.ConsecutiveSuccesses >= cb.maxRequests {
			cb.state = StateClosed
			cb.counts.clear()
			cb.expiry = time.Time{}
		}
	}
}

func (cb *CircuitBreaker) onFailure() {
	switch cb.state {
	case StateClosed:
		cb.counts.onFailure()
		if cb.readyToTrip(cb.counts) {
			cb.expiry = cb.timeProvider.Now().Add(cb.timeout)
			cb.state = StateOpen
			cb.counts.clear()
		}
	case StateHalfOpen:
		cb.expiry = cb.timeProvider.Now().Add(cb.timeout)
		cb.state = StateOpen
		cb.counts.clear()
	}
}

func (cb *CircuitBreaker) Execute(req Request) (interface{}, error) {
	if cb.state == StateOpen && cb.expiry.Before(cb.timeProvider.Now()) {
		cb.state = StateHalfOpen
		cb.expiry = time.Time{}
		cb.counts.clear()
	}

	if cb.state == StateOpen {
		return nil, ErrOpenState
	}
	if cb.state == StateHalfOpen && cb.counts.Requests >= cb.maxRequests {
		return nil, ErrTooManyRequests
	}

	cb.counts.onRequest()

	response, err := req()

	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}

	return response, err
}
