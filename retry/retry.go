// Package retry defines the retry/backoff configuration shared by every
// muninn provider (boamp, decp, beauamp) for requests against their external,
// potentially rate-limited government APIs. The actual retry logic lives in
// internal/httpx; this package only exposes the configuration type so
// external consumers can construct and pass one via each provider's
// WithRetryPolicy option.
package retry

import "time"

// Policy configures how a provider retries a request that received a
// 429/5xx response or a transient network error. A zero-value Policy
// behaves like DefaultRetryPolicy.
type Policy struct {
	// MaxAttempts is the total number of attempts, including the first
	// (non-retry) one. <= 0 falls back to DefaultRetryPolicy.MaxAttempts.
	MaxAttempts int
	// BaseDelay is the backoff base for exponential growth. <= 0 falls back to
	// DefaultRetryPolicy.BaseDelay.
	BaseDelay time.Duration
	// MaxDelay caps the computed backoff (before a server Retry-After header
	// overrides it). <= 0 falls back to DefaultRetryPolicy.MaxDelay.
	MaxDelay time.Duration
}

// DefaultRetryPolicy is a conservative default for public government APIs: up
// to 3 retries (4 attempts total), starting at 300ms, capped at 5s, full
// jitter.
var DefaultRetryPolicy = Policy{
	MaxAttempts: 4,
	BaseDelay:   300 * time.Millisecond,
	MaxDelay:    5 * time.Second,
}
