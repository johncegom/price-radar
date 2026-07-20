# PriceRadar — Implementation Plan

This document breaks the architecture in [Solution Architecture](02-solution-architecture.md) and [System Architecture](03-system-architecture.md) into an epic → task sequence for autonomous AI agents to execute directly against this repo. It replaces the earlier human-paced building plan, which was removed when the project was reframed as agent-built (see [Project Description](01-project-description.md) and `CLAUDE.md`).

Each task lists what gets built, a concrete "done" signal, and its dependencies, so progress is verifiable without step-by-step human supervision. Epics are sequenced by actual build dependency order.

## Resolved design gap: verdict write-back

The architecture docs note "the agent (or the CLI, on the agent's behalf) appends the observation" to `price-history.json`, without fully resolving which. Decision: **verdict write-back goes through a new CLI subcommand (`priceradar record-verdict`)**, not direct agent file edits — this preserves `internal/store`'s atomic temp-file-plus-rename write guarantee instead of letting the agent bypass it. See E7/T7.4 below.

## Epics & tasks (build now, in dependency order)

### E0 — Repo scaffold
- **T0.1** Initialize `go.mod` (module `priceradar`) and stub package directories for `cmd/priceradar`, `internal/{httpclient,browser,parser,prefilter,store,model}`, `skill/`. `internal/tmp/` is created at runtime, not checked in.
  Done: `go build ./...` and `go vet ./...` clean with stub packages only.
- **T0.2** `config.json` schema (target device spec(s), listing URL, notify thresholds) + a loader (inline in `cmd/priceradar/main.go`, matching the documented "straight-line main()" shape — no separate `internal/config` package).
  Done: loader unmarshals a fixture `config.json` without error; a test covers malformed JSON producing a clear error.
  Depends on: T0.1.

### E1 — Shared model
- **T1.1** `internal/model`: `Product{Name,URL,Price,OriginalPrice,DiscountPct,InStock,FetchedAt}`, `Snapshot`, `Candidate{Product,Score,MatchedTokens,MissingTokens}`, `Target`.
  Done: package builds; JSON marshal/unmarshal round-trip test per struct (these types cross the CLI stdout boundary and the store file, so tags must be correct).
  Depends on: T0.1.

### E2 — HTTP fetch (initial batch)
- **T2.1** `internal/httpclient`: single `*http.Client` with explicit `Timeout`; custom `http.RoundTripper` wrapping `http.DefaultTransport` to inject a realistic desktop User-Agent.
  Done: `httptest.Server`-based test confirms the UA header lands on every request.
- **T2.2** Retry/backoff loop around `client.Do`: exponential backoff with cap on network errors/429/5xx; other errors/status propagate immediately. Requests via `http.NewRequestWithContext` so caller `context.WithTimeout` bounds total time.
  Done: tests cover first-try success, success-after-N-retries, gives-up-after-cap, non-retryable status short-circuits, context cancellation aborts mid-retry.
  Depends on: T2.1.
- **T2.3** `httpclient.FetchListing(ctx, url) ([]byte, error)` — GETs only the clean listing URL, no query-param construction (never the filtered/query-param URLs `robots.txt` disallows).
  Done: test against `httptest.Server` fixture page; test/comment documents the function never appends query params.
  Depends on: T2.2.

### E3 — HTML parsing
- **T3.1** `internal/parser/testdata/`: fixture HTML with multiple product cards, including one deliberately malformed card (missing price/broken tag/empty name), covering in-stock/out-of-stock and discounted/non-discounted variety.
  Depends on: E1.
- **T3.2** `internal/parser.Parse(html string) []model.Product`, regex-based, per-card, fault-isolated (bad card logged/skipped, not fatal).
  Done: test asserts correct well-formed-card count, malformed card skipped without panic, at least one full card's fields match golden values.
  Depends on: T3.1.

### E4 — Browser fetch (full catalog)
The one sanctioned third-party dependency; keep it scoped to this package only.
- **T4.1** Add `github.com/chromedp/chromedp` to `go.mod`.
  Done: `go build ./...` succeeds; grep confirms only `internal/browser` imports chromedp.
  Depends on: T0.1.
- **T4.2** Open a chromedp browser context, navigate to the clean listing URL only.
  Done: Chrome-gated test (skippable via build tag/`-short` if no browser present) against a local `httptest.Server` fixture confirms page load.
  Depends on: T4.1.
- **T4.3** "Load more" click loop selecting by **visible text** ("Xem thêm"), not CSS class — click, wait for new cards, repeat until control disappears, with a bounded iteration cap as a safety net.
  Done: integration test against a fixture page simulating incremental card loading; loop terminates with full count, never infinite.
  Depends on: T4.2.
- **T4.4** Selector-miss handling: on failure to find the control, capture a full-page screenshot to `internal/tmp/`, return a structured `needs_agent_review` result (reason, screenshot path, DOM candidates) instead of panicking or returning a silent partial list.
  Done: test with a no-match fixture asserts screenshot written, structured error populated, no partial list silently treated as complete.
  Depends on: T4.3.
- **T4.5** `browser.FetchFullCatalog(ctx, url) (html string, err error)` composing T4.2–T4.4, returning HTML in the same shape `internal/parser` consumes.
  Done: Chrome-gated end-to-end test; cross-check that returned HTML parses via `internal/parser.Parse` unchanged.
  Depends on: T4.4, T3.2 (cross-check only).

### E5 — Deterministic prefilter
- **T5.1** Shared tokenizer (lowercase, split on whitespace/hyphen) used identically on target and product names.
  Done: unit tests on hyphenation, case, whitespace edge cases.
  Depends on: E1.
