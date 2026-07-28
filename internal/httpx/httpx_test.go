package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
}

func newReq(url string) func(context.Context) (*http.Request, error) {
	return func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	}
}

func TestDo_RetriesOnServerErrorThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), http.DefaultClient, testPolicy(), newReq(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

func TestDo_RetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), http.DefaultClient, testPolicy(), newReq(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestDo_HonorsRetryAfterHeader(t *testing.T) {
	var calls int32
	var firstCallAt, secondCallAt time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			firstCallAt = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		secondCallAt = time.Now()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// MaxDelay is tiny, but Retry-After (1s) must still be honored, so the
	// second call should land ~1s after the first.
	resp, err := Do(context.Background(), http.DefaultClient, testPolicy(), newReq(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if gap := secondCallAt.Sub(firstCallAt); gap < 900*time.Millisecond {
		t.Errorf("gap between calls = %v, want >= ~1s (Retry-After ignored?)", gap)
	}
}

func TestDo_ExhaustsRetriesAndReturnsLastResponse(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	policy := testPolicy()
	resp, err := Do(context.Background(), http.DefaultClient, policy, newReq(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", resp.StatusCode)
	}
	if int(calls) != policy.MaxAttempts {
		t.Errorf("calls = %d, want %d", calls, policy.MaxAttempts)
	}
}

func TestDo_DoesNotRetryOnNonRetryableStatus(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), http.DefaultClient, testPolicy(), newReq(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (404 must not retry)", calls)
	}
}

func TestDo_StopsImmediatelyOnCanceledContext(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Do(ctx, http.DefaultClient, testPolicy(), newReq(srv.URL))
	if err == nil {
		t.Fatal("Do: want error for canceled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestDo_ZeroValuePolicyUsesDefaults(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), http.DefaultClient, RetryPolicy{}, newReq(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (zero-value policy should still retry using defaults)", calls)
	}
}

func TestRetryDelay_RetryAfterOverridesBackoff(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Retry-After": {"2"}}}
	got := retryDelay(testPolicy(), 0, resp)
	if got != 2*time.Second {
		t.Errorf("retryDelay = %v, want 2s", got)
	}
}

func TestRetryDelay_LargeAttemptDoesNotOverflow(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 100, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	got := retryDelay(policy, 63, nil) // shift of 63 would overflow int64 unguarded.
	if got < 0 || got > policy.MaxDelay {
		t.Errorf("retryDelay = %v, want in [0, %v]", got, policy.MaxDelay)
	}
}
