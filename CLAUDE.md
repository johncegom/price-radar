# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Early stage — planning only, no Go code yet. There is no `go.mod`, no `cmd/`, no `internal/` on disk yet. This repo currently consists of `README.md` and `docs/`. When implementation starts, build it per the architecture documented below and in `docs/`; there are no build/lint/test commands to run until then. Once a Go module exists, standard commands apply: `go run .` / `go build ./...` / `go test ./...` (a single test: `go test ./internal/<pkg> -run TestName`).

## What this is

A personal Go program that checks whether a specific device (e.g. MacBook Pro M2 Pro 32GB/512GB) is listed on `fptshop.com.vn/may-doi-tra` (FPT Shop's returned/refurbished device listing) and tracks its price over time. Single-site (FPT Shop) today, with the pipeline deliberately shaped so a second site could plug in later without touching the core.

**This project is built autonomously by AI agents**, not hand-written — see the README's "How this is built" section. It doubles as a testbed for high-autonomy, multi-agent development: an agent (or swarm) plans and writes the implementation directly against this repo; a human reviews direction/outcomes rather than authoring code.

## Core design principle: deterministic core vs. judgment layer

The whole architecture hinges on this split — read it before touching any pipeline stage:

| Layer | Owns | Why |
|---|---|---|
| **Deterministic core** (Go) | Fetch, parse, cheap prefiltering, storage | Fast, free, reliable — no ambiguity to resolve |
| **Judgment layer** (AI agent, at runtime) | Resolving ambiguous device-name matches, deciding if a price is worth flagging | Naming variation and "is this a good deal" are contextual, not rule-shaped |

Only a short list (0–5 candidates), never the full ~650-item catalog, is ever handed to the judgment layer — this keeps the non-deterministic/expensive work proportional to actual ambiguity. Do not add LLM calls inside the deterministic core; the boundary is intentional (see `docs/02-solution-architecture.md`, "Why this shape, not simpler").

## Pipeline (planned package layout)

```
priceradar/
  cmd/
    priceradar/            # one-shot CLI entrypoint (default, scheduler-invoked)
    priceradar-mcp/        # optional MCP server entrypoint (extension, build later)
  internal/
    httpclient/            # net/http.Client wrapper: UA header, timeout, retry/backoff
    browser/                # chromedp wrapper: navigate, click "Load more" loop, screenshot-on-selector-miss
    parser/                 # HTML -> []Product via regexp
    prefilter/              # token-overlap scoring -> ShortList
    store/                  # JSON price-history read/append
    model/                  # shared structs: Product, Snapshot, Candidate, Target
    tmp/                    # generated at runtime — screenshots written on selector-miss
  skill/
    judgment.md             # agent instructions: matching rules, notify rules, output contract
  config.json                # target device spec(s), listing URL, notify thresholds
  price-history.json          # generated at runtime — append-only snapshot log
```

Data flow: Scheduler → Fetch (httpclient for initial batch, browser/chromedp to exhaust "Load more" for the full ~650-item catalog) → Parse → Deterministic Prefilter → (shortlist) Agent Judgment → Price History Store / Notify.

## Tech stack constraints (deliberate, don't deviate without discussion)

- **No HTTP framework** — `net/http` only (no gin/echo/chi), for both the CLI's outbound requests and the future MCP server transport.
- **No third-party HTML parser by default** — `regexp` (stdlib) for parsing product cards. `golang.org/x/net/html` is an explicitly-flagged fallback only if regex proves too fragile, not a default choice.
- **`chromedp` is the one sanctioned third-party dependency**, scoped narrowly to `internal/browser`, because the listing has no URL-based pagination (`?trang=`, `?page=`, `?p=` all verified to return the identical first batch) and the site's internal API + filtered-URL scraping are both off-limits (see Compliance below). Don't reach for `go-rod`/`playwright-go` or add other dependencies without an equivalent justification.
- **Storage:** `encoding/json`, flat file (`price-history.json`), no database. Writes must be atomic: temp file + `os.Rename`, never a direct in-place write.
- **Scheduling:** OS-level (Windows Task Scheduler / cron) driving a one-shot binary — not an in-process daemon/goroutine scheduler.

## CLI exit-code contract

`cmd/priceradar` uses a **three-way** exit contract, not just success/failure — preserve this shape if you touch `main()`:

| Exit code | Meaning | Output |
|---|---|---|
| `0` | Success | JSON with `shortlist` and `since_last_run` fields on stdout |
| `1` | Hard error | `{error, code}` JSON on stderr |
| `2` | `needs_agent_review` — `internal/browser` couldn't locate the "Load more" control | JSON on stdout: `{"status": "needs_agent_review", "step": "load_more_detection", "reason": "primary_selector_not_found", "screenshot_path": ..., "dom_candidates": [...]}` |

Exit code 2 is a deliberate hand-off, not an error variant: the deterministic core recognized it can't decide something mechanical and stops rather than guessing. It's a *different* judgment than the shortlist/product-matching judgment — it's about whether the scraping mechanism still matches reality.

## Compliance constraints (do not violate when writing fetch/browser code)

- Only the clean listing URL (and plain pagination) is ever fetched or driven — **never** the filtered/query-parameter URLs `robots.txt` disallows (e.g. `?hang-san-xuat=`, `?ram=`), and never the disallowed `/ajax/` path or the site's undocumented internal API (`papi.fptshop.com.vn`).
- Requests must stay infrequent (hours between runs, not seconds), use a realistic User-Agent, and back off on errors (429/5xx) with exponential delay + a cap.
- The headless-browser path exists solely to drive the page's own "Load more" control against the *same permitted URL* — it is not a workaround to reach disallowed endpoints.

## Selector strategy (internal/browser)

Select the "Load more" control by its **visible text content** ("Xem thêm"), not by CSS/Tailwind utility classes — those regenerate on every frontend rebuild and are not a stable signal. On selector miss, capture a full-page screenshot to `internal/tmp/` and surface `needs_agent_review` (exit 2) rather than panicking or silently returning a partial list.

## Judgment layer contract

`skill/judgment.md` (not yet written) is read directly from this repo by the wrapping agent — it is not passed as an opaque embedded prompt. When creating/editing it, keep an explicit output contract: matched URL or none, confidence, one-line reason. The judgment layer must be able to say "no match" rather than forcing a pick when the true match is absent from the shortlist (prefilter is recall-biased, so this will happen).

## Extensibility boundary (don't build until a second site is real)

Only `internal/httpclient` + `internal/parser` are site-specific. If/when a second site is added, the concrete shape is: move FPT Shop's current logic into `internal/sites/fptshop/`, introduce a `siteplugin` interface (`Fetch(target) -> []model.Product`), add the new site alongside it. `prefilter`, judgment, `store`, and `notify` should need **zero** changes — if a change there turns out to be required, treat that as a signal the original design missed something, not something to route around.

## Docs

- `docs/00-prd.md` — the agreed v1 requirements, non-goals, and acceptance criteria; check this before treating an ambiguous case as an invitation to expand scope.
- `docs/01-project-description.md` — what it is, why it exists, what it deliberately doesn't do, compliance posture.
- `docs/02-solution-architecture.md` — the conceptual pipeline, design rationale, "why this shape, not simpler."
- `docs/03-system-architecture.md` — concrete Go package layout, component-by-component detail, CLI/MCP contracts, deployment shape.
- `docs/04-implementation-plan.md` — the epic/task breakdown for building the pipeline, in dependency order; follow this when picking up implementation work.
