# PriceRadar — Architecture Decision Records

This is the *why/history* log, kept separate from the PRD and architecture docs, which describe *current state* only. Each entry follows the standard Nygard ADR template (Status, Context, Decision, Consequences) and is never rewritten after acceptance — if a decision changes, add a new entry and mark the old one superseded.

**Process:** when a human and an agent agree to change an established architectural decision, do both of the following, not just one:
1. Add a new ADR entry here (or mark the affected entry `Superseded by ADR-NNN`).
2. Update the relevant section of [PRD](00-prd.md) / [Solution Architecture](02-solution-architecture.md) / [System Architecture](03-system-architecture.md) so it still reflects current truth.

This file is the audit trail of *why* the design is what it is; the PRD and architecture docs are what agents check against day-to-day and must never be left out of sync with an accepted decision.

---

## ADR-001: Use chromedp, scoped to internal/browser, for "Load more"

### Status
Accepted — 2026-07-21

### Context
The listing page has no URL-based pagination: `?trang=`, `?page=`, and `?p=` were all verified to return the identical first batch, so fetching further pages requires driving the page's own "Load more" control. The site's internal API (`papi.fptshop.com.vn`) and `/ajax/`-path scraping are both off-limits under `robots.txt` and the project's compliance posture, so bypassing the UI to reach data directly isn't an option. Something has to drive real browser interaction for the full ~650-item catalog.

### Decision
We will use `chromedp` as the one sanctioned third-party dependency, scoped narrowly to `internal/browser`, solely to click the "Load more" control repeatedly against the same permitted listing URL. We will not use `go-rod`, `playwright-go`, or any other browser-automation library, and we will not use the headless browser to reach any URL or endpoint that plain HTTP fetching couldn't already legitimately reach.

### Consequences
Full-catalog fetching becomes possible without touching disallowed endpoints, at the cost of a heavier runtime dependency (a real browser) confined to one package. The rest of the pipeline (`parser`, `prefilter`, `store`, judgment) stays browser-agnostic. If the site ever adds real pagination, `internal/browser` can be dropped without touching any other package.

---

## ADR-002: regexp (stdlib) for HTML parsing, x/net/html as flagged fallback only

### Status
Accepted — 2026-07-21

### Context
Parsing product cards out of the listing HTML needs to be fast, dependency-light, and good enough for one known, relatively stable page structure. A full HTML parser (`golang.org/x/net/html` or similar) is more robust to malformed markup but adds a dependency and more code for a job regex can plausibly do on a single well-understood template.

### Decision
We will parse product cards with `regexp` (stdlib) by default. `golang.org/x/net/html` is an explicitly-flagged fallback only, to be adopted if regex parsing proves too fragile in practice — not a default or parallel choice.

### Consequences
Keeps the parser dependency-free and simple for the common case. Accepts the known fragility of regex-based HTML parsing (sensitive to markup changes) as a tradeoff, mitigated by FR2's requirement that individual malformed cards fail without aborting the run.

---

## ADR-003: Flat JSON file with atomic writes, no database

### Status
Accepted — 2026-07-21

### Context
Price history for a single target device (or a handful of target specs) is small, low-write-frequency data (one run every few hours) with one reader/writer (the CLI, invoked by a scheduler). A database would add an operational dependency disproportionate to the data volume and access pattern.

### Decision
We will store price history in a flat, append-only JSON file (`price-history.json`) using `encoding/json`, with no database. Writes must be atomic: write to a temp file, then `os.Rename` — never a direct in-place write.

### Consequences
Zero operational overhead (no server/service to run or back up beyond the file itself) and trivially inspectable/diffable history. Accepts that this doesn't scale to high write concurrency or large multi-user datasets — acceptable since this is explicitly a single-user, low-frequency tool (see PRD Users/Non-goals).

---

## ADR-004: OS-level scheduling driving a one-shot binary, not an in-process daemon

### Status
Accepted — 2026-07-21

### Context
The pipeline runs infrequently (hours between runs) and has no need to hold state in memory between runs. An in-process scheduler (goroutine ticker, cron library) would require the binary to run continuously, adding a long-lived process to manage, restart on crash/reboot, and monitor.

### Decision
We will rely on OS-level scheduling (Windows Task Scheduler / cron) to invoke a one-shot `cmd/priceradar` binary on a schedule, rather than building an in-process daemon or scheduler loop.

### Consequences
Process lifecycle (start, crash recovery, reboot survival) is delegated to infrastructure the OS already provides, so the binary itself stays simple and stateless between invocations. Accepts that scheduling logic lives outside the repo (in Task Scheduler/cron config) rather than being inspectable as Go code.

---

## ADR-005: Three-way CLI exit-code contract (0/1/2)

### Status
Accepted — 2026-07-21

### Context
The deterministic core can fail in two qualitatively different ways: an ordinary hard error (network failure, malformed config), or a specific, recoverable situation where the scraping mechanism itself may no longer match reality — the "Load more" selector isn't found. Collapsing both into a single non-zero exit code would force the invoking agent/scheduler to inspect output just to tell "something broke" from "something needs a judgment call about whether the site changed."

### Decision
We will use a three-way exit-code contract for `cmd/priceradar`: `0` success (JSON with `shortlist` and `since_last_run` on stdout), `1` hard error (`{error, code}` JSON on stderr), `2` `needs_agent_review` (structured JSON on stdout describing the selector miss, screenshot path, and DOM candidates) when `internal/browser` can't locate "Load more."

### Consequences
Callers (scheduler, wrapping agent) can branch on exit code alone without parsing output to distinguish "retry later" from "a human/agent needs to look at what the page looks like now." Adds a small amount of exit-code discipline that must be preserved anywhere `main()` is touched.
