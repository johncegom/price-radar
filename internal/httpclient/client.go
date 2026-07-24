package httpclient

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// userAgent is a realistic desktop browser User-Agent string sent on every
// outbound request. FPT Shop's robots.txt asks for infrequent, well-behaved
// requests; presenting a plausible browser UA is part of that.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// userAgentTransport is an http.RoundTripper that wraps another transport
// (normally http.DefaultTransport) and injects a realistic desktop
// User-Agent header on every request.
type userAgentTransport struct {
	base http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone so we never mutate the caller's request.
	cloned := req.Clone(req.Context())
	cloned.Header.Set("User-Agent", userAgent)
	return t.base.RoundTrip(cloned)
}

// Default retry/backoff parameters.
const (
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 4
	defaultBaseDelay  = 200 * time.Millisecond
	defaultMaxDelay   = 5 * time.Second
)

// Client wraps *http.Client with a User-Agent-injecting transport and an
// exponential backoff retry loop for transient network errors and
// retryable HTTP status codes (429, 5xx).
type Client struct {
	httpClient *http.Client
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// New returns a Client configured with an explicit request timeout and a
// User-Agent-injecting RoundTripper wrapping http.DefaultTransport.
func New() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout:   defaultTimeout,
			Transport: &userAgentTransport{base: http.DefaultTransport},
		},
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
		maxDelay:   defaultMaxDelay,
	}
}

// isRetryableStatus reports whether an HTTP response status code should be
// retried: 429 (rate-limited) or any 5xx server error.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

// backoffDelay returns the exponential backoff delay for the given attempt
// (0-indexed), capped at maxDelay, with a small jitter to avoid thundering
// herd against the target site.
func backoffDelay(attempt int, base, cap time.Duration) time.Duration {
	d := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
	if d > cap || d <= 0 {
		d = cap
	}
	// Add up to 20% jitter.
	jitter := time.Duration(rand.Int63n(int64(d)/5 + 1))
	return d + jitter
}

// Do executes req with exponential backoff retry on network errors and
// retryable status codes (429, 5xx). Non-retryable status codes (e.g. 4xx
// other than 429) and non-retryable errors are returned immediately. The
// request's context bounds the total time spent across all attempts;
// context cancellation aborts retrying immediately.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := req.Context().Err(); err != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("httpclient: context done after %d attempt(s), last error: %w", attempt, lastErr)
			}
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
		} else if isRetryableStatus(resp.StatusCode) {
			resp.Body.Close()
			lastErr = fmt.Errorf("httpclient: retryable status %d", resp.StatusCode)
		} else {
			// Success or non-retryable status: return immediately.
			return resp, nil
		}

		if attempt == c.maxRetries {
			break
		}

		delay := backoffDelay(attempt, c.baseDelay, c.maxDelay)
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, fmt.Errorf("httpclient: context done during backoff, last error: %w", lastErr)
		case <-timer.C:
		}
	}

	return nil, fmt.Errorf("httpclient: giving up after %d attempts: %w", c.maxRetries+1, lastErr)
}
