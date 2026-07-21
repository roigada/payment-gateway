package httpapi

import (
	"errors"
	"math"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type RouteClass string

const (
	RouteClassRead  RouteClass = "read"
	RouteClassWrite RouteClass = "write"
)

type RateLimitConfig struct {
	ReadRequestsPerSecond  int
	ReadBurst              int
	WriteRequestsPerSecond int
	WriteBurst             int
}

func (c RateLimitConfig) validate() error {
	if c.ReadRequestsPerSecond <= 0 || c.ReadBurst <= 0 || c.WriteRequestsPerSecond <= 0 || c.WriteBurst <= 0 {
		return errors.New("rate limit settings must be positive")
	}
	return nil
}

type RateLimiter struct {
	clock   Clock
	config  RateLimitConfig
	mu      sync.Mutex
	buckets map[rateLimitKey]tokenBucket
}

type rateLimitKey struct {
	principalID string
	routeClass  RouteClass
}

type tokenBucket struct {
	tokens  float64
	updated time.Time
}

func NewRateLimiter(clock Clock, config RateLimitConfig) (*RateLimiter, error) {
	if clock == nil {
		return nil, errors.New("rate limit clock is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &RateLimiter{clock: clock, config: config, buckets: make(map[rateLimitKey]tokenBucket)}, nil
}

// Reserve returns whether the request acquired a token and, if not, the
// whole-second delay until the next token can be acquired.
func (l *RateLimiter) Reserve(principalID string, routeClass RouteClass) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rate, burst := l.limitFor(routeClass)
	now := l.clock.Now()
	key := rateLimitKey{principalID: principalID, routeClass: routeClass}
	bucket, exists := l.buckets[key]
	if !exists {
		bucket = tokenBucket{tokens: float64(burst), updated: now}
	} else if elapsed := now.Sub(bucket.updated); elapsed > 0 {
		bucket.tokens = math.Min(float64(burst), bucket.tokens+elapsed.Seconds()*float64(rate))
		bucket.updated = now
	}

	if bucket.tokens >= 1 {
		bucket.tokens--
		l.buckets[key] = bucket
		return true, 0
	}

	delay := int(math.Ceil((1 - bucket.tokens) / float64(rate)))
	if delay < 1 {
		delay = 1
	}
	// A rejection must not alter stored capacity: callers can retry exactly when
	// Retry-After says a token will be available.
	l.buckets[key] = bucket
	return false, delay
}

func (l *RateLimiter) limitFor(routeClass RouteClass) (rate, burst int) {
	if routeClass == RouteClassRead {
		return l.config.ReadRequestsPerSecond, l.config.ReadBurst
	}
	return l.config.WriteRequestsPerSecond, l.config.WriteBurst
}
