// Package browser drives a headless browser (chromedp) against the FPT Shop
// listing page to exhaust its "Load more" control and return the fully-loaded
// catalog HTML.
//
// It is the only package that knows a headless browser exists; everything
// downstream (internal/parser) consumes the plain HTML string it returns,
// regardless of whether that HTML came from the initial httpclient fetch or
// from this package after clicking through every "Load more" batch.
//
// The "Load more" control is selected by its visible text content ("Xem
// thêm"), never by CSS/Tailwind utility classes — those regenerate on every
// frontend rebuild and are not a stable signal (ADR-009). On a selector miss
// the package captures a full-page screenshot, uploads it to the configured S3
// bucket/prefix, and surfaces a structured *NeedsAgentReview error rather than
// panicking or silently returning a partial list as if it were complete.
package browser
