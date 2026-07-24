package browser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockUploader records the last upload and can simulate an upload failure. It
// stands in for the S3-backed uploader so selector-miss handling can be tested
// without live browser or AWS access.
type mockUploader struct {
	called      bool
	gotKey      string
	gotData     []byte
	gotType     string
	returnURL   string
	returnError error
}

func (m *mockUploader) Upload(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	m.called = true
	m.gotKey = key
	m.gotData = data
	m.gotType = contentType
	return m.returnURL, m.returnError
}

// httpUploader uploads screenshot bytes to a local httptest.Server, exercising
// the "S3 calls against a local mock/httptest.Server" path required by T4.4
// without contacting real AWS.
type httpUploader struct {
	baseURL string
	client  *http.Client
}

func (u *httpUploader) Upload(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	dest := u.baseURL + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, dest, strings.NewReader(string(data)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("upload: unexpected status " + resp.Status)
	}
	return dest, nil
}

// TestUploadScreenshot_Success verifies a selector-miss screenshot is uploaded
// against a local httptest server and the resulting URL is recorded on the
// structured result — no live browser needed.
func TestUploadScreenshot_Success(t *testing.T) {
	var gotBody []byte
	var gotType, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("upload method = %s, want PUT", r.Method)
		}
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := &Fetcher{
		Uploader:         &httpUploader{baseURL: srv.URL, client: srv.Client()},
		ScreenshotPrefix: "screenshots/selector-miss",
	}
	res := &NeedsAgentReview{Step: "load_more_detection", Reason: "primary_selector_not_found"}
	shot := []byte("PNGDATA")

	f.uploadScreenshot(context.Background(), res, shot)

	if res.UploadErr != nil {
		t.Fatalf("UploadErr = %v, want nil", res.UploadErr)
	}
	if res.ScreenshotURL == "" {
		t.Fatal("ScreenshotURL is empty, want the uploaded object URL")
	}
	if !strings.HasPrefix(gotPath, "/screenshots/selector-miss/") {
		t.Errorf("upload path = %q, want prefix /screenshots/selector-miss/", gotPath)
	}
	if !strings.HasSuffix(gotPath, ".png") {
		t.Errorf("upload path = %q, want .png suffix", gotPath)
	}
	if gotType != "image/png" {
		t.Errorf("content type = %q, want image/png", gotType)
	}
	if string(gotBody) != "PNGDATA" {
		t.Errorf("uploaded body = %q, want PNGDATA", gotBody)
	}
}

// TestUploadScreenshot_UploadFailureDoesNotEscalate verifies the edge case that
// a failing screenshot upload is recorded on the result but never replaces the
// selector-miss outcome nor turns it into a hard error.
func TestUploadScreenshot_UploadFailureDoesNotEscalate(t *testing.T) {
	up := &mockUploader{returnError: errors.New("s3 unavailable")}
	f := &Fetcher{Uploader: up, ScreenshotPrefix: "shots"}
	res := &NeedsAgentReview{Step: "load_more_detection", Reason: "primary_selector_not_found"}

	f.uploadScreenshot(context.Background(), res, []byte("x"))

	if !up.called {
		t.Fatal("uploader was not called")
	}
	if res.ScreenshotURL != "" {
		t.Errorf("ScreenshotURL = %q, want empty on upload failure", res.ScreenshotURL)
	}
	if res.UploadErr == nil {
		t.Fatal("UploadErr = nil, want the upload failure recorded")
	}
	// The primary outcome must remain the selector miss.
	if res.Reason != "primary_selector_not_found" {
		t.Errorf("Reason = %q, want primary_selector_not_found (must not be masked)", res.Reason)
	}
}

// TestUploadScreenshot_NoUploaderSkips verifies that with no Uploader
// configured (on-demand local run, no S3), the upload is skipped cleanly and
// the review is still surfaced with an empty ScreenshotURL and no UploadErr.
func TestUploadScreenshot_NoUploaderSkips(t *testing.T) {
	f := &Fetcher{}
	res := &NeedsAgentReview{Step: "load_more_detection", Reason: "primary_selector_not_found"}

	f.uploadScreenshot(context.Background(), res, []byte("x"))

	if res.ScreenshotURL != "" {
		t.Errorf("ScreenshotURL = %q, want empty when no uploader configured", res.ScreenshotURL)
	}
	if res.UploadErr != nil {
		t.Errorf("UploadErr = %v, want nil when upload is deliberately skipped", res.UploadErr)
	}
}

// TestNeedsAgentReview_ErrorMessage checks the error string surfaces the step,
// reason, and (when present) the upload failure.
func TestNeedsAgentReview_ErrorMessage(t *testing.T) {
	e := &NeedsAgentReview{Step: "load_more_detection", Reason: "primary_selector_not_found"}
	if got := e.Error(); !strings.Contains(got, "load_more_detection") || !strings.Contains(got, "primary_selector_not_found") {
		t.Errorf("Error() = %q, want step and reason present", got)
	}

	e2 := &NeedsAgentReview{Step: "load_more_detection", Reason: "primary_selector_not_found", UploadErr: errors.New("boom")}
	if got := e2.Error(); !strings.Contains(got, "boom") {
		t.Errorf("Error() = %q, want upload failure surfaced", got)
	}
}

// TestNeedsAgentReview_IsError confirms *NeedsAgentReview satisfies error and is
// distinguishable via errors.As, so the handler can map it to a non-error
// Lambda response instead of a hard error.
func TestNeedsAgentReview_IsError(t *testing.T) {
	var err error = &NeedsAgentReview{Step: "load_more_detection", Reason: "primary_selector_not_found"}
	var target *NeedsAgentReview
	if !errors.As(err, &target) {
		t.Fatal("errors.As failed to extract *NeedsAgentReview")
	}
	if target.Reason != "primary_selector_not_found" {
		t.Errorf("Reason = %q", target.Reason)
	}
}
