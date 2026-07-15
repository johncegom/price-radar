# PriceRadar — Project Description

## What it is
A personal Go program that checks whether a specific device (e.g. **MacBook Pro M2 Pro 32GB/512GB**) is listed on a single retailer page — [fptshop.com.vn/may-doi-tra](https://fptshop.com.vn/may-doi-tra) (FPT Shop's returned/refurbished device listing) — and tracks its price over time.

## Why
- Manually re-checking a listing page for one specific device is tedious and easy to forget.
- The target page's initial batch is server-rendered and publicly accessible, so most of the pipeline can run mechanically without a login. Seeing the *full* ~650-item catalog does require driving the page's own "Load more" control (see [Compliance posture](#compliance-posture) below) — there's no URL-based pagination to fetch instead.
- The project doubles as a **Go learning vehicle**: a small, real, end-to-end system (HTTP client → parser → storage → decision logic) built primarily on the standard library, that can grow in scope as Go fluency grows.

## What it does
1. Fetches the listing page(s) on a schedule (not continuously).
2. Parses out every product card (name, price, discount, stock).
3. Deterministically narrows ~650+ listings down to a short list of plausible matches for the target device.
4. Judges the short list against the target spec and price/notify criteria — first with simple rules, later with an AI agent layer for ambiguous naming and "is this worth flagging" judgment.
5. Records every observation in a price-history log (not just "seen/unseen") so price trend over time is visible.
6. Optionally notifies when the device appears or its price drops meaningfully.

## Scope today vs. scope tomorrow
This is built as a **single-site product first, multi-site design intent second**: FPT Shop is the only site implemented right now, but the pieces that are inherently site-specific (URL, parsing rules, matching vocabulary) are kept separate from the pieces that never change (scheduling, storage, judgment, notification) — see [Solution Architecture § Future Extensibility](02-solution-architecture.md#future-extensibility-not-built-yet) for exactly which pieces would become per-site plugins later. Nothing below should be read as "will never support other sites" — only as "not solved yet, on purpose."

## What it deliberately does not do (for now)
- No scraping of other retailers or categories — one site, one page, one target spec today (see extensibility note above).
- No login, no checkout automation, no purchase automation.
- No filtered/query-parameter scraping and no reliance on the site's internal/undocumented API — see [Compliance posture](#compliance-posture).
- No continuous/real-time polling — scheduled, low-frequency checks only.
- No third-party HTTP framework — `net/http` only, by explicit design choice (see [System Architecture](03-system-architecture.md)).

## Compliance posture
- Only the clean listing URL is ever fetched or driven — never the filtered/query-parameter product URLs that `robots.txt` disallows (e.g. `?hang-san-xuat=`, `?ram=`, and the other faceted-filter params), and never the disallowed `/ajax/` path or the site's undocumented internal API.
- There is no URL-based pagination on this listing (confirmed by testing `?trang=`, `?page=`, `?p=` — all return an identical response to the base URL). The full ~650-item catalog is only reachable through the page's own client-side "Load more" control, so seeing it in full requires driving a headless browser (see [System Architecture § Browser Automation](03-system-architecture.md#internalbrowser)) against the same permitted URL — never a different, disallowed one.
- Requests are infrequent (hours between runs, not seconds), with a realistic User-Agent and backoff on errors — matching the low-volume, public-endpoint-only approach already used elsewhere in this workspace's job-scraper tooling.

## Companion documents
- [Solution Architecture](02-solution-architecture.md) — what the system is made of and how data flows through it, at a conceptual level.
- [System Architecture](03-system-architecture.md) — the concrete Go tech stack, package layout, and deployment shape.
- [Building Plan](04-building-plan.md) — the phased, checkpoint-driven plan for learning Go by building this project.
