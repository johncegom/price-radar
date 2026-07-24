package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fastClient returns a Client with short backoff timings suitable for
// tests, so retry tests don't take seconds.
func fastClient(maxRetries int) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: &userAgentTransport{base: http.DefaultTransport},
		},
		maxRetries: maxRetries,
		baseDelay:  1 * time.Millisecond,
		maxDelay:   10 * time.Millisecond,
	}
}

func TestUserAgentInjected(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotUA != userAgent {
		t.Errorf("got User-Agent %q, want %q", gotUA, userAgent)
	}
}

func TestUserAgentInjectedOnEveryRequest(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		if r.Header.Get("User-Agent") != userAgent {
			t.Errorf("request %d missing expected User-Agent, got %q", count, r.Header.Get("User-Agent"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New()
	for i := 0; i < 3; i++ {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		resp.Body.Close()
	}
	if count != 3 {
		t.Errorf("got %d requests, want 3", count)
	}
}

func TestDoFirstTrySuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := fastClient(3)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if attempts != 1 {
		t.Errorf("got %d attempts, want 1", attempts)
	}
}

func TestDoSuccessAfterRetries(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := fastClient(5)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if attempts != 3 {
		t.Errorf("got %d attempts, want 3", attempts)
	}
}

func TestDoGivesUpAfterCap(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	maxRetries := 3
	c := fastClient(maxRetries)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}

	wantAttempts := int32(maxRetries + 1)
	if attempts != wantAttempts {
		t.Errorf("got %d attempts, want %d", attempts, wantAttempts)
	}
}

func TestDoNonRetryableStatusShortCircuits(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := fastClient(5)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got status %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	if attempts != 1 {
		t.Errorf("got %d attempts, want 1 (non-retryable status must short-circuit)", attempts)
	}
}

func TestDoContextCancellationAbortsMidRetry(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := &Client{
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: &userAgentTransport{base: http.DefaultTransport},
		},
		maxRetries: 20,
		baseDelay:  50 * time.Millisecond,
		maxDelay:   200 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	start := time.Now()
	_, err := c.Do(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from context cancellation, got nil")
	}
	// Should abort well before all 20 retries would complete (which would
	// take well over a second at these delays).
	if elapsed > 1*time.Second {
		t.Errorf("Do took %v, expected to abort quickly on context cancellation", elapsed)
	}
	if attempts < 1 {
		t.Errorf("expected at least 1 attempt before cancellation, got %d", attempts)
	}
}