- **T5.2** `internal/prefilter.Filter(target, products) []model.Candidate` — hard-exclude category mismatches, soft-include by score = matched/total tokens, biased toward recall.
  Done: tests cover mismatch exclusion, exact-match top score, imperfect-but-plausible match retained (recall bias), 0–5 candidate output on a representative fixture, empty input handled cleanly.
  Depends on: T5.1.

### E6 — Store
- **T6.1** `internal/store`: load `price-history.json` (missing file → empty map, not error), append snapshot per product URL, atomic write (temp file + `os.Rename`).
  Done: tests cover missing-file-first-run, append-then-reload round-trip, additive history (no dedup — multiple snapshots per URL retained), and an interrupted-write simulation proving the original file is untouched if the write is aborted before rename.
  Depends on: E1.

### E7 — CLI wiring + exit contract
- **T7.1** `cmd/priceradar/main.go` happy path: config → httpclient fetch → browser fetch full catalog → parse → prefilter → print `{shortlist, since_last_run}` JSON to stdout → append snapshots to `price-history.json` → exit 0. Extract a testable `run(...) int` so exit-path tests don't need subprocess spawns.
  Done: test confirms exit 0 and stdout JSON shape.
  Depends on: T2.3, T4.5, T3.2, T5.2, T6.1.
- **T7.2** Hard-error path (config load failure, retries-exhausted fetch failure, store write failure) → `{error, code}` JSON on stderr, exit 1.
  Done: table-driven tests via the extracted `run()`.
  Depends on: T7.1.
- **T7.3** `needs_agent_review` path: wire `internal/browser`'s selector-miss result to stdout JSON (`status`, `step`, `reason`, `screenshot_path`, `dom_candidates`) and exit 2 — deliberately on stdout, not stderr.
  Done: test forcing selector-miss asserts exit 2 and exact field set.
  Depends on: T7.1, T4.4.
- **T7.4** `priceradar record-verdict` subcommand: takes the agent's judgment output (matched URL or none, confidence, reason) and appends it through `internal/store`, preserving atomic-write guarantees — this is how the judgment layer's verdict gets recorded (see the resolved design gap above).
  Done: test round-trips a verdict through the subcommand into `price-history.json` and confirms atomicity guarantees still hold (reuses T6.1's store, no direct file edits).
  Depends on: T6.1, T7.1.

### E8 — Judgment layer content
- **T8.1** Write `skill/judgment.md`: matching rules (chip generation, RAM/storage order, "Demo"/refurb labels), notify rules (new match, meaningful price drop, below-threshold), explicit output contract (matched URL or none, confidence, one-line reason) — must allow "no match" as a valid verdict, not force a pick.
  Done: content reviewed against the exact output-contract fields `T7.4`'s `record-verdict` subcommand expects, so the round trip (agent reads shortlist JSON → applies `judgment.md` → calls `record-verdict`) is coherent end-to-end.
  Depends on: T7.1 (needs the real shortlist JSON shape to write realistic instructions against), T7.4 (needs the verdict subcommand's expected input shape).

### E9 — Notify
- **T9.1** Minimal `internal/notify` with a log-line notifier (zero-config) as the default channel; delivery decoupled from the judgment decision. No queueing/retry — this is low-frequency, best-effort.
  Done: test confirms verdict formatting; if a webhook notifier is added, test POST payload via `httptest.Server`.
  Depends on: T8.1.

### E10 — End-to-end hardening
- **T10.1** Full pipeline integration test: config → fetch (mocked) → parse → prefilter → judgment-shape output → store, pinning the CLI's agent-facing JSON contract so future changes don't silently break it.
  Done: `go test ./...` green.
  Depends on: E7, E8, E9.
- **T10.2** Compliance guardrail tests: assert no code path ever constructs a disallowed URL (filtered query params, `/ajax/`, `papi.fptshop.com.vn`) — fetch functions only ever hit the configured clean listing URL.
  Done: tests pass; this is a correctness requirement, not a style preference.
  Depends on: T2.3, T4.5.
- **T10.3** Update `README.md` / `CLAUDE.md` "Status" sections from "planning only, no code yet" to reflect the built pipeline.
  Depends on: T10.1.

## Deferred epics (trigger-based, not scheduled)

### E11 — MCP extension
`cmd/priceradar-mcp` exposing `fetch_listings`, `get_price_history`, `get_target_config` tools + `judgment-instructions` resource, later `verify_page_structure`. Explicitly not built now — only build once an MCP-capable agent host is actually going to be used interactively against this repo.

### E12 — Second-site extensibility
`internal/siteplugin` interface, migrate FPT Shop logic into `internal/sites/fptshop/`, add `site` field to `config.json` targets. Deliberately deferred until a second real site is real — do not build speculatively.

## Sequencing

```
E0 → E1
E1 → E2, E3, E5, E6 (parallelizable)
E3 → E4 (cross-check dependency)
E2, E3, E4, E5, E6 → E7
E7 → E8 → E9
E7, E8, E9 → E10
[deferred] E11, E12 — build on E0–E10's internal/* packages, not scheduled
```

## Verification

- Per-package: `go test ./...` after each epic lands; each task above lists its own done signal.
- End-to-end (E10.1): run the built `priceradar` binary against fixture/mocked HTTP + a Chrome-gated browser test, confirm exit codes 0/1/2 all reachable on demand and stdout/stderr JSON shapes match `CLAUDE.md`'s documented contract exactly.
- Compliance (E10.2): guardrail tests plus a manual review that no disallowed URL is ever constructed.
- No live-site scraping should be part of automated tests — all HTTP/browser tests run against local fixtures (`httptest.Server` / saved HTML), per the compliance posture (infrequent, low-volume real requests only).
