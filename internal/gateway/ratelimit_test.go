package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
)

func TestTokenBucket_TryAcquire(t *testing.T) {
	tb := NewTokenBucket(5, 10)

	for i := 0; i < 5; i++ {
		if !tb.TryAcquire(1) {
			t.Errorf("expected to acquire token %d", i)
		}
	}

	if tb.TryAcquire(1) {
		t.Error("expected bucket to be exhausted")
	}

	time.Sleep(110 * time.Millisecond)

	if !tb.TryAcquire(1) {
		t.Error("expected bucket to have refilled")
	}
}

func TestRateLimiter_Acquire(t *testing.T) {
	rl := NewRateLimiter()
	rl.AddBucket(domain.EndpointOrderPlace, 2, 100)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := rl.Acquire(ctx, domain.EndpointOrderPlace, 1); err != nil {
		t.Errorf("expected first acquire to succeed: %v", err)
	}

	if err := rl.Acquire(ctx, domain.EndpointOrderPlace, 1); err != nil {
		t.Errorf("expected second acquire to succeed: %v", err)
	}
}

func TestRateLimiter_UnknownCategory(t *testing.T) {
	rl := NewRateLimiter()

	if !rl.TryAcquire(domain.EndpointAccount, 1) {
		t.Error("unknown category should always succeed")
	}
}
