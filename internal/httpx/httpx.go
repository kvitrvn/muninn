// Package httpx provides a shared HTTP retry helper for the providers that
// hit external, potentially rate-limited government APIs (Opendatasoft,
// data.gouv.fr). It retries transient failures (429/5xx) with exponential
// backoff and jitter, honoring a numeric Retry-After header when present.
package httpx

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/kvitrvn/muninn/retry"
)

// RetryPolicy is an alias for retry.Policy: the configuration type lives
// in the public retry package so external consumers can construct one (e.g.
// for a provider's WithRetryPolicy option), while the retry logic below
// (backoff, jitter, Retry-After handling) stays an internal implementation
// detail.
type RetryPolicy = retry.Policy

// DefaultRetryPolicy re-exports retry.DefaultRetryPolicy for convenience
// within this package.
var DefaultRetryPolicy = retry.DefaultRetryPolicy

// withDefaults fills any unset (<=0) field of p from DefaultRetryPolicy, so a
// zero-value RetryPolicy{} and a partially-specified one both behave
// sensibly. It's a plain function rather than a RetryPolicy method since
// RetryPolicy is now an alias for a type defined in another package, and Go
// does not allow attaching methods to a type from outside its own package.
func withDefaults(p RetryPolicy) RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = DefaultRetryPolicy.MaxAttempts
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = DefaultRetryPolicy.BaseDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = DefaultRetryPolicy.MaxDelay
	}
	return p
}

// Do executes an HTTP request built by newReq, retrying on 429/5xx responses
// and transient network errors according to policy. newReq is called once per
// attempt (never reusing a *http.Request across attempts, since a consumed
// request body cannot be safely resent) and must build its request against
// the ctx it is given.
//
// Retries honor a numeric Retry-After header when the server sends one,
// otherwise back off exponentially with full jitter, capped at
// policy.MaxDelay. A ctx cancellation or deadline is never retried and
// aborts immediately. When every attempt is exhausted, the last response (if
// any) or error is returned as-is, so the caller's usual status/decode
// handling applies unchanged.
func Do(ctx context.Context, client *http.Client, policy RetryPolicy, newReq func(context.Context) (*http.Request, error)) (*http.Response, error) {
	policy = withDefaults(policy)

	var lastErr error
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		req, err := newReq(ctx)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		switch {
		case err != nil:
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			lastErr = err
			resp = nil
		case !retryableStatus(resp.StatusCode):
			return resp, nil
		default:
			lastErr = nil
		}

		last := attempt == policy.MaxAttempts-1
		if last {
			if resp != nil {
				return resp, nil
			}
			return nil, lastErr
		}

		delay := retryDelay(policy, attempt, resp)
		if resp != nil {
			resp.Body.Close()
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr // unreachable: MaxAttempts >= 1 after withDefaults.
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// retryDelay honors a numeric Retry-After header when present (the HTTP-date
// form is not handled, matching what these APIs are observed to send),
// otherwise computes exponential backoff with full jitter capped at
// policy.MaxDelay.
func retryDelay(policy RetryPolicy, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}

	// Bound the shift so BaseDelay<<attempt can't overflow into a negative or
	// wrapped duration for a large attempt count; anything past a handful of
	// doublings already exceeds any sane MaxDelay, so it just saturates.
	const maxShift = 20
	shift := attempt
	if shift > maxShift {
		shift = maxShift
	}
	backoff := policy.BaseDelay << uint(shift)
	if backoff <= 0 || backoff > policy.MaxDelay {
		backoff = policy.MaxDelay
	}
	return time.Duration(rand.Int63n(int64(backoff) + 1))
}
