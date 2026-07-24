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
Accepted — 2026-07-21. Superseded by ADR-007 (2026-07-22) — the storage *shape* (single JSON object, no database) is unchanged, but the backend moves from a local file to an S3 object.

### Context
Price history for a single target device (or a handful of target specs) is small, low-write-frequency data (one run every few hours) with one reader/writer (the CLI, invoked by a scheduler). A database would add an operational dependency disproportionate to the data volume and access pattern.

### Decision
We will store price history in a flat, append-only JSON file (`price-history.json`) using `encoding/json`, with no database. Writes must be atomic: write to a temp file, then `os.Rename` — never a direct in-place write.

### Consequences
Zero operational overhead (no server/service to run or back up beyond the file itself) and trivially inspectable/diffable history. Accepts that this doesn't scale to high write concurrency or large multi-user datasets — acceptable since this is explicitly a single-user, low-frequency tool (see PRD Users/Non-goals).

---

## ADR-004: OS-level scheduling driving a one-shot binary, not an in-process daemon

### Status
Accepted — 2026-07-21. Superseded by ADR-006 (2026-07-22) — no always-on machine is available to host Task Scheduler/cron, so scheduling moves to AWS EventBridge Scheduler + Lambda.

### Context
The pipeline runs infrequently (hours between runs) and has no need to hold state in memory between runs. An in-process scheduler (goroutine ticker, cron library) would require the binary to run continuously, adding a long-lived process to manage, restart on crash/reboot, and monitor.

### Decision
We will rely on OS-level scheduling (Windows Task Scheduler / cron) to invoke a one-shot `cmd/priceradar` binary on a schedule, rather than building an in-process daemon or scheduler loop.

### Consequences
Process lifecycle (start, crash recovery, reboot survival) is delegated to infrastructure the OS already provides, so the binary itself stays simple and stateless between invocations. Accepts that scheduling logic lives outside the repo (in Task Scheduler/cron config) rather than being inspectable as Go code.

---

## ADR-005: Three-way CLI exit-code contract (0/1/2)

### Status
Accepted — 2026-07-21. Superseded by ADR-008 (2026-07-22) — the three-way *distinction* (success / hard error / needs-agent-review) is unchanged, but it's carried by a Lambda handler's `(response, error)` return value instead of a process exit code, since there's no invoking script left to inspect an exit code.

### Context
The deterministic core can fail in two qualitatively different ways: an ordinary hard error (network failure, malformed config), or a specific, recoverable situation where the scraping mechanism itself may no longer match reality — the "Load more" selector isn't found. Collapsing both into a single non-zero exit code would force the invoking agent/scheduler to inspect output just to tell "something broke" from "something needs a judgment call about whether the site changed."

### Decision
We will use a three-way exit-code contract for `cmd/priceradar`: `0` success (JSON with `shortlist` and `since_last_run` on stdout), `1` hard error (`{error, code}` JSON on stderr), `2` `needs_agent_review` (structured JSON on stdout describing the selector miss, screenshot path, and DOM candidates) when `internal/browser` can't locate "Load more."

### Consequences
Callers (scheduler, wrapping agent) can branch on exit code alone without parsing output to distinguish "retry later" from "a human/agent needs to look at what the page looks like now." Adds a small amount of exit-code discipline that must be preserved anywhere `main()` is touched.

---

## ADR-006: AWS Lambda + EventBridge Scheduler, superseding OS-level scheduling

### Status
Accepted — 2026-07-22

### Context
ADR-004 assumed a machine that's on and reachable whenever the schedule fires (Windows Task Scheduler / cron). The maintainer doesn't have a machine kept on 24/7 for this, so that assumption doesn't hold in practice. The pipeline still runs infrequently (hours apart) and needs no in-memory state between runs, so a long-lived daemon is still not the right shape either. Separately, the maintainer wants AWS/cloud experience that's recognizable in the job market, which rules out a scheduler-as-code option like a CI cron job as the primary choice even though it would have been architecturally simpler (see the rejected GitHub Actions alternative below).

