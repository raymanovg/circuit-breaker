package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type TestTimeProvider struct {
	modifiers []func(time time.Time) time.Time
}

func (p *TestTimeProvider) Modify(modifier func(time time.Time) time.Time) {
	p.modifiers = append(p.modifiers, modifier)
}

func (p *TestTimeProvider) Now() time.Time {
	now := time.Now()
	for _, modifier := range p.modifiers {
		now = modifier(now)
	}
	return now
}

func fail(cb *CircuitBreaker) error {
	_, err := cb.Execute(func() (interface{}, error) {
		return nil, errors.New("fail")
	})

	return err
}

func succeed(cb *CircuitBreaker) error {
	_, err := cb.Execute(func() (interface{}, error) {
		return nil, nil
	})

	return err
}

func TestCircuitBreaker_Execute(t *testing.T) {
	timeProvider := &TestTimeProvider{}

	cb := NewCircuitBreaker(
		WithTimeout(5*time.Second),
		WithMaxRequests(5),
		WithReadyToTrip(func(counts Counts) bool {
			return counts.ConsecutiveFailures > 5
		}),
		WithTimeProvider(timeProvider),
	)

	// 5 запусков с ошибкой
	for i := 0; i < 5; i++ {
		assert.NotNil(t, fail(cb))
	}

	// состояние все еще Closed т.к. нужно > 5 ошибок подрят
	assert.Equal(t, StateClosed, cb.state)
	assert.Equal(t, Counts{5, 0, 5, 0, 5}, cb.counts)

	// успешный запуск. должен сбросить ConsecutiveFailures
	assert.Nil(t, succeed(cb))
	assert.Equal(t, StateClosed, cb.state)
	assert.Equal(t, Counts{6, 1, 5, 1, 0}, cb.counts)

	// ошибка. статус все еще Closed т.к. ConsecutiveFailures=1
	assert.NotNil(t, fail(cb))
	assert.Equal(t, StateClosed, cb.state)
	assert.Equal(t, Counts{7, 1, 6, 0, 1}, cb.counts)

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.NotNil(t, fail(cb)) // 6 consecutive failures
	}

	assert.Equal(t, StateOpen, cb.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, cb.counts)
	assert.False(t, cb.expiry.IsZero())

	// в Open запросы не проходят
	assert.Error(t, succeed(cb))
	assert.Error(t, fail(cb))
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, cb.counts)

	// StateOpen to StateHalfOpen
	// over Timeout
	timeProvider.Modify(func(t time.Time) time.Time {
		return t.Add(6 * time.Second)
	})

	assert.Nil(t, succeed(cb))
	assert.Equal(t, StateHalfOpen, cb.state)
	assert.True(t, cb.expiry.IsZero())
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, cb.counts)

	// StateHalfOpen to StateOpen
	assert.NotNil(t, fail(cb))
	assert.Equal(t, StateOpen, cb.state)
	assert.False(t, cb.expiry.IsZero())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, cb.counts)

	// StateOpen to StateHalfOpen
	// over Timeout
	timeProvider.Modify(func(t time.Time) time.Time {
		return t.Add(6 * time.Second)
	})

	assert.Nil(t, succeed(cb))
	assert.Equal(t, StateHalfOpen, cb.state)
	assert.True(t, cb.expiry.IsZero())
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, cb.counts)

	// StateHalfOpen to StateClosed
	// ConsecutiveSuccesses(5) >= MaxRequests(5)
	for i := 0; i < 4; i++ {
		assert.Nil(t, succeed(cb))
	}
	assert.Equal(t, StateClosed, cb.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, cb.counts)
	assert.True(t, cb.expiry.IsZero())
}
