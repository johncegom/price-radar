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
| FR4 | On selector miss, capture a screenshot and hand off to agent review rather than guessing or returning a silent partial list | E4 |
| FR5 | Deterministically narrow the full listing to a short list (0–5 candidates) of plausible matches for the target device, biased toward recall over precision | E5 |
| FR6 | Persist every observation (price, discount, stock) to an append-only, timestamped history log per product URL, with atomic writes | E6 |
| FR7 | Expose the pipeline as a one-shot CLI with a three-way exit contract: success (0, shortlist JSON), hard error (1, `{error,code}` JSON), needs-agent-review (2, structured JSON) | E7 |
| FR8 | Let the judgment layer (an AI agent reading `skill/judgment.md` + the shortlist) decide match and notify-worthiness, including explicitly returning "no match" rather than forcing a pick, and record that verdict back through the CLI (`record-verdict`) rather than direct file edits | E7, E8 |
| FR9 | Notify (log line, webhook, or chat message — channel decoupled from the decision) only when the judgment layer's verdict says so | E9 |

## Non-goals (v1)

These are hard boundaries, not soft defaults — an agent must not silently cross them without a change-control step (see below):

- No scraping of other retailers or categories — one site, one page, one target spec.
- No login, checkout, or purchase automation.
- No filtered/query-parameter scraping and no reliance on the site's undocumented internal API (`robots.txt`-disallowed).
- No continuous/real-time polling — scheduled, low-frequency checks only.
- No third-party HTTP framework, no third-party HTML parser by default, no LLM calls inside the deterministic core (`chromedp` is the one sanctioned dependency, scoped to `internal/browser`).
- No MCP extension or second-site support in v1 — both are explicitly deferred (E11, E12 in the Implementation Plan), trigger-based, not scheduled work.

## Constraints

Tech-stack, compliance, and selector-strategy constraints are enforced in `CLAUDE.md` and detailed in [System Architecture](03-system-architecture.md) — this PRD doesn't restate them, it defers to those as the living source. Any requirement above that conflicts with `CLAUDE.md`'s constraints is a bug in this PRD, not a license to violate the constraint.

## Acceptance criteria for "v1 done"

Tied to the Implementation Plan's E10 (hardening epic):
- `go test ./...` passes across all packages.
- All three CLI exit codes (0/1/2) are reachable on demand and match the documented JSON shape exactly.
- No code path ever constructs a disallowed URL (filtered query params, `/ajax/`, the internal API).
- The judgment round trip — CLI shortlist JSON → `skill/judgment.md` applied → `record-verdict` → `price-history.json` updated — is demonstrated end-to-end at least once.

## Change control

If implementation reveals this PRD is wrong, incomplete, or a requirement needs to change, the fix is a doc PR updating this file (and any cross-linked docs) *before* code proceeds on the changed assumption — not a silent in-code decision. This is the mechanism for handling disagreement or new information without letting scope drift unnoticed across agent runs.
