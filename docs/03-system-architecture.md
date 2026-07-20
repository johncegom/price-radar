# PriceRadar — System Architecture (Go)

Concrete tech stack and structure. Companion to [Solution Architecture](02-solution-architecture.md), which covers the conceptual pipeline this implements.

## Tech stack constraints
- **Language:** Go, standard library only for HTTP — **no HTTP framework** (no gin/echo/chi). `net/http` for both client and (later, MCP) server transport.
- **Parsing:** `regexp` (stdlib) — zero third-party dependencies, consistent with the zero-dependency pattern already used elsewhere in this workspace's scraper CLIs. (`golang.org/x/net/html` is a fallback option if regex proves too fragile against markup drift — a deliberate tradeoff to revisit only if needed.)
- **Browser automation:** [`chromedp`](https://github.com/chromedp/chromedp) — a deliberate, scoped exception to the zero-third-party-dependency stance. Needed because the listing's full ~650-item catalog has no URL-based pagination (verified empirically — `?trang=`, `?page=`, `?p=` all return the identical first batch) and no compliant alternative: the site's internal API (`papi.fptshop.com.vn`) is undocumented and unstable, and the filtered/query-parameter URLs that would otherwise narrow results are explicitly disallowed by `robots.txt`. Driving the page's own "Load more" control via chromedp fetches the same permitted URL a real user would, just automated. Chosen over `go-rod` and `playwright-go` for largest community + most active maintenance + no separate driver-install step (pure Go, CDP-based, only needs a local Chrome/Chromium binary).
- **Storage:** `encoding/json`, flat file, no database.
- **Scheduling:** OS-level scheduler (Windows Task Scheduler / cron), not an in-process daemon.

## Module layout

```
priceradar/
  cmd/
    priceradar/            # one-shot CLI entrypoint (default, scheduler-invoked)
      main.go
    priceradar-mcp/         # optional MCP server entrypoint (extension)
      main.go
  internal/
    httpclient/             # net/http.Client wrapper: UA header, timeout, retry/backoff
    browser/                # chromedp wrapper: navigate, click "Load more" loop, screenshot-on-selector-miss
    parser/                 # HTML -> []Product via regexp
    prefilter/              # token-overlap scoring -> ShortList
    store/                  # JSON price-history read/append
    model/                  # shared structs: Product, Snapshot, Candidate, Target
    tmp/                    # generated at runtime — screenshots written on selector-miss (see internal/browser)
  skill/
    judgment.md             # agent instructions: matching rules, notify rules, output contract
  config.json                # target device spec(s), listing URL, notify thresholds
  price-history.json          # generated at runtime — append-only snapshot log
```

## Component detail

### `internal/httpclient`
- One `*http.Client` built once, `Timeout` set explicitly (e.g. 15s).
- Custom `http.RoundTripper` wrapping `http.DefaultTransport`, solely to inject a realistic desktop User-Agent header — the idiomatic stdlib substitute for HTTP client "middleware."
- Retry loop (plain `for`, not a library) around `client.Do(req)`: exponential backoff with cap on network errors / 429 / 5xx; anything else propagates as an error.
- Requests built via `http.NewRequestWithContext`, so a `context.WithTimeout` at the call site bounds total run time.

### `internal/browser`
- Owns a single chromedp browser context for the run — navigates to the clean listing URL, then loops: find the "Load more" control, click it, wait for new cards to render, repeat until the control is gone or the reported total is reached.
- **Selector strategy: text-content match, not CSS class.** The control's actual markup has no stable `id`/class (only Tailwind utility classes that regenerate on every frontend rebuild) — so the primary selector targets the visible text ("Xem thêm") via an XPath-style query, not styling classes, to survive markup churn.
- **On selector miss (mechanical fault, not a hard error):** captures a full-page screenshot via chromedp's screenshot action (the browser context is already open at this point, so this is nearly free), writes it to `internal/tmp/`, and surfaces a `needs_agent_review` result up to `main()` instead of panicking or silently returning a partial list. See the CLI contract below for how this becomes exit code 2.
- This package is the only one that knows a headless browser exists — `parser` still consumes plain HTML text regardless of whether it came from `httpclient` (initial batch) or `browser` (full catalog after all clicks).

### `internal/parser`
- Per-product-card regex extraction into `model.Product{Name, URL, Price, OriginalPrice, DiscountPct, InStock, FetchedAt}`.
- Fault-isolated: a single card's parse failure is logged and skipped, not fatal to the run.

### `internal/prefilter`
- Tokenizes target spec and each product name identically (lowercase, split on whitespace/hyphen).
- Hard-exclude list for category mismatches (cheap, safe — never a false negative risk).
- Score = matched tokens / total target tokens; soft-include threshold biased toward recall.
- Output: `model.Candidate{Product, Score, MatchedTokens, MissingTokens}`.

### `internal/store`
- `map[string][]model.Snapshot]` keyed by product URL, `encoding/json` marshal/unmarshal.
- Atomic write: serialize to a temp file, then `os.Rename` over the target — avoids partial writes if the process is interrupted mid-write.

### `cmd/priceradar` (one-shot CLI)
Straight-line `main()`, no goroutines required for a single-page run:
1. Load `config.json`.
2. Fetch initial batch (`httpclient`) + drive `browser` to load the full catalog.
3. Parse the resulting HTML.
4. Prefilter → shortlist.
5. Print shortlist + history diff as JSON to stdout (for the agent layer to consume) and/or hand off directly to an in-process judgment call.
6. Append new snapshots to `price-history.json`.

**Exit code / stdout contract** — a three-way outcome, not just success/failure:

| Exit code | Meaning | stdout / stderr |
|---|---|---|
| `0` | Success | JSON with `shortlist` and `since_last_run` fields on stdout |
| `1` | Hard error | `{error, code}` JSON on stderr (matches this workspace's other scraper CLIs) |
| `2` | `needs_agent_review` — `internal/browser` couldn't locate the "Load more" control by its primary selector | JSON on stdout: `{"status": "needs_agent_review", "step": "load_more_detection", "reason": "primary_selector_not_found", "screenshot_path": "internal/tmp/load-more-<timestamp>.png", "dom_candidates": [...]}` |

Exit code 2 is a deliberate third state, not a variant of the error path: it means the deterministic core *correctly* recognized it couldn't decide something mechanical (has the site's markup changed?) and handed the decision to the wrapping Agent instead of guessing. The Agent — already running this CLI directly against the repo (see the project README's Usage section) — reads the screenshot, decides the correct selector/action, records that decision somewhere the next invocation reads (e.g. a `selector-override.json`, or a `--selector` flag), and re-invokes. `internal/*` needs no LLM API client for this — the entire hand-off is files-on-disk plus an exit code.

### Concurrency (only if pagination is needed)
If the listing spans multiple pages, fetch them concurrently with a small `sync.WaitGroup` + goroutines over `internal/httpclient` — still stdlib-only, no `errgroup` dependency required unless cleaner error aggregation becomes worth the tradeoff later.

### 6. MCP Extension (optional)
A second entrypoint, `cmd/priceradar-mcp`, exposes the same `internal/*` packages as MCP tools/resources rather than a CLI:

| MCP surface | Backed by | Purpose |
|---|---|---|
| Tool `fetch_listings` | `httpclient` + `parser` + `prefilter` | Run one fetch cycle, return shortlist + total scanned count |
| Tool `get_price_history(url)` | `store` | Return snapshot history for one product URL |
| Tool `get_target_config` | `config.json` | Return configured target spec(s) + notify thresholds |
| Resource `judgment-instructions` | `skill/judgment.md` | Serve the judgment criteria so any MCP host loads it automatically |
| Tool `verify_page_structure(screenshot)` *(future)* | `browser` | Formalized version of the exit-code-2 hand-off — lets an MCP host resolve a selector-miss interactively instead of via exit code + re-invoke. Not built now: the CLI's exit-code/JSON contract already covers this, and building the tool early would front-run the rest of this optional extension. |

- **Transport:** stdio — this is a local, personal-use tool; no network exposure, no auth surface.
- **Adapter-only impact:** `cmd/priceradar-mcp/main.go` is a thin translation layer over the existing `internal/*` packages. None of the core packages need to know MCP exists — this is what makes the extension additive rather than a rewrite.

## Deployment shape

```mermaid
flowchart TB
    subgraph Local machine
        T[OS Scheduler\nTask Scheduler / cron] -->|invokes| CLI[priceradar one-shot binary]
        CLI --> HF[price-history.json]
        Host[MCP-capable agent host] -.optional interactive path.-> MCP[priceradar-mcp]
        MCP --> HF
    end
    CLI -->|HTTPS GET| Site[fptshop.com.vn/may-doi-tra]
    MCP -->|HTTPS GET| Site
```

Both entrypoints share the same `internal/*` core and the same `price-history.json` file — the scheduled batch path and the interactive MCP path are two doors into one system, not two systems.

## Future extensibility: where a second site would plug in (not built yet)

Per [Solution Architecture § Future Extensibility](02-solution-architecture.md#future-extensibility-not-built-yet), only `internal/httpclient` + `internal/parser` are site-specific today. Concretely, when a second site becomes real (not before):

```
internal/
  sites/
    fptshop/                # today's httpclient+parser logic moves here unchanged
      fetch.go
      parse.go
    <secondsite>/            # new site, same shape, own fetch.go/parse.go
  siteplugin/                 # the interface both above satisfy: Fetch(target) -> []model.Product
  prefilter/                  # unchanged — already site-agnostic
  store/                      # unchanged — keyed by product URL, not by site
  model/                      # unchanged
```

`config.json` would gain a `site` field per target so the CLI knows which `siteplugin` implementation to invoke; everything downstream of `[]model.Product` — prefilter, judgment, store, notify — needs no changes at all. This restructuring is deliberately deferred until a second real site justifies the abstraction.
