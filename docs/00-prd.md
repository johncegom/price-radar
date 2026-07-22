# PriceRadar — PRD

This is the front-door requirements doc: what was agreed for v1, stated as testable requirements, so any agent picking up work later checks itself against this rather than reconstructing intent from the other docs. Read this first. For *why* it exists, see [Project Description](01-project-description.md); for *how* it's designed, see [Solution Architecture](02-solution-architecture.md) and [System Architecture](03-system-architecture.md); for *build order*, see [Implementation Plan](04-implementation-plan.md); for *why key decisions were made*, see [Decisions](05-decisions.md).

## Problem & goal

Manually re-checking `fptshop.com.vn/may-doi-tra` for one specific device (e.g. MacBook Pro M2 Pro 32GB/512GB) is tedious and easy to forget. The goal is an autonomous, scheduled checker that fetches the full listing, narrows it to plausible matches for the target device, judges whether a match/price is worth flagging, and records an append-only price history — so trend over time is visible without manual polling.

## Users

Single user: the maintainer, running this personally against their own target device spec(s). Not a multi-tenant product; no auth/accounts model is in scope.

## Functional requirements

Each requirement is checkable against a specific epic/task in [Implementation Plan](04-implementation-plan.md). If a requirement below can't be traced to a task, or a task exists that isn't traceable to a requirement here, that's a drift signal — stop and reconcile before continuing.

| # | Requirement | Owning epic |
|---|---|---|
| FR1 | Fetch the initial listing batch over plain HTTPS, with a realistic User-Agent and retry/backoff on 429/5xx | E2 |
| FR2 | Parse every product card into a structured record (name, price, original price, discount %, stock, URL), tolerating individual malformed cards without aborting the run | E3 |
| FR3 | Fetch the *full* ~650-item catalog by driving the listing page's own "Load more" control (not just the first batch), selecting the control by visible text content, not CSS class | E4 |
| FR4 | On selector miss, capture a screenshot, upload it to S3, and notify (SNS/email) for later human/agent review rather than guessing, blocking on a live reviewer, or returning a silent partial list; the fix is delivered as a code commit + redeploy, not a runtime-read override | E4 |
| FR5 | Deterministically narrow the full listing to a short list (0–5 candidates) of plausible matches for the target device, biased toward recall over precision | E5 |
| FR6 | Persist every observation (price, discount, stock) to an append-only, timestamped history log per product URL, stored as a single JSON object in S3 | E6 |
| FR7 | Expose the pipeline as an AWS Lambda handler (invoked by EventBridge Scheduler) with a three-way outcome contract: success (`(Response, nil)`, shortlist JSON), hard error (`(nil, err)`, Lambda invocation error), needs-agent-review (`(Response{status:"needs_agent_review"}, nil)`, structured JSON, deliberately not an invocation error) | E7 |
| FR8 | Let the judgment layer — `internal/judge`, calling the Claude API directly with `skill/judgment.md` + the shortlist, within the same Lambda invocation — decide match and notify-worthiness, including explicitly returning "no match" rather than forcing a pick, and record that verdict into the S3-backed price history in-process (no external round trip / no separate `record-verdict` caller) | E7, E8 |
| FR9 | Notify (log line, SNS/email, or chat message — channel decoupled from the decision) only when the judgment layer's verdict says so | E9 |

## Non-goals (v1)

These are hard boundaries, not soft defaults — an agent must not silently cross them without a change-control step (see below):

- No scraping of other retailers or categories — one site, one page, one target spec.
- No login, checkout, or purchase automation.
- No filtered/query-parameter scraping and no reliance on the site's undocumented internal API (`robots.txt`-disallowed).
- No continuous/real-time polling — scheduled (EventBridge Scheduler), low-frequency checks only.
- No third-party HTTP framework, no third-party HTML parser by default, no LLM calls inside `httpclient`/`browser`/`parser`/`prefilter`/`store` (`chromedp` is the one sanctioned scraping dependency, scoped to `internal/browser`; `aws-sdk-go-v2` and `aws-lambda-go` are the sanctioned AWS-integration dependencies). The judgment layer (`internal/judge`) *does* call the Claude API at runtime by design — see ADR-010 — that exception is scoped to judgment only and does not extend to the deterministic stages.
- No runtime-read selector-override mechanism for selector-miss recovery — the fix is always a reviewed code commit + redeploy (see ADR-009).
- No MCP extension or second-site support in v1 — both are explicitly deferred (E11, E12 in the Implementation Plan), trigger-based, not scheduled work.

## Constraints

Tech-stack, compliance, and selector-strategy constraints are enforced in `CLAUDE.md` and detailed in [System Architecture](03-system-architecture.md) — this PRD doesn't restate them, it defers to those as the living source. Any requirement above that conflicts with `CLAUDE.md`'s constraints is a bug in this PRD, not a license to violate the constraint.

## Acceptance criteria for "v1 done"

Tied to the Implementation Plan's E10 (hardening epic):
- `go test ./...` passes across all packages, without requiring AWS credentials or a deployed Lambda function.
- All three Lambda handler outcomes (success / hard error / `needs_agent_review`) are reachable on demand (local invocation of the extracted `run(...)` function) and match the documented JSON shape exactly.
- No code path ever constructs a disallowed URL (filtered query params, `/ajax/`, the internal API).
- The judgment round trip — shortlist → `internal/judge` calls the Claude API with `skill/judgment.md` → verdict recorded into the S3-backed price history — is demonstrated end-to-end at least once within a single handler invocation.

## Change control

If implementation reveals this PRD is wrong, incomplete, or a requirement needs to change, the fix is a doc PR updating this file (and any cross-linked docs) *before* code proceeds on the changed assumption — not a silent in-code decision. This is the mechanism for handling disagreement or new information without letting scope drift unnoticed across agent runs.
