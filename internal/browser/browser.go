package browser

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

// loadMoreText is the visible label of the FPT Shop "Load more" control. The
// control is matched by this text (ADR-009), never by its Tailwind utility
// classes, which regenerate on every frontend rebuild.
const loadMoreText = "Xem thêm"

// loadMoreXPath matches the innermost element whose own text node contains the
// "Load more" label. Using text() (not contains(., ...)) avoids matching every
// ancestor up to <body>, which all "contain" the label transitively.
const loadMoreXPath = `//*[contains(text(), "` + loadMoreText + `")]`

// defaultMaxLoadMore bounds the click loop as a safety net against an infinite
// loop (e.g. a control that never disappears). The real catalog needs ~13
// batches, so this leaves generous headroom while still terminating.
const defaultMaxLoadMore = 25

// clickSettleTimeout bounds how long we wait for new cards to render after a
// single "Load more" click before giving up on that click.
const clickSettleTimeout = 8 * time.Second

// cardCountExpr counts loaded product cards. It is used only to detect that a
// click actually rendered new content (loop synchronization); it is not the
// load-bearing control selector, so a broad, class-agnostic count of anchors is
// sufficient and monotonically increases as batches load.
const cardCountExpr = `document.querySelectorAll('a').length`

// ErrLoadMoreCapExceeded is returned when the click loop hits its bounded
// iteration cap while the control is still present. This is a hard error, not a
// selector miss and not a silently-truncated success: the catalog may be
// genuinely larger than expected or the control may be stuck, and either way we
// must not report a partial list as complete.
var ErrLoadMoreCapExceeded = errors.New("browser: load-more click cap exceeded before control disappeared")

// Uploader stores a selector-miss screenshot and returns the URL at which it
// can later be reviewed (an S3 object URL in production). It is injected so
// tests can supply a mock/httptest-backed uploader and so on-demand local runs
// with no S3 configured can leave it nil (upload skipped, review still
// surfaced). It is deliberately a minimal put-blob interface, not the full AWS
// SDK surface, keeping the AWS dependency out of this package.
type Uploader interface {
	// Upload stores data under key and returns the URL of the stored object.
	Upload(ctx context.Context, key string, data []byte, contentType string) (string, error)
}

// NeedsAgentReview is a typed error returned when the "Load more" control
// cannot be located by its visible-text selector. It is an error so it
// propagates through FetchFullCatalog's (string, error) return, but it
// represents "a human/agent needs to look at this," not a transient failure —
// the handler maps it to a non-error Lambda response (status
// "needs_agent_review") so it does not trigger Lambda's automatic retry storm
// (ADR-009). Retrying a broken selector immediately cannot fix it; the fix is a
// code change + redeploy.
type NeedsAgentReview struct {
	// Step is the pipeline step that needs review. Always "load_more_detection".
	Step string
	// Reason is a machine-readable cause, e.g. "primary_selector_not_found".
	Reason string
	// ScreenshotURL is the URL of the uploaded full-page screenshot, or empty
	// if the upload was skipped (no Uploader configured) or itself failed.
	ScreenshotURL string
	// DOMCandidates are visible texts of nearby clickable elements, offered to
	// whoever fixes the selector as likely replacements for the missing control.
	DOMCandidates []string
	// UploadErr, if non-nil, records that capturing/uploading the screenshot
	// failed. This is attached, not returned in its place: a failed upload must
	// never mask the underlying selector miss nor escalate it into a hard error.
	UploadErr error
}

func (e *NeedsAgentReview) Error() string {
	msg := fmt.Sprintf("browser: needs agent review at step %q: %s", e.Step, e.Reason)
	if e.UploadErr != nil {
		msg += fmt.Sprintf(" (screenshot upload failed: %v)", e.UploadErr)
	}
	return msg
}

