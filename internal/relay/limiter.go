package relay

import (
	"sync"
	"time"
)

type tokenBucket struct {
	tokens    float64
	updatedAt time.Time
}

type TokenBucketLimiter struct {
	mutex      sync.Mutex
	buckets    map[string]tokenBucket
	capacity   float64
	refillRate float64
	clock      func() time.Time
}

func NewTokenBucketLimiter(capacity int, refillPeriod time.Duration, clock func() time.Time) *TokenBucketLimiter {
	if capacity < 1 {
		capacity = 1
	}
	if refillPeriod <= 0 {
		refillPeriod = time.Minute
	}
	if clock == nil {
		clock = time.Now
	}
	return &TokenBucketLimiter{
		buckets:    make(map[string]tokenBucket),
		capacity:   float64(capacity),
		refillRate: float64(capacity) / refillPeriod.Seconds(),
		clock:      clock,
	}
}

func (limiter *TokenBucketLimiter) Allow(key string) bool {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	now := limiter.clock()
	bucket, exists := limiter.buckets[key]
	if !exists {
		bucket = tokenBucket{tokens: limiter.capacity, updatedAt: now}
	}
	elapsed := now.Sub(bucket.updatedAt).Seconds()
	if elapsed > 0 {
		bucket.tokens = min(limiter.capacity, bucket.tokens+elapsed*limiter.refillRate)
		bucket.updatedAt = now
	}
	if bucket.tokens < 1 {
		limiter.buckets[key] = bucket
		return false
	}
	bucket.tokens--
	limiter.buckets[key] = bucket
	return true
}
