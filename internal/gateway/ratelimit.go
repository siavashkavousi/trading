package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
)

type TokenBucket struct {
	mu          sync.Mutex
	tokens      float64
	capacity    float64
	refillRate  float64
	lastRefill  time.Time
}

func NewTokenBucket(capacity, refillPerSecond int) *TokenBucket {
	return &TokenBucket{
		tokens:     float64(capacity),
		capacity:   float64(capacity),
		refillRate: float64(refillPerSecond),
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.lastRefill = now
}

func (tb *TokenBucket) TryAcquire(weight int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	w := float64(weight)
	if tb.tokens >= w {
		tb.tokens -= w
		return true
	}
	return false
}

func (tb *TokenBucket) Acquire(ctx context.Context, weight int) error {
	for {
		if tb.TryAcquire(weight) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type RateLimiter struct {
	mu      sync.RWMutex
	buckets map[domain.EndpointCategory]*TokenBucket
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[domain.EndpointCategory]*TokenBucket),
	}
}

func (rl *RateLimiter) AddBucket(category domain.EndpointCategory, capacity, refillPerSecond int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.buckets[category] = NewTokenBucket(capacity, refillPerSecond)
}

func (rl *RateLimiter) Acquire(ctx context.Context, category domain.EndpointCategory, weight int) error {
	rl.mu.RLock()
	bucket, ok := rl.buckets[category]
	rl.mu.RUnlock()
	if !ok {
		return nil
	}
	return bucket.Acquire(ctx, weight)
}

func (rl *RateLimiter) TryAcquire(category domain.EndpointCategory, weight int) bool {
	rl.mu.RLock()
	bucket, ok := rl.buckets[category]
	rl.mu.RUnlock()
	if !ok {
		return true
	}
	return bucket.TryAcquire(weight)
}