// Fetcher drives the headless browser to load the full catalog. Callers that
// need selector-miss screenshots persisted (the Lambda handler) construct a
// Fetcher with an Uploader and the screenshot bucket/prefix wired in;
// FetchFullCatalog is the zero-config convenience entry point for callers that
// do not (on-demand local runs without S3).
type Fetcher struct {
	// Uploader receives selector-miss screenshots. If nil, the screenshot is
	// skipped and the NeedsAgentReview result is still surfaced (with an empty
	// ScreenshotURL) — mirroring the config-presence gating used by store/notify.
	Uploader Uploader
	// ScreenshotBucket is unused for object addressing here (the Uploader owns
	// the bucket) but is carried for parity with config and future use.
	ScreenshotBucket string
	// ScreenshotPrefix is prepended to the generated screenshot object key.
	ScreenshotPrefix string
	// MaxLoadMore overrides the click-loop iteration cap. Zero uses the default.
	MaxLoadMore int
}

// FetchFullCatalog navigates a fresh headless browser context to url, drives
// the page's own "Load more" control until the full catalog is loaded, and
// returns the resulting page HTML in the same shape internal/parser consumes.
//
// url must be the clean listing URL only — never a filtered/query-param URL,
// /ajax/ path, or the site's internal API. The headless path exists solely to
// drive the page's own control against the same permitted URL.
//
// On a selector miss it returns a *NeedsAgentReview error (without an
// Uploader configured, the screenshot is skipped but the review is still
// surfaced). This is the zero-config entry point; the handler uses a Fetcher
// with an Uploader wired in.
func FetchFullCatalog(ctx context.Context, url string) (string, error) {
	return (&Fetcher{}).Fetch(ctx, url)
}

// Fetch is FetchFullCatalog with this Fetcher's uploader/config applied. It
// owns the chromedp browser context for the run.
func (f *Fetcher) Fetch(ctx context.Context, url string) (html string, err error) {
	taskCtx, cancel := chromedp.NewContext(ctx)
	defer cancel()
	return f.run(taskCtx, url)
}

// run performs the navigate + click-loop + extract sequence against an
// already-established chromedp context. It is split from Fetch so tests can
// drive it with their own context/browser.
func (f *Fetcher) run(ctx context.Context, url string) (string, error) {
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		return "", fmt.Errorf("browser: navigate %q: %w", url, err)
	}

	maxClicks := f.MaxLoadMore
	if maxClicks <= 0 {
		maxClicks = defaultMaxLoadMore
	}

	clicks := 0
	for {
		present, err := f.controlPresent(ctx)
		if err != nil {
			return "", err
		}
		if !present {
			// The control is gone. If we have already clicked at least once,
			// this is normal completion. If we never found it at all, the
			// ~650-item listing that always paginates has no control where one
			// is expected — treat that as a selector miss, not an empty success.
			if clicks == 0 {
				return "", f.selectorMiss(ctx)
			}
			break
		}

		if clicks >= maxClicks {
			return "", ErrLoadMoreCapExceeded
		}

		before, err := f.cardCount(ctx)
		if err != nil {
			return "", err
		}

		if err := chromedp.Run(ctx, chromedp.Click(loadMoreXPath, chromedp.BySearch)); err != nil {
			return "", fmt.Errorf("browser: click load-more (iteration %d): %w", clicks, err)
		}
		clicks++

		// Wait for the click to actually render new cards before re-checking
		// the control. Polling until the count strictly increases closes the
		// race between our re-check and the asynchronous DOM update. A timeout
		// here means the click produced no new cards: stop rather than spin,
		// and return what loaded (the control was found, so this is not a miss).
		if err := f.waitForNewCards(ctx, before); err != nil {
			break
		}
	}

	var html string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery)); err != nil {
		return "", fmt.Errorf("browser: extract html: %w", err)
	}
	return html, nil
}

