package httpapi

import (
	"errors"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
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
	buckets map[rateLimitKey]*rate.Limiter
}

type rateLimitKey struct {
	principalID string
	routeClass  RouteClass
}

func NewRateLimiter(clock Clock, config RateLimitConfig) (*RateLimiter, error) {
	if clock == nil {
		return nil, errors.New("rate limit clock is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &RateLimiter{clock: clock, config: config, buckets: make(map[rateLimitKey]*rate.Limiter)}, nil
}

// Reserve returns whether the request acquired a token and, if not, the
// whole-second delay until the next token can be acquired.
func (l *RateLimiter) Reserve(principalID string, routeClass RouteClass) (bool, int) {
	l.mu.Lock()
	key := rateLimitKey{principalID: principalID, routeClass: routeClass}
	limiter, exists := l.buckets[key]
	if !exists {
		requestsPerSecond, burst := l.limitFor(routeClass)
		limiter = rate.NewLimiter(rate.Limit(requestsPerSecond), burst)
		l.buckets[key] = limiter
	}
	l.mu.Unlock()

	now := l.clock.Now()
	if limiter.AllowN(now, 1) {
		return true, 0
	}

	reservation := limiter.ReserveN(now, 1)
	delay := int(math.Ceil(reservation.DelayFrom(now).Seconds()))
	if delay < 1 {
		delay = 1
	}
	// Calculating Retry-After must not reserve future capacity: callers can retry
	// exactly when the header says a token will be available.
	reservation.CancelAt(now)
	return false, delay
}

func (l *RateLimiter) limitFor(routeClass RouteClass) (rate, burst int) {
	if routeClass == RouteClassRead {
		return l.config.ReadRequestsPerSecond, l.config.ReadBurst
	}
	return l.config.WriteRequestsPerSecond, l.config.WriteBurst
}