### Decision
We will run `cmd/priceradar` as an AWS Lambda function, invoked on a cron schedule by AWS EventBridge Scheduler, rather than relying on an OS-level scheduler driving a locally-running binary.

### Alternatives considered
- **GitHub Actions scheduled workflow:** would have preserved nearly the entire original architecture unchanged (flat-file storage via `git commit`, no S3/DynamoDB rewrite; trivial Chromium install via `apt-get` instead of a Lambda Layer; free). Rejected specifically because it doesn't build AWS/cloud skills the way Lambda does, which the maintainer prioritized over minimizing rework.
- **A small always-on EC2/Fargate instance running the OS-scheduler approach unchanged:** rejected as needlessly costly (paying for idle compute between infrequent runs) compared to Lambda's pay-per-invocation model.

### Consequences
Removes the "needs a machine kept on" constraint entirely — the scheduler and compute are both managed AWS services. In exchange, several downstream assumptions that depended on "a process is always available to be invoked live" no longer hold and had to be redesigned: price-history storage moves off local disk (ADR-007), the CLI exit-code contract becomes a Lambda handler return-value contract (ADR-008), the selector-miss hand-off becomes asynchronous (ADR-009), and running chromedp requires a headless-Chromium Lambda Layer plus more careful memory/timeout tuning than a local Chrome install did. This is materially more AWS surface area to build and operate (IAM, S3, SNS, EventBridge, Lambda Layers) than the original single-binary design, accepted deliberately for the job-market rationale above.

---

## ADR-007: S3 object storage for price history, superseding local flat file

### Status
Accepted — 2026-07-22

### Context
ADR-003 established a local flat JSON file with atomic temp-file-plus-rename writes, appropriate for a single always-resident process with a persistent local filesystem. Moving to Lambda (ADR-006) removes both assumptions: Lambda's filesystem is ephemeral scratch space, not shared or persisted across invocations, so `price-history.json` needs to live somewhere durable that survives between scheduled runs.

### Decision
We will store price history as a single JSON object (same `map[string][]model.Snapshot]` shape, same `encoding/json` marshal/unmarshal logic) in an S3 bucket, read via `GetObject` and written back in full via `PutObject`, rather than as a local file with `os.Rename`-based atomic writes. DynamoDB (item-per-snapshot modeling) was considered and rejected for now — it would require a real rewrite of `internal/store`'s data shape for a dataset that stays small and low-write-frequency (one Lambda invocation every few hours), where the simplicity of "same JSON blob, different I/O backend" outweighs DynamoDB's better concurrent-write and query characteristics.

### Consequences
`internal/store`'s marshal/unmarshal logic and the `model.Snapshot` shape are unchanged — only the I/O calls change from file read/write to S3 `GetObject`/`PutObject`. A single whole-object `PutObject` is already atomic from any reader's perspective, so the local temp-file-plus-rename mechanic has no S3 equivalent to build — it simply isn't needed. Accepts a read-modify-write race if two invocations ever overlapped (not a concern today: EventBridge Scheduler invokes this Lambda on one non-overlapping schedule, so there is exactly one writer at a time by construction). If concurrent writers or richer querying become a real need later, that's the trigger to revisit DynamoDB, not a reason to build it now.

---

## ADR-008: Lambda handler return-value contract, superseding process exit codes

### Status
Accepted — 2026-07-22

### Context
ADR-005 established a three-way process exit-code contract (0/1/2) for a CLI invoked by a scheduler or a wrapping agent that could inspect the exit code directly. A Lambda function has no process exit code in that sense — the runtime contract is a Go handler returning `(response, error)`, and AWS's own retry/failure-destination machinery reacts to whether that `error` is `nil`, not to a numeric code.

### Decision
We will preserve the same three-way *distinction* (success / hard error / needs-agent-review) but carry it through the Lambda handler's return value instead of a process exit code: success returns `(Response{...}, nil)`; a hard error returns `(nil, err)` so Lambda records an invocation failure (letting EventBridge's retry policy and any on-failure destination react); `needs_agent_review` returns `(Response{status: "needs_agent_review", ...}, nil)` — deliberately a *successful* invocation, not an error, because retrying immediately cannot fix a broken selector and letting Lambda's automatic retries fire on it would just repeat the same miss.

