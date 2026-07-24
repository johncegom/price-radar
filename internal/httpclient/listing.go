package httpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// FetchListing GETs the given listing URL as-is and returns the raw
// response body.
//
// It never constructs or appends query parameters to url: FPT Shop's
// robots.txt disallows the filtered/query-parameter variants of the
// listing (e.g. "?hang-san-xuat=", "?ram="), so callers must pass the
// clean listing URL (from config) and FetchListing fetches exactly that
// URL, unmodified.
func FetchListing(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpclient: building request: %w", err)
	}

	client := New()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpclient: fetching listing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("httpclient: unexpected status %d fetching listing", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpclient: reading listing body: %w", err)
	}

	return body, nil
}
