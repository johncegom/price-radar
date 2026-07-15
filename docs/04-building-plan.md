# Go through PriceRadar

Outcome: independently design and build small, dependency-light Go network/CLI tools — HTTP clients without a framework, stdlib-first parsing and storage, and judgment-boundary design (what a program decides vs. what it hands off).
Project: PriceRadar — a Go device-price checker for one retailer page today ([fptshop.com.vn/may-doi-tra](https://fptshop.com.vn/may-doi-tra)), growing from a raw HTTP fetch into a scheduled, prefiltered, agent-assisted pipeline, structured so a second site can plug in later without touching the core (see [System Architecture § Future Extensibility](03-system-architecture.md#future-extensibility-where-a-second-site-would-plug-in-not-built-yet)).
Practice mix: ~80% cumulative PriceRadar project / ~20% isolated experiments (flagged per phase below)
Pace: flexible weekly targets, tracked by checkpoint evidence, not a calendar

This plan assumes prior general software-engineering experience (per learner profile) — Go-specific semantics (interfaces, goroutines, error handling idioms, `context`) are taught explicitly at the point the project needs them; generic engineering concepts are not retaught.

---

## Phase 0 — Environment smoke test

Weekly outcome: confirm Go toolchain, module init, and basic run/build work on this machine.
Evidence of completion: `go run .` prints something; `go build` produces a binary.
Concepts introduced: `go mod init`, module vs. package, `go run` vs `go build`.

**Checkpoint 0.1 — Hello, module**
Why now: nothing else can be built until the module and toolchain are confirmed working.
Change: `go mod init priceradar`, a `main.go` with a `func main()` printing a fixed string.
Run: `go run .`
Expected: the string prints; no toolchain errors.
New idea: a Go module is the dependency/versioning boundary; a package is the compilation unit — plain-language mapping, no theory beyond what's needed to proceed.
Break it: rename the package declaration to something other than `main` and try `go run .` — observe the exact error.
Transfer: fix it back and explain in one sentence why `main` is special.
Done when: `go run .` succeeds and you can state why `package main` + `func main()` matters.

---

## Phase 1 — Smallest end-to-end vertical slice: raw fetch

Weekly outcome: fetch the real listing page over HTTPS and confirm bytes come back.
Evidence of completion: program prints HTTP status code and response byte length for `https://fptshop.com.vn/may-doi-tra`.
Concepts introduced: `net/http.Get` (temporary — replaced in Phase 4), `io.ReadAll`, `defer resp.Body.Close()`, basic Go error handling (`if err != nil`).

**Checkpoint 1.1 — Fetch and measure**
Why now: this is the smallest possible working slice — no value in parsing before the fetch itself is proven reliable.
Change: `main.go` calls `http.Get(url)`, checks `err`, reads and measures the body.
Run: `go run .`
Expected: prints something like `status=200 bytes=87342`.
New idea: Go error handling is explicit, not exceptions — every fallible call returns `(value, error)` and the caller decides. Metaphor: a delivery receipt you must check before assuming the package arrived; nothing throws it in your face automatically.
Visual model: none yet — single linear call, no interacting components.
Break it: point the URL at a nonexistent path (typo the domain) and observe what `err` looks like versus a real HTTP error status (e.g. 404) — these are two different failure shapes in Go's `net/http`.
Transfer: write a one-line explanation of why a non-2xx status is *not* a Go `error` from `http.Get`'s perspective, and add the status check yourself without copying.
Done when: you can explain, from memory, the difference between "the request failed" and "the request succeeded but returned an error status."

---

## Phase 2 — Parse one product card

Weekly outcome: extract a single product's name and price from the fetched HTML using `regexp`.
Evidence of completion: program prints one real product name + price parsed out of the actual page.
Concepts introduced: `regexp.MustCompile`, capture groups, `FindStringSubmatch`, why a naive regex approach is chosen here deliberately (see [System Architecture](03-system-architecture.md)) instead of a full HTML parser.

**Checkpoint 2.1 — First extraction**
Why now: proves the parsing strategy against real markup before designing the full product model.
Change: a `parseOneProduct(html string) (name, price string, ok bool)` function using one hand-written regex.
Run: `go run .` against the fetched body from Phase 1.
Expected: prints a real product name and price from the current listing.
New idea: capture groups as "named holes" in a pattern — map each `()` in the regex to its corresponding matched substring.
Break it: change the regex to expect a slightly different price format (e.g. assume no thousands separator) and observe the match silently fail or extract garbage.
Transfer: fix the regex yourself and explain why silent wrong-extraction is more dangerous here than an outright crash.
Done when: extraction works against live data and you can explain one way this regex could break if FPT Shop changes its markup.

---

## Phase 3 — Full product model + fault-isolated parsing

Weekly outcome: parse the *entire* listing into a slice of structured records, tolerating individual bad cards.
Evidence of completion: program prints a count of successfully parsed products (should be dozens+) and zero panics even if some cards fail.
Concepts introduced: `struct` definition (`model.Product`), slices of structs, `time.Time` for `FetchedAt`, basic table-driven tests with the `testing` package.

**Checkpoint 3.1 — `model.Product` struct**
Why now: a single-card function doesn't scale to a real listing; need a data shape before storing or filtering.
Change: define `Product{Name, URL, Price, OriginalPrice, DiscountPct string/int, InStock bool, FetchedAt time.Time}` in `internal/model`.
Run: n/a (compiles).
Expected: package compiles.
New idea: Go structs as plain data containers — no inheritance, composition over hierarchy (a stdlib-idiom point worth stating explicitly for a learner from OOP-heavier languages).
Done when: struct compiles and fields match what Phase 2's regex actually extracts.

**Checkpoint 3.2 — Parse-all with fault isolation**
Why now: production input has irregular cards; the parser must not die on the first bad one.
Change: loop over per-card matches; wrap each card's parse in its own error handling; log-and-skip on failure instead of returning early.
Run: `go run .` against the full page.
Expected: prints "`parsed N of M candidate cards`" with N close to M.
New idea: fault isolation as a design stance, not a language feature — the loop body decides to continue, Go doesn't do this for you.
Break it: deliberately corrupt one card's HTML in a saved test fixture and confirm the rest still parse.
Transfer: add your own first table-driven test (`testing` package) covering one well-formed and one malformed card.
Done when: a saved HTML fixture with one broken card still yields all the good ones, backed by a passing test.

---

## Phase 4 — Real HTTP client: UA header + retry/backoff (no framework)

Weekly outcome: replace the throwaway `http.Get` with a purpose-built client used everywhere.
Evidence of completion: a forced 429/5xx (or a local test server simulating one) triggers visible retries with increasing delay, then either succeeds or gives up cleanly.
Concepts introduced: `http.RoundTripper` interface, composing behavior via wrapping (not inheritance), `context.WithTimeout`, exponential backoff as a plain loop.

**Checkpoint 4.1 — Custom `RoundTripper` for the User-Agent**
Why now: this is the idiomatic stdlib substitute for "framework middleware" — the project's explicit no-framework constraint makes this the load-bearing Go concept of the phase.
Change: `internal/httpclient` defines a type implementing `RoundTripper` that wraps `http.DefaultTransport`, sets the UA header, then delegates.
Run: point requests at a local echo server (`httptest.NewServer`) and print the UA header the server received.
Expected: server logs show the injected UA on every request.
New idea: interfaces in Go are satisfied implicitly — metaphor: a socket adapter, any plug shaped right fits, no explicit "implements" declaration needed. Map this back to why `RoundTripper` composition works without inheritance.
Visual model: sequence diagram — request → custom RoundTripper → `http.DefaultTransport` → server → response, to make the wrapping/delegation order concrete before backoff logic sits on top of it.
Break it: forget to call the wrapped transport's `RoundTrip` and observe the request never actually leaves the process.
Transfer: add a second header (e.g. `Accept-Language`) to the same wrapper without copying the first one verbatim.
Done when: `httptest` server confirms both headers land correctly, explained from memory.

**Checkpoint 4.2 — Retry with backoff**
Why now: real-world 429/5xx handling is the next load-bearing brick before this client is trustworthy against a live site.
Change: a retry loop around `client.Do`, exponential delay with a cap, using `context.WithTimeout` to bound total time.
Run: `httptest` server programmed to return 429 twice then 200.
Expected: log shows two backoff waits, then a successful response.
Break it: make the test server return 429 indefinitely and confirm the loop gives up after the configured max retries instead of hanging forever.
Transfer: explain why `context.WithTimeout` is the safety net even if the retry-count logic has a bug.
Done when: both the "eventually succeeds" and "gives up cleanly" paths are demonstrated against the test server.

*(Disposable experiment — the ~20% slice: build a tiny standalone program that deliberately triggers each `net/http` error class — DNS failure, connection refused, timeout, non-2xx — against `httptest` servers, just to see each error's exact shape in isolation before trusting the retry loop's assumptions.)*

---

## Phase 5 — Price history storage

Weekly outcome: persist parsed snapshots to disk, append-only, surviving process restarts.
Evidence of completion: running the program twice in a row produces a JSON file with two timestamped entries per product URL.
Concepts introduced: `encoding/json` struct tags, `os.WriteFile` vs. temp-file-then-`os.Rename` for atomic writes, map-of-slices as a JSON shape.

**Checkpoint 5.1 — Read-modify-write**
Why now: without persistence there's no "history" — this is the first checkpoint where the project outlives one run.
Change: `internal/store` reads existing `price-history.json` (or starts empty), appends new `Snapshot{Timestamp, Price, DiscountPct, InStock}` per product URL, writes back.
Run: `go run .` twice.
Expected: file shows 2 entries for the same URL after the second run.
New idea: atomic file replace (write temp file, `os.Rename`) as protection against a crash mid-write — metaphor: writing a new draft fully before swapping it for the original, rather than editing the original in place.
Break it: kill the process (Ctrl+C) mid-write with the naive direct-write approach first, show a truncated/corrupt file, then switch to the temp-file+rename approach and show it can't happen.
Transfer: explain why this matters more for a long-running log than for a one-shot overwrite.
Done when: the corrupt-file failure is reproduced once, then proven fixed.

---

## Phase 6 — Deterministic prefilter

Weekly outcome: given the target spec string and the full parsed listing, output a short list of plausible matches with scores.
Evidence of completion: running against the live site with target "MacBook Pro M2 Pro 32GB/512GB" prints 0–5 candidates with visible token scores, not all ~650 listings.
Concepts introduced: string normalization (`strings.ToLower`, `strings.Fields`), sets via `map[string]struct{}`, designing for recall vs. precision as an explicit tradeoff.

**Checkpoint 6.1 — Token overlap score**
Why now: this is the deterministic core's most "judgment-shaped" piece — worth its own checkpoint before wiring in any agent layer.
Change: `internal/prefilter` tokenizes target + each product name, computes matched/total ratio, hard-excludes obvious category mismatches.
Run: `go run .` against the live listing.
Expected: shortlist output includes score + matched/missing tokens per candidate.
New idea: recall-over-precision as a deliberate design choice — cheap filters should never be the last line of defense against a false negative when a smarter layer sits downstream.
Visual model: data-flow diagram — full listing → hard-exclude → soft-include threshold → shortlist, to make the two-tier filter concrete (see [Solution Architecture § Deterministic Prefilter](02-solution-architecture.md#3-deterministic-prefilter)).
Break it: set the soft-include threshold too high and show the real target device gets dropped from the shortlist; then correct it.
Transfer: add one more hard-exclude category on your own (e.g. "ipad") without being shown the pattern twice.
Done when: the real target device reliably appears in the shortlist across a few live runs, and you can state in one sentence why the threshold is biased toward recall.

---

## Phase 6.5 — Full catalog via headless browser + Agent hand-off

Weekly outcome: fetch the *entire* ~650-item catalog (not just the ~30-40 item first batch) by driving the page's own "Load more" control, and hand off to the wrapping Agent instead of guessing when that control can't be found.
Evidence of completion: a run against the live site reports a product count close to the full catalog size, not just the first batch; deliberately breaking the selector produces a screenshot on disk and exit code 2 instead of a crash or a silent partial result.
Concepts introduced: driving an external library (`chromedp`) as a scoped, deliberate exception to stdlib-only; XPath/text-content selectors vs. brittle CSS-class selectors; a three-way process exit contract (success / hard error / needs-agent-review) instead of a boolean one.

Why this phase exists at all: earlier investigation (see [Project Description § Compliance posture](01-project-description.md#compliance-posture)) found there's no URL-based pagination on this listing, the filtered/query-param URLs are `robots.txt`-disallowed, and the site's internal API is undocumented — so a headless browser driving the *same permitted URL* a real user would use is the only compliant, full-coverage option.

**Checkpoint 6.5.1 — Click "Load more" until exhausted**
Why now: the prefilter from Phase 6 only does its job meaningfully against the full listing — a partial catalog undermines the recall-over-precision design it was built on.
Change: `internal/browser` opens one chromedp context against the listing URL, loops: locate the control by its **visible text** ("Xem thêm"), click, wait for new cards, repeat until the control disappears.
Run: `go run .` against the live site.
Expected: parsed product count is close to the full catalog (~650), not the ~30-40 of the first batch.
New idea: selecting by what a human reads (text content), not by implementation-detail styling (Tailwind utility classes, which regenerate on every frontend rebuild) — the more stable signal isn't always the one that looks like a normal CSS selector.
Break it: point the selector at one of the button's Tailwind classes instead of its text, then have the site (or a saved fixture) change classes on a rebuild — observe the click silently fail to find anything.
Transfer: revert to the text-based selector yourself and explain in one sentence why it's the more durable choice here.
Done when: a full-catalog run completes and you can state why URL pagination wasn't an option (cite the empirical test from Phase 6.5's intro).

**Checkpoint 6.5.2 — Screenshot + exit-code hand-off on selector miss**
Why now: "the site changed and broke our selector" is exactly the kind of ambiguous, non-deterministic judgment call this project already hands to an Agent rather than hardcoding around (same principle as Phase 8's matching judgment) — but this one is about the *scraping mechanism*, not the *product*.
Change: when the text-based selector in 6.5.1 finds nothing, capture a full-page screenshot via chromedp (the browser context is already open, so this is nearly free), write it to `internal/tmp/`, and exit `2` with a `needs_agent_review` JSON payload (see [System Architecture § CLI contract](03-system-architecture.md#cmdpriceradar-one-shot-cli)) instead of panicking or returning an empty list.
Run: temporarily break the selector on purpose, run the binary, confirm exit code 2 and a real screenshot file.
Expected: process exits 2; `internal/tmp/` contains a screenshot; stdout has `status`, `reason`, and `screenshot_path` fields.
New idea: a three-way exit contract (0/1/2) as a way to make "I genuinely can't decide this" a first-class, structured outcome — distinct from both success and hard failure — so the layer that *can* decide (the wrapping Agent, already running this CLI against the repo) knows exactly when and how to step in.
Break it: delete the screenshot file after it's written but before the Agent would read it, and observe what the Agent actually does with a `needs_agent_review` result it can't act on — this is the failure mode worth understanding before relying on the contract.
Transfer: explain, from memory, why this hand-off writes files and an exit code rather than the Go program calling an LLM API directly.
Done when: the selector-miss path is demonstrated end-to-end (break it → screenshot appears → exit 2 → fix it → normal run resumes), and you can explain why this is a *mechanical* judgment call, not the same kind as Phase 8's product-matching judgment.

*(Deferred, not built now: a formalized MCP tool version of this hand-off — `verify_page_structure` — once [Phase 9's MCP extension](#phase-9--mcp-extension-optional) exists. The exit-code contract already works; building the MCP tool now would front-run work that's itself optional and deferred.)*

---

## Phase 7 — One-shot CLI wiring

Weekly outcome: a single `priceradar` binary that runs config → fetch (incl. full-catalog browser pass) → parse → prefilter → store → JSON stdout, ready for a scheduler (or a wrapping Agent) to call.
Evidence of completion: `priceradar` run from a fresh shell (not `go run .`) produces valid JSON on stdout on success, `{error, code}` + non-zero exit on a forced hard failure, and exit code 2 + `needs_agent_review` JSON on a forced selector miss.
Concepts introduced: `flag` or manual `os.Args` parsing (no CLI framework), `encoding/json.Marshal` for stdout contracts, exit codes via `os.Exit`.

**Checkpoint 7.1 — Config-driven run**
Why now: hardcoded target specs don't scale past one device; this is also the natural point to freeze the CLI's I/O contract (all three exit codes) before anything downstream (agent, scheduler) depends on it.
Change: `config.json` holds target spec(s) + listing URL + thresholds; `main.go` loads it, wires the phases 1–6.5 pipeline together, and prints one JSON object to stdout.
Run: `./priceradar` (built binary) with a real `config.json`.
Expected: valid JSON with `shortlist` and `since_last_run` fields.
Break it: point `config.json` at a malformed JSON file and confirm the program exits non-zero with a `{error, code}` JSON on stderr, not a raw Go panic/stack trace.
Transfer: add a second target device to `config.json` and confirm the pipeline handles both without code changes.
Done when: the binary behaves correctly standalone (no `go run`), and a scheduler could invoke it blindly based on exit code + stdout/stderr contract alone.

*(Visual checkpoint — system-context view: draw the whole pipeline from [Solution Architecture](02-solution-architecture.md) from memory at this point, labeling exactly which boxes are Go and which are the (not-yet-built) agent layer. This is the redraw exercise for the mega-diagram introduced at the start of the project.)*

---

## Phase 8 — Agent judgment integration point

Weekly outcome: the CLI's JSON output is consumable by an AI agent that applies `skill/judgment.md` and returns a match/notify verdict.
Evidence of completion: manually feeding a real shortlist JSON to an agent session (with the judgment instructions loaded) produces a sensible verdict, including on a deliberately ambiguous case (e.g. wrong storage size).
Concepts introduced: none new in Go — this phase is about the *contract* between deterministic output and agent input, reinforcing the judgment-boundary design from [Solution Architecture](02-solution-architecture.md#why-this-shape-not-simpler).

**Checkpoint 8.1 — Judgment instructions file**
Why now: the boundary between "Go decides" and "agent decides" needs to be written down before it can be tested.
Change: draft `skill/judgment.md` with matching rules, notify rules, and an explicit output contract (matched URL or none, confidence, one-line reason).
Run: manually paste a real shortlist + this file into an agent session.
Expected: agent produces a verdict matching the output contract, and gets the ambiguous case right (or wrong in an explainable way).
Break it: feed the agent a shortlist where the true match is *absent* (simulate a prefilter false negative) and see whether it correctly reports "no match" rather than forcing a bad pick onto the nearest candidate.
Transfer: revise the judgment instructions yourself after seeing one failure mode, without being handed the fix.
Done when: the judgment file reliably produces correct verdicts across a small set of hand-constructed shortlists, including at least one "no match" case.

---

## Phase 9 — MCP extension (optional)

Weekly outcome: the same deterministic core is reachable as MCP tools (`fetch_listings`, `get_price_history`, `get_target_config`) in addition to the one-shot CLI.
Evidence of completion: an MCP-capable agent host calls `fetch_listings` interactively and gets the same shortlist shape the CLI produces on stdout.
Concepts introduced: MCP tool/resource definitions as a thin adapter layer, stdio transport, why this is additive rather than a rewrite (see [System Architecture § MCP Extension](03-system-architecture.md#6-mcp-extension-optional)).

**Checkpoint 9.1 — Thin MCP adapter**
Why now: only worth building once the CLI contract (Phase 7) and judgment boundary (Phase 8) are both stable — MCP should wrap a known-good core, not a moving target.
Change: `cmd/priceradar-mcp/main.go` exposes `internal/*` packages as MCP tool handlers; no changes to `internal/*` itself.
Run: connect an MCP host to the stdio server, invoke `fetch_listings`.
Expected: response shape matches the CLI's stdout JSON.
Done when: you can explain, from memory, why `internal/*` needed zero changes to support this — i.e., the adapter boundary actually held.

---

## Phase 10 — Second site extension (not started — trigger-based, not scheduled)

Weekly outcome: n/a until a real second site is actually needed — this phase exists only as a placeholder so the extensibility intent from [Solution Architecture](02-solution-architecture.md#future-extensibility-not-built-yet) and [System Architecture](03-system-architecture.md#future-extensibility-where-a-second-site-would-plug-in-not-built-yet) has a home in the roadmap rather than being forgotten.
Trigger to start: a genuine second site to track, not "it would be nice to be ready."
Shape when triggered: extract `internal/httpclient` + `internal/parser` behind a `siteplugin` interface (`Fetch(target) -> []model.Product`), move FPT Shop's current logic into `internal/sites/fptshop`, add the new site alongside it. Prefilter, judgment, store, and notify are expected to need zero changes — if they do, that's a signal the original design missed something, worth a `Why` pause rather than pushing through.

---

## Progress ledger (fill in as sessions happen)

```
PriceRadar learning progress

Current phase / week:
Weekly target:

Completed checkpoints:
- <checkpoint> — <evidence>

Current project structure:
- <only relevant paths or modules>

Concepts now in use:
-

Diagrams and traced scenarios:
- <view> — <happy path, failure path, or constraint change>

What works:
-

What is unclear or failing:
-

Resources used:
- <exact section and extracted concept>

Next checkpoint:
- <one small outcome>
```

## Resource prescriptions (use only when a named gap appears)

```
Resource: Go by Example — "HTTP Clients" (gobyexample.com/http-clients)
Study only: the client construction + response handling section
Extract: how a *http.Client is built once and reused; defer-close pattern for response bodies
Skip for now: HTTP servers section (not needed until Phase 9)
Apply immediately: Phase 1 checkpoint 1.1
```

```
Resource: Effective Go — "Interfaces" section (go.dev/doc/effective_go#interfaces)
Study only: the interface satisfaction section (implicit implementation)
Extract: why no "implements" keyword; how RoundTripper composition works
Skip for now: everything on generics/embedding beyond interfaces
Apply immediately: Phase 4 checkpoint 4.1
```