### Consequences
The extracted, testable `run(...)` function pattern from the original CLI design (T7.1) carries over directly — it just returns `(Response, error)` instead of an `int` exit code, so unit tests still don't need to spawn a subprocess or a real Lambda runtime to exercise all three outcomes. The `needs_agent_review`-is-not-an-error choice means Lambda/EventBridge's built-in failure alerting does *not* cover this case automatically — the handler itself must publish the SNS notification for it (see ADR-009), rather than relying on an on-failure destination to do so.

---

## ADR-009: Asynchronous selector-miss hand-off (S3 screenshot + notify, fix via commit + redeploy)

### Status
Accepted — 2026-07-22

### Context
The original selector-miss design (ADR-001, ADR-005, and the PRD's FR4) assumed a wrapping Agent already running the CLI live against this repo, able to read the screenshot and resume the same run once it determined the fix. Moving to a scheduled Lambda (ADR-006) removes that assumption: nothing is watching a given invocation in real time, so the screenshot can't be read and acted on within the same run no matter where it's written.

### Decision
On selector miss, the handler uploads the screenshot to S3 and publishes an SNS/email notification containing the screenshot URL and DOM candidates, then returns successfully (see ADR-008) rather than blocking or erroring. The fix is delivered as an ordinary, reviewed code change to `internal/browser` (new selector/strategy), committed and redeployed — not a runtime-read override value (e.g. an SSM parameter or `selector-override.json` the handler consults each invocation). The next scheduled invocation then uses the fixed code. A runtime-read override was considered and rejected: it would mean the "fix" bypasses normal code review/versioning, adding a second, less-visible path for changing scraping behavior alongside the real one.

### Consequences
Accepts a real, unavoidable gap: from the moment a selector breaks until someone notices the alert and ships a fix, no new price data is collected for that run. This is inherent to removing "an agent is always watching live" and cannot be fully closed without paying for continuous live monitoring, which is out of scope for a personal tool. In exchange, the fix path stays simple and auditable — it's a normal commit, reviewable and revertible like any other change, rather than a second, harder-to-track configuration surface.

---

## ADR-010: Judgment layer calls the Claude API directly from the Lambda handler at runtime

### Status
Accepted — 2026-07-22

### Context
The original design (PRD FR8, Solution Architecture §4, and the "why not call an LLM API from inside Go" reasoning) deliberately kept judgment external: an already-running wrapping Agent would invoke the CLI, read the shortlist JSON from stdout, apply `skill/judgment.md` itself, and call back into a `record-verdict` subcommand. That design assumed the same thing ADR-009's original counterpart assumed — an agent already present and running the CLI live. On a scheduled Lambda, that agent isn't there for a routine run; if judgment stayed external, no run would ever be autonomous end-to-end — every invocation would produce a shortlist and then simply wait for someone to act on it, which defeats the goal of an unattended, scheduled price checker.

### Decision
`internal/judge` will call the Claude API directly (via `net/http`, no SDK) from inside the Lambda handler, synchronously within the same invocation, passing it the shortlist, relevant price history, and `skill/judgment.md`'s instructions (embedded via `go:embed`). The verdict is recorded into the S3-backed price history and acted on by `internal/notify` within that same invocation — no external round trip, no separate `record-verdict` caller. This intentionally narrows the original "no LLM calls inside the core" stance: it now applies to `httpclient`/`browser`/`parser`/`prefilter`/`store` specifically, not to the judgment layer, which was always meant to be the one place non-deterministic reasoning happens — only *how* it's invoked changes (in-process API call vs. external agent).

Structure Verification (selector-miss, ADR-009) deliberately does **not** get the same treatment, and stays an async, human/agent-reviewed code change. The two hand-offs look similar on the surface (both are "the deterministic core hit something it can't decide") but differ in what kind of decision is being made: judgment is a bounded classification/scoring call over a small, well-defined input with a strict output contract, safe to automate and re-run every schedule; selector recovery means changing the actual scraping mechanism, which should go through the same review a code change normally would, not be auto-applied by an LLM call inside a running function.

### Consequences
The pipeline becomes autonomous end-to-end on a schedule, with no per-run human action required for the common case (which was a stated goal once there's no always-present agent to lean on). Adds a runtime dependency on Claude API availability and a secret to manage (the API key, via an SSM Parameter Store `SecureString`, read by `internal/judge`) — a hard Claude API failure during a run should be treated as this run's hard error path (ADR-008), not silently skipped. Also adds a small, ongoing API cost proportional to how often the shortlist is non-empty (bounded by the prefilter's 0–5-candidate output, per the original design's cost-proportionality goal). `skill/judgment.md` remains a plain file read directly from the repo (now embedded at build time rather than read from disk at invocation time), preserving the "instructions are inspectable/editable text, not a hardcoded prompt" property from the original design.

---

## ADR-011: On-demand local invocation as a first-class trigger mode, pre-deployment

### Status
Accepted — 2026-07-23

### Context
ADR-006 established AWS Lambda + EventBridge Scheduler as the production trigger, and ADR-008 carried the three-way outcome contract (success / hard error / `needs_agent_review`) through the Lambda handler's `(response, error)` return value. Both assume the Lambda is deployed. Before deployment, the only way to exercise the real pipeline has been to invoke the extracted `run(ctx, cfg)` function directly — which the original PRD framed purely as a test/dev affordance ("all three outcomes are reachable on demand [local invocation of the extracted `run(...)` function]"), not as a documented product trigger. In practice, an agent or the maintainer working in this repo wants to trigger a real, complete check *right now*, without waiting for E10.3 (Lambda packaging/deployment) to land — e.g. to validate a code change against the live site, or just to get a fresh price check, before any AWS infrastructure exists. The already-planned MCP extension (E11) would eventually offer an on-demand path too, but E11 is explicitly deferred and, per this same round of decisions, is now deferred specifically until *after* the Lambda is deployed (see the E11 update in the Implementation Plan) — it is not a near-term option.

A related question this raises: `run(ctx, cfg)` calls S3 (`internal/store`), SNS (`internal/notify`), and the Claude API (`internal/judge`). Requiring real AWS infrastructure (a provisioned S3 bucket, an SNS topic) before this on-demand trigger could be used at all would tie a dev-stage convenience to production infra provisioning, which is disproportionate this early.

### Decision
We will promote local invocation of `run(ctx, cfg)` from a test/dev-only affordance to a first-class, documented on-demand trigger mode. `cmd/priceradar/main()` will detect its runtime environment via the `AWS_LAMBDA_RUNTIME_API` environment variable (set automatically by the Lambda runtime, absent in a normal local/dev shell): if present, it calls `lambda.Start(handler)` as today; if absent, it calls `run(ctx, cfg)` once, marshals the returned `Response` to stdout and exits `0` on `(Response, nil)` (covering both success and `needs_agent_review`), or writes the error to stderr and exits `1` on `(nil, err)`. This reuses ADR-008's exact three-way outcome contract without modification — the on-demand path is a different *surface* for the same contract, not a new one.

To keep this usable before any AWS infrastructure is provisioned, `internal/store` and `internal/notify` select their backend/channel purely by whether `config.json`'s S3/SNS fields are populated, not by which trigger invoked the run: no S3 bucket configured → `internal/store` falls back to a local JSON file; no SNS topic ARN configured → `internal/notify` skips the SNS publish and relies on its log-line channel alone. `needs_agent_review`'s screenshot-upload + notify behavior is not given a separate code path for local runs — `internal/browser` never branches on trigger mode, only `internal/notify`/`internal/store` branch on config presence, keeping the mechanism identical regardless of who's watching.

This is explicitly a two-phase story: the on-demand local trigger is the near-term (pre-deployment) on-demand path; once the Lambda is deployed (E10.3), the MCP extension (E11) becomes the intended production-facing on-demand/interactive trigger, per the updated E11 deferred-until condition. The local trigger does not go away once MCP exists — it remains a valid, simpler on-demand path for direct dev-stage use — but MCP is the one meant to carry that role going forward in production use.

### Consequences
A real, complete pipeline run (all three outcomes) is reachable today without any AWS deployment or provisioned infrastructure — useful both for an agent iterating on this repo and for the maintainer wanting an immediate check. Once real S3/SNS are configured (whether pre- or post-Lambda-deployment), the on-demand path uses them via the standard local AWS credential chain rather than an IAM execution role. Introducing a second invocation surface for the same core also means `internal/store`'s single-writer assumption (ADR-007) no longer holds by construction once real S3 is in use, since an on-demand run can now overlap a scheduled Lambda invocation — see ADR-012 for the resulting store change this decision requires. The local-file fallback mode has no such concurrency exposure, since it implies a single machine and process by construction.

---

## ADR-012: S3 conditional writes for concurrent-invocation safety, superseding ADR-007's single-writer assumption

### Status
Accepted — 2026-07-23. Supersedes the concurrency assumption in ADR-007 (the storage *backend* — a single JSON object in S3, `GetObject`/`PutObject` — is unchanged; only the write safety mechanism changes).

### Context
ADR-007 accepted a bare read-modify-write race in `internal/store` on the grounds that "EventBridge Scheduler invokes this Lambda on a single non-overlapping schedule — there is exactly one writer at a time by construction." ADR-011 removes that construction once real S3 is in use: an on-demand local trigger can now run at any time, including concurrently with (or interleaved with) a scheduled Lambda invocation, and later an MCP-triggered run (E11) could add a third overlapping writer. A bare `GetObject`-then-`PutObject` sequence under real overlap can silently drop one writer's appended snapshot (last write wins, whole object). This is a personal, single-user tool with at most two or three writers in practice, not a high-concurrency distributed system — so the fix needs to close the race without introducing infrastructure (e.g. a DynamoDB locking table) disproportionate to that scale.

### Decision
`internal/store`'s S3-backed write path will use S3's conditional-write support instead of a bare `PutObject`: `GetObject` the current history and capture its `ETag`; apply the in-memory append; `PutObject` with `If-Match: <etag>` (or `If-None-Match: "*"` if no object existed yet, to guard the first-ever-write race). If S3 rejects the write with `412 Precondition Failed`, retry from the `GetObject` step — re-reading the now-current object, re-applying the append on top of it, and re-attempting the conditional `PutObject` — up to a fixed bound (e.g. 5 attempts) with short exponential backoff between attempts (e.g. 100ms, 200ms, 400ms, ...). If retries are exhausted, the write returns an error rather than silently overwriting or dropping the snapshot; this surfaces through the existing hard-error path (`(nil, err)` / exit `1`, ADR-008) rather than a new failure mode. This uses only `aws-sdk-go-v2`'s existing S3 `PutObjectInput.IfMatch`/`IfNoneMatch` fields — no new dependency, no DynamoDB or external locking table, consistent with ADR-007's original rejection of DynamoDB for this dataset's size/frequency. The local-file fallback backend (ADR-011) has no equivalent mechanism, since it's only used pre-infrastructure, single-machine, single-process.

### Consequences
Two overlapping writers (e.g. an on-demand local run and a scheduled Lambda invocation, both configured against real S3) can no longer silently clobber each other's appended snapshot — the losing writer detects the conflict via `412` and retries against the updated object, so both writers' data ends up recorded (possibly requiring the retrying writer to re-derive its append against a newer base, which is safe since appends are additive, not positional). This adds a small amount of latency in the (rare, at this scale) conflict case and a small amount of code complexity to `internal/store` (a retry loop instead of a single call), but avoids both the correctness gap of ADR-007's now-invalid assumption and the operational overweight of a dedicated locking service. If write contention ever grows beyond "occasionally two or three writers overlap" (e.g. many concurrent MCP-triggered writers), that would be the trigger to revisit a queue or a proper locking table — not a reason to build one now.
