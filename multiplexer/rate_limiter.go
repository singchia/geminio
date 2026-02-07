package multiplexer

import (
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter
// It limits the number of packets per second (PPS) that can be sent
type RateLimiter struct {
	mu sync.Mutex

	// tokens: current available tokens
	// capacity: maximum tokens (burst size)
	// refillRate: tokens added per second
	tokens     float64
	capacity   float64
	refillRate float64
	lastRefill time.Time
}

// NewRateLimiter creates a new rate limiter
// pps: packets per second (rate limit)
// burst: maximum burst size (capacity)
func NewRateLimiter(pps int, burst int) *RateLimiter {
	if pps <= 0 {
		pps = 1000 // default: 1000 pps
	}
	if burst <= 0 {
		burst = pps * 2 // default: 2x pps for burst
	}
	return &RateLimiter{
		tokens:     float64(burst),
		capacity:   float64(burst),
		refillRate: float64(pps),
		lastRefill: time.Now(),
	}
}

// Allow checks if a packet can be sent (consumes one token)
// Returns true if allowed, false if rate limited
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()

	// Refill tokens based on elapsed time
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.capacity {
		rl.tokens = rl.capacity
	}
	rl.lastRefill = now

	// Check if we have enough tokens
	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		return true
	}

	// Not enough tokens, rate limited
	return false
}

// Reset resets the rate limiter to initial state
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.tokens = rl.capacity
	rl.lastRefill = time.Now()
}
