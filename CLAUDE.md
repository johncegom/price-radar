# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Early stage — repo scaffold only (`go.mod`, stub `internal/*` packages, a `config.json` loader in `cmd/priceradar/main.go`). No pipeline logic wired up yet. Build/test as a normal Go module regardless of the AWS Lambda deployment target described below: `go run .` / `go build ./...` / `go test ./...` (a single test: `go test ./internal/<pkg> -run TestName`). Deployment to Lambda is a packaging/invocation concern layered on top of an ordinary Go binary — it must not require AWS credentials or a deployed function to build, test, or run the pipeline locally.

## What this is

A personal Go program that checks whether a specific device (e.g. MacBook Pro M2 Pro 32GB/512GB) is listed on `fptshop.com.vn/may-doi-tra` (FPT Shop's returned/refurbished device listing) and tracks its price over time. Single-site (FPT Shop) today, with the pipeline deliberately shaped so a second site could plug in later without touching the core.

**This project is built autonomously by AI agents**, not hand-written — see the README's "How this is built" section. It doubles as a testbed for high-autonomy, multi-agent development: an agent (or swarm) plans and writes the implementation directly against this repo; a human reviews direction/outcomes rather than authoring code.

## Core design principle: deterministic core vs. judgment layer

The whole architecture hinges on this split — read it before touching any pipeline stage:

| Layer | Owns | Why |
|---|---|---|
| **Deterministic core** (Go) | Fetch, parse, cheap prefiltering, storage | Fast, free, reliable — no ambiguity to resolve |
| **Judgment layer** (Claude API call, made by the Lambda handler at runtime) | Resolving ambiguous device-name matches, deciding if a price is worth flagging | Naming variation and "is this a good deal" are contextual, not rule-shaped |

Only a short list (0–5 candidates), never the full ~650-item catalog, is ever handed to the judgment layer — this keeps the non-deterministic/expensive work proportional to actual ambiguity. Do not add LLM calls inside `httpclient`/`browser`/`parser`/`prefilter`/`store` — those stay deterministic. `internal/judge` (the one package that calls the Claude API) is the sole exception, and it runs *after* prefiltering, over the shortlist only — see ADR-010 in `docs/05-decisions.md` for why this moved from an external wrapping-agent invocation to an in-process runtime API call, and why selector-miss handling (below) deliberately did **not** make the same move.

## Pipeline (planned package layout)

```
priceradar/
  cmd/
    priceradar/            # dual-mode entrypoint: Lambda handler (EventBridge-invoked) when deployed, first-class on-demand local trigger otherwise
    priceradar-mcp/        # optional MCP server entrypoint (extension, build later)
  internal/
    httpclient/            # net/http.Client wrapper: UA header, timeout, retry/backoff
    browser/                # chromedp wrapper: navigate, click "Load more" loop, screenshot-on-selector-miss (uploads to S3)
    parser/                 # HTML -> []Product via regexp
    prefilter/              # token-overlap scoring -> ShortList
    store/                  # JSON price-history read/append, backed by a single S3 object
    judge/                   # Claude API client: shortlist + skill/judgment.md -> verdict
    notify/                  # verdict/alert delivery: log line + SNS
    model/                  # shared structs: Product, Snapshot, Candidate, Target
  skill/
    judgment.md             # judgment-layer instructions: matching rules, notify rules, output contract (embedded into the Lambda package via go:embed)
  config.json                # target device spec(s), listing URL, notify thresholds, S3/SNS identifiers
```

`price-history.json` is no longer a repo-local file — it's the object key inside the S3 bucket configured in `config.json`. Selector-miss screenshots go to S3 too (see Selector strategy below), not a local `internal/tmp/`.

Data flow: EventBridge Scheduler → Lambda invocation → Fetch (httpclient for initial batch, browser/chromedp to exhaust "Load more" for the full ~650-item catalog) → Parse → Deterministic Prefilter → (shortlist) Judgment (`internal/judge` calls the Claude API) → Price History Store (S3) / Notify (SNS) — all within one Lambda invocation, no external round trip.

## Tech stack constraints (deliberate, don't deviate without discussion)

- **No HTTP framework** — `net/http` only (no gin/echo/chi), for the outbound site fetch, the Claude API call in `internal/judge`, and the future MCP server transport.
- **No third-party HTML parser by default** — `regexp` (stdlib) for parsing product cards. `golang.org/x/net/html` is an explicitly-flagged fallback only if regex proves too fragile, not a default choice.
- **`chromedp` is the one sanctioned third-party dependency for scraping**, scoped narrowly to `internal/browser`, because the listing has no URL-based pagination (`?trang=`, `?page=`, `?p=` all verified to return the identical first batch) and the site's internal API + filtered-URL scraping are both off-limits (see Compliance below). Don't reach for `go-rod`/`playwright-go` or add other scraping dependencies without an equivalent justification. On Lambda, chromedp additionally needs a headless-Chromium Lambda Layer (e.g. a `sparticuz/chromium`-style prebuilt layer) — see `docs/03-system-architecture.md` for the layer/memory/timeout details.
- **AWS SDK for Go v2** (`aws-sdk-go-v2`, S3 + SNS clients only) and **`aws-lambda-go`** (the Lambda runtime shim) are sanctioned dependencies for the AWS integration surface — same "narrowly scoped, deliberately justified" treatment as chromedp, not a general license to add AWS SDK modules elsewhere.
- **Storage:** `encoding/json`, one JSON object (`price-history.json` as an S3 key), no database. Writes are whole-object `PutObject` calls, protected by an ETag-conditioned (`If-Match`/`If-None-Match`) get-modify-put retry loop rather than a bare overwrite — S3 makes a single write atomic from a reader's perspective, but with more than one possible writer now (scheduled Lambda + on-demand local trigger, see ADR-011), the conditional-write retry is what prevents a lost update; see ADR-012 and `docs/03-system-architecture.md` § `internal/store`. Pre-deployment, when no S3 bucket is configured, `internal/store` falls back to a local JSON file instead — no concurrency protection needed there (single machine, single process).
- **Scheduling:** AWS EventBridge Scheduler invoking the Lambda function on a cron expression — not an in-process daemon/goroutine scheduler, and not an OS-level scheduler (superseded; see ADR-006).
- **Judgment-layer LLM calls:** the Claude API, called directly from `internal/judge` inside the Lambda handler over `net/http` (no SDK needed for a single-request/response call) — see ADR-010. Structure-verification (selector-miss, below) deliberately does **not** get this treatment.

## Lambda handler outcome contract

`cmd/priceradar`'s handler uses a **three-way** outcome contract, adapted from the original CLI exit-code design for Lambda's `(response, error)` handler shape — preserve this shape if you touch the handler:

| Outcome | Lambda return | Meaning |
|---|---|---|
| Success | `(Response{shortlist, since_last_run, ...}, nil)` | Normal run, JSON response body |
| Hard error | `(nil, err)` | Network/config/store failure — Lambda records an invocation error; EventBridge's retry policy may retry, and a configured on-failure destination (e.g. SNS) can alert after retries are exhausted |
| `needs_agent_review` | `(Response{status: "needs_agent_review", step: "load_more_detection", reason: "primary_selector_not_found", screenshot_url: <S3 URL>, dom_candidates: [...]}, nil)` | `internal/browser` couldn't locate the "Load more" control |

`needs_agent_review` deliberately returns as a **successful** invocation (`nil` error), not a Lambda error — retrying immediately won't fix a broken selector, so this must not trigger Lambda's automatic retry storm. Instead the handler itself uploads the screenshot to S3 and publishes an SNS notification before returning, so a human/agent can review it later; see ADR-009. This mirrors the original exit-code design's intent — distinguishing "retry might help" from "a human/agent needs to look at this" — just adapted to Lambda's return-value contract instead of a process exit code that an always-present watcher would inspect live.

## Invocation modes

`cmd/priceradar/main()` picks its mode by checking for the `AWS_LAMBDA_RUNTIME_API` env var (set by the Lambda runtime, absent locally): present → `lambda.Start(handler)`; absent → run `run(ctx, cfg)` once as an on-demand local trigger, printing the `Response` JSON to stdout and exiting `0`/`1` per the same three-way contract above (see ADR-011). This on-demand mode is a first-class, documented trigger — not just a test harness — usable today, pre-deployment, and it works without any AWS resources provisioned: `internal/store`/`internal/notify` fall back to a local JSON file / log-only notify when `config.json` has no S3/SNS configured. Once the Lambda is deployed, the MCP extension (E11, deferred until then) becomes the intended production-facing on-demand trigger instead; the local mode keeps working alongside it for direct dev-stage use.

## Compliance constraints (do not violate when writing fetch/browser code)

- Only the clean listing URL (and plain pagination) is ever fetched or driven — **never** the filtered/query-parameter URLs `robots.txt` disallows (e.g. `?hang-san-xuat=`, `?ram=`), and never the disallowed `/ajax/` path or the site's undocumented internal API (`papi.fptshop.com.vn`).
- Requests must stay infrequent (hours between runs, not seconds), use a realistic User-Agent, and back off on errors (429/5xx) with exponential delay + a cap.
- The headless-browser path exists solely to drive the page's own "Load more" control against the *same permitted URL* — it is not a workaround to reach disallowed endpoints.

## Selector strategy (internal/browser)

Select the "Load more" control by its **visible text content** ("Xem thêm"), not by CSS/Tailwind utility classes — those regenerate on every frontend rebuild and are not a stable signal. On selector miss, capture a full-page screenshot and upload it to the S3 bucket/prefix configured in `config.json`, then surface `needs_agent_review` rather than panicking or silently returning a partial list.

**The fix is a code change, not a runtime override.** Because nothing is watching a scheduled Lambda invocation live, the old "wrapping agent reads the screenshot and resumes the same run" hand-off doesn't apply. Instead: the SNS/email notification (with the S3 screenshot link + DOM candidates) is reviewed later, the new selector is committed as a normal change to `internal/browser`, and the Lambda function is redeployed — the next scheduled invocation then uses the fixed selector. Do not build a runtime-read selector-override mechanism (e.g. an SSM parameter the handler consults) — that was considered and deliberately rejected in favor of keeping the fix as an ordinary, reviewable commit (see ADR-009).

## Judgment layer contract

`skill/judgment.md` is embedded into the Lambda deployment package (e.g. via `go:embed`) and read by `internal/judge`, which calls the Claude API directly with the shortlist and these instructions as context — it is not an opaque hardcoded prompt string, and it is not invoked by an external wrapping agent anymore (see ADR-010). When creating/editing it, keep an explicit output contract: matched URL or none, confidence, one-line reason. The judgment layer must be able to say "no match" rather than forcing a pick when the true match is absent from the shortlist (prefilter is recall-biased, so this will happen). The verdict is recorded into the S3-backed price history within the same Lambda invocation — no separate `record-verdict` round trip from an external caller.

## Extensibility boundary (don't build until a second site is real)

Only `internal/httpclient` + `internal/parser` are site-specific. If/when a second site is added, the concrete shape is: move FPT Shop's current logic into `internal/sites/fptshop/`, introduce a `siteplugin` interface (`Fetch(target) -> []model.Product`), add the new site alongside it. `prefilter`, judgment, `store`, and `notify` should need **zero** changes — if a change there turns out to be required, treat that as a signal the original design missed something, not something to route around.

## Docs

- `docs/00-prd.md` — the agreed v1 requirements, non-goals, and acceptance criteria; check this before treating an ambiguous case as an invitation to expand scope.
- `docs/01-project-description.md` — what it is, why it exists, what it deliberately doesn't do, compliance posture.
- `docs/02-solution-architecture.md` — the conceptual pipeline, design rationale, "why this shape, not simpler."
- `docs/03-system-architecture.md` — concrete Go package layout, component-by-component detail, CLI/MCP contracts, deployment shape.
- `docs/04-implementation-plan.md` — the epic/task breakdown for building the pipeline, in dependency order; follow this when picking up implementation work.
- `docs/05-decisions.md` — the Architecture Decision Record log: why things are the way they are, kept separate from the PRD/architecture docs (which describe current state only). When a human and agent agree to change an established architectural decision, add an ADR entry here *and* update the affected PRD/architecture section — don't do just one.