// controlPresent reports whether the "Load more" control is currently in the
// DOM, without blocking (AtLeast(0) so a zero-match query returns cleanly
// instead of waiting for the default minimum of one node).
func (f *Fetcher) controlPresent(ctx context.Context) (bool, error) {
	var nodes []*cdp.Node
	err := chromedp.Run(ctx, chromedp.Nodes(loadMoreXPath, &nodes, chromedp.BySearch, chromedp.AtLeast(0)))
	if err != nil {
		return false, fmt.Errorf("browser: query load-more control: %w", err)
	}
	return len(nodes) > 0, nil
}

// cardCount returns the current number of loaded cards (approximated by anchor
// count) for loop synchronization.
func (f *Fetcher) cardCount(ctx context.Context) (int, error) {
	var n int
	if err := chromedp.Run(ctx, chromedp.Evaluate(cardCountExpr, &n)); err != nil {
		return 0, fmt.Errorf("browser: count cards: %w", err)
	}
	return n, nil
}

// waitForNewCards blocks until the card count strictly exceeds before, or the
// settle timeout elapses (returned as an error the caller treats as "no more
// loaded", not as a hard failure).
func (f *Fetcher) waitForNewCards(ctx context.Context, before int) error {
	expr := fmt.Sprintf(`%s > %d`, cardCountExpr, before)
	var done bool
	return chromedp.Run(ctx, chromedp.Poll(expr, &done, chromedp.WithPollingTimeout(clickSettleTimeout)))
}

// selectorMiss captures a full-page screenshot and DOM candidates, uploads the
// screenshot (if an Uploader is configured), and builds the NeedsAgentReview
// error. A failure to capture or upload the screenshot is recorded on the
// result rather than replacing it — the selector miss is the primary fault and
// must always be the outcome surfaced, never escalated into a hard error by a
// secondary upload failure.
func (f *Fetcher) selectorMiss(ctx context.Context) *NeedsAgentReview {
	res := &NeedsAgentReview{
		Step:   "load_more_detection",
		Reason: "primary_selector_not_found",
	}

	res.DOMCandidates = f.domCandidates(ctx)

	var shot []byte
	if err := chromedp.Run(ctx, chromedp.FullScreenshot(&shot, 90)); err != nil {
		res.UploadErr = fmt.Errorf("capture screenshot: %w", err)
		return res
	}
	f.uploadScreenshot(ctx, res, shot)
	return res
}

// uploadScreenshot uploads shot via the configured Uploader and records the
// resulting URL (or, on failure/absence, leaves ScreenshotURL empty and notes
// why). Split from selectorMiss so it can be exercised without a live browser.
func (f *Fetcher) uploadScreenshot(ctx context.Context, res *NeedsAgentReview, shot []byte) {
	if f.Uploader == nil {
		// No S3 configured (e.g. on-demand local run) — skip upload, still
		// surface the review with an empty ScreenshotURL.
		return
	}
	key := path.Join(f.ScreenshotPrefix, fmt.Sprintf("selector-miss-%d.png", time.Now().UTC().UnixNano()))
	url, err := f.Uploader.Upload(ctx, key, shot, "image/png")
	if err != nil {
		res.UploadErr = fmt.Errorf("upload screenshot: %w", err)
		return
	}
	res.ScreenshotURL = url
}

// domCandidatesExpr collects the visible text of clickable elements as
// suggestions for whoever fixes the selector.
const domCandidatesExpr = `Array.from(document.querySelectorAll('button, a, [role="button"]'))
	.map(function (e) { return (e.textContent || '').trim(); })
	.filter(function (t) { return t.length > 0 && t.length < 60; })
	.filter(function (t, i, a) { return a.indexOf(t) === i; })
	.slice(0, 10)`

// domCandidates returns visible texts of nearby clickable elements. A failure
// here is non-fatal: candidates are a debugging aid, not required for the
// review to be actionable.
func (f *Fetcher) domCandidates(ctx context.Context) []string {
	var candidates []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(domCandidatesExpr, &candidates)); err != nil {
		return nil
	}
	// Normalize whitespace so multi-line labels read cleanly.
	for i, c := range candidates {
		candidates[i] = strings.Join(strings.Fields(c), " ")
	}
	return candidates
}
