package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fixtureHTML = `<!DOCTYPE html>
<html>
<body>
<div class="product-card">MacBook Pro M2 Pro 32GB/512GB</div>
</body>
</html>`

func TestFetchListing(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fixtureHTML))
	}))
	defer srv.Close()

	body, err := FetchListing(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchListing: %v", err)
	}

	if !strings.Contains(string(body), "MacBook Pro M2 Pro") {
		t.Errorf("body missing expected fixture content: %s", body)
	}

	// FetchListing must never append query params: the server should see
	// no query string on the request it received.
	if gotQuery != "" {
		t.Errorf("got query %q, want empty (FetchListing must not construct query params)", gotQuery)
	}
	_ = gotPath
}

func TestFetchListingNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchListing(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
}
