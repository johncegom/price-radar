//go:build chrome

// These tests drive a real headless Chrome via chromedp and are gated behind
// the "chrome" build tag so the default `go test ./...` (and any environment
// without a headless Chrome) stays green. Run them with:
//
//	go test -tags chrome ./internal/browser
//
// Every test runs against a local httptest.Server fixture — never the real
// fptshop.com.vn site (compliance) and never real AWS (S3 uploads go to a local
// httptest.Server). They additionally skip under -short.
package browser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// incrementalListingHTML serves a self-contained page whose "Xem thêm" button
// appends a batch of cards on each click and removes itself once `batches`
// clicks have happened — simulating the real listing's client-side "Load more".
func incrementalListingHTML(batches, perBatch int) string {
	return `<!doctype html><html><head><meta charset="utf-8"></head><body>
<h1>May doi tra</h1>
<div id="cards"><a href="/p/1">Card 1</a></div>
<button id="more" class="tw-random-abc123">Xem thêm</button>
<script>
var batches = ` + itoa(batches) + `;
var perBatch = ` + itoa(perBatch) + `;
var clicked = 0;
var next = 2;
document.getElementById('more').addEventListener('click', function () {
  var c = document.getElementById('cards');
  for (var i = 0; i < perBatch; i++) {
    var a = document.createElement('a');
    a.href = '/p/' + next;
    a.textContent = 'Card ' + next;
    c.appendChild(a);
    next++;
  }
  clicked++;
  if (clicked >= batches) { this.remove(); }
});
</script>
</body></html>`
}

// noControlHTML serves a page with cards but no "Xem thêm" control at all,
// simulating a selector miss (e.g. the label changed on a frontend rebuild).
const noControlHTML = `<!doctype html><html><head><meta charset="utf-8"></head><body>
<h1>May doi tra</h1>
<div id="cards"><a href="/p/1">Card 1</a><a href="/p/2">Card 2</a></div>
<button class="tw-xyz">Tai them san pham</button>
</body></html>`

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func fixtureServer(t *testing.T, html string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
}

// T4.2 — page load: navigating to the clean listing URL loads the page.
func TestChrome_PageLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Chrome-gated test in -short mode")
	}
	srv := fixtureServer(t, incrementalListingHTML(2, 3))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	html, err := FetchFullCatalog(ctx, srv.URL)
	if err != nil {
		t.Fatalf("FetchFullCatalog: %v", err)
	}
	if !strings.Contains(html, "May doi tra") {
		t.Errorf("returned HTML missing page marker; got %d bytes", len(html))
	}
}

// T4.3 / T4.5 — the load-more loop clicks through every batch, terminates when
// the control disappears (never infinite), and returns the full card count.
func TestChrome_LoadMoreLoopFullCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Chrome-gated test in -short mode")
	}
	const batches, perBatch = 3, 5
	srv := fixtureServer(t, incrementalListingHTML(batches, perBatch))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	html, err := FetchFullCatalog(ctx, srv.URL)
	if err != nil {
		t.Fatalf("FetchFullCatalog: %v", err)
	}

	// 1 initial card + batches*perBatch appended.
	wantCards := 1 + batches*perBatch
	if got := strings.Count(html, "/p/"); got != wantCards {
		t.Errorf("card count = %d, want %d", got, wantCards)
	}
	// The control must be gone at completion (loop terminated by disappearance).
	if strings.Contains(html, "Xem thêm") {
		t.Error("returned HTML still contains the load-more control; loop did not exhaust it")
	}
}

// T4.3 — the bounded cap prevents an infinite loop when the control never
// disappears. Using a MaxLoadMore below the fixture's batch count forces the
// cap and asserts the hard error (never a silent partial success).
func TestChrome_LoadMoreCapIsSafetyNet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Chrome-gated test in -short mode")
	}
	// A page whose button never removes itself: batches far exceeds the cap.
	srv := fixtureServer(t, incrementalListingHTML(1000, 2))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	f := &Fetcher{MaxLoadMore: 3}
	_, err := f.Fetch(ctx, srv.URL)
	if !errors.Is(err, ErrLoadMoreCapExceeded) {
		t.Fatalf("err = %v, want ErrLoadMoreCapExceeded", err)
	}
}

// T4.4 — selector miss against a no-control fixture: screenshot captured and
// uploaded to a local httptest.Server, structured result populated, and no
// partial list is silently returned as complete.
func TestChrome_SelectorMissUploadsAndReports(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Chrome-gated test in -short mode")
	}
	srv := fixtureServer(t, noControlHTML)
	defer srv.Close()

	var uploadedBytes int
	var uploadedType string
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		n, _ := r.Body.Read(buf)
		uploadedBytes = n
		uploadedType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer s3.Close()

	f := &Fetcher{
		Uploader:         &httpUploader{baseURL: s3.URL, client: s3.Client()},
		ScreenshotPrefix: "screenshots",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	html, err := f.Fetch(ctx, srv.URL)

	if html != "" {
		t.Errorf("html = %q, want empty (no partial list on selector miss)", html)
	}
	var review *NeedsAgentReview
	if !errors.As(err, &review) {
		t.Fatalf("err = %v, want *NeedsAgentReview", err)
	}
	if review.Reason != "primary_selector_not_found" {
		t.Errorf("Reason = %q", review.Reason)
	}
	if review.Step != "load_more_detection" {
		t.Errorf("Step = %q", review.Step)
	}
	if review.UploadErr != nil {
		t.Errorf("UploadErr = %v, want nil (upload should succeed)", review.UploadErr)
	}
	if review.ScreenshotURL == "" {
		t.Error("ScreenshotURL empty, want uploaded screenshot URL")
	}
	if uploadedBytes == 0 {
		t.Error("no screenshot bytes uploaded")
	}
	if uploadedType != "image/png" {
		t.Errorf("uploaded content type = %q, want image/png", uploadedType)
	}
}
