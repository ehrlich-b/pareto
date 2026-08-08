# Design prompt — pareto explorer

You (Claude) are mid-design-pass on this app. This file is your brief: what it is,
what's been tried, what's settled, and what's still open. Read README.md for the
data pipeline; this is only about how the thing looks and reads.

## The job

Static single-page pareto-frontier explorer (`index.html`, vanilla JS/SVG, no build
step) for Bryan's team to see the pareto maximum on AI models — cost vs capability
vs size vs time, ~75 benchmarks. Ships to **pareto.ehrlich.dev** via `deploy.sh`.
The data is good. The complaint was the design ("it's not lookin good"). One pass
is done; Bryan says it's still not there. Work locally: `python3 -m http.server 8787`
in the repo, hard-refresh or `?v=n` to dodge stale cache. `file://` won't load in
the browser tools.

## Design thesis of pass 1

The frontier is the entire point of the page, so it must be the loudest thing on
it, and the answer should be readable in words before the reader decodes a single
dot.

What landed (all in `index.html`):

- **Frontier readout bar** between masthead and chart: chips naming each frontier
  model with its axis values. Hovering a chip isolates its point. Reads "Record
  holders" on time axes.
- **Frontier emphasis by weight and fill, never hue**: solid 2.5px ink-colored
  line over a surface halo, dominated region shaded (`--front-fill`), frontier
  points ringed. Color stays reserved for lab identity.
- **Type system**: system sans for prose, mono (`--mono`) for every datum, label,
  eyebrow, and control caption; tabular numerals; uppercase letterspaced control
  labels.
- **Masthead**: eyebrow + 30px h1 + one-line sub, freshness as a status pill
  (green dot fresh / amber stale) with meta lines right-aligned.
- **Neutrals re-biased cool** (both themes), tokens only — components never
  hard-code color. Light and dark are separately tuned, not inverted.

## Settled — do not relitigate

- **Palette is a validated 3-slot cap**: Anthropic `#2a78d6/#3987e5`, OpenAI
  `#eb6834/#d95926`, Google `#1baf7a/#199e70` (light/dark). Passes all six checks
  in both modes via the dataviz skill's `validate_palette.js`. **No 4th hue** —
  xAI/Meta/Moonshot/DeepSeek get shapes, the rest gray.
- **Gray "Other" vs Google green fails deutan ΔE (2.0) as same-size dots.** Fixed
  by form: Other renders 1.5px smaller. Don't try to fix it with hue — every
  candidate green breaks the lightness band. Don't collide with hollow=open-weights.
- **`writeHash()` wraps `history.replaceState` in try/catch.** It throws in
  sandboxed frames and blanks the page. Keep the guard.
- Frontier emphasis never gets its own hue (would read as a 4th lab).

## Pass 2 (landed 2026-08-05, via a claude.ai design session, merged + deployed)

- Readout became the chart's header inside the card: benchmark title, axes line,
  stats (on frontier / beaten / frontier fit r²), then the chips.
- Default view flipped to x=cost, y=score; picking an axis equal to the other
  swaps them.
- Lower-is-better axes get reversed scales — better is ALWAYS ↗; "WORSE ↙ /
  BETTER ↗" region labels replace the old corner cue.
- Non-time views draw a fitted frontier (regression through undominated points in
  logit/log space): soft band + solid over the observed span, dashed where it
  extrapolates, legend says "estimated". Time views keep the true record staircase.
- Dominated points recede to 45% opacity; frontier stroke 3px over a 6.5px halo.
- Verified: 6,660 benchmark × axis combos, zero errors; both themes.

## Roadmap (usability review 2026-08-07 — site is SHARED now; per deploy: run the
## 6,660-combo sweep, never break URL-hash back-compat)

Facts behind the review: only 3/74 Epoch benchmarks publish per-row eval dates
(terminalbench 202/202, gso 20/38, aider 9/72 — 4% of rows); everything else
falls back to snapshot date in the tooltip. Epoch also lags source leaderboards
(DeepSWE: Epoch has v1, 50 runs/18 models; datacurve is on v1.1, 113 tasks/21
models, with ±CIs and effort badges).

Phase 1 — copy & trust (zero risk):
- Stale copy: freshness pill says "run fetch.py", footer says "Refresh data with
  python3 fetch.py" — both false now (self-refreshing Go app, fetch.py deleted).
- Tooltip "Scored: not published (Epoch snapshot …)" reads as an error — reframe
  as provenance, and give the tooltip hierarchy (headline score+cost, then meta).
- r² stat in readout is unexplained — title tooltip or drop.

Phase 2 — use the freshness data we already collect:
- price_history.jsonl accrues every 6h and is never read: price movement ↑↓
  vs ~7d ago in tooltip/table (llm-stats does this well).
- NEW badge for models released <30d; per-benchmark "updated X ago" where
  latest_eval exists.

Phase 3 — density & discovery:
- Benchmark picker: 74 flat options → Epoch-style category groups + a filter
  input. (Epoch's own hub groups by Math/Agent/SWE/Games/etc.)
- "Frontier only" toggle to hide dominated points on dense benches (MMLU plots
  131; the gray mass overlaps hit targets). Smarter label collision handling.
- Table: score bars, movement column.

Phase 4 — data depth:
- Direct datacurve DeepSWE ingestion (v1.1 + confidence intervals) as a
  second-source override; CIs anywhere a source publishes them.
- Real mobile pass (untested — window resize wouldn't shrink below ~1560 in
  review). Side-by-side compare view (llm-stats /compare pattern) if wanted.

Steal-list sources: artificialanalysis.ai (superlative top-4 cards, estimated-*
flags), llm-stats.com (movement arrows, NEW badges, freshness statement,
compare flow), epoch.ai/benchmarks (category grouping, frontier-only toggle),
deepswe.datacurve.ai (±CIs, version toggle, effort badges).

## History

A claude.ai artifact build of this page exists (see memory `pareto-artifact-url`)
— it was a mis-aimed deliverable, superseded by pareto.ehrlich.dev. Ignore it
unless Bryan asks. Deploy is `./deploy.sh`; DNS record for the subdomain was still
pending in Cloudflare as of 2026-08-05.
