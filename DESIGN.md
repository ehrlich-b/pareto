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

## Roadmap (usability review 2026-08-07)

The site is shared now. Preserve URL-hash backward compatibility and run the
current browser audit before releases.

Facts behind the review: evaluation dates are sparse and must never be inferred;
the pipeline now recognizes source timestamps including Epoch-internal `Started at`
values as well as external run/evaluation dates. Unknown dates remain explicit.
Epoch also lags source leaderboards
(DeepSWE: Epoch has v1, 50 runs/18 models; datacurve is on v1.1, 113 tasks/21
models, with ±CIs and effort badges).

All four phases SHIPPED 2026-08-08 (repo is git now; one commit per phase):
1. Copy & trust — tooltip hero (score ± CI, $/task) + meta table + provenance
   footer; pill/footer no longer mention fetch.py; r² and evals-thru stats have
   title explainers.
2. Freshness — Go emits `price_prev` (newest history line ≥6d old, else oldest)
   from price_history.jsonl; tooltip shows ↑↓ vs baseline when ≥5% move; NEW
   badge (release <30d) in tooltip + table; "evals thru" readout stat.
3. Density & discovery — picker grouped into 7 hand-mapped categories
   (BENCH_CAT in index.html — new benchmarks fall into "Other", extend the map);
   "frontier only" checkbox (hash param `fonly`, additive); table score bars +
   NEW badges. Label collision handling already existed — untouched.
4. Data depth — DeepSWE ingested live from
   deepswe.datacurve.ai/artifacts/v1.1/leaderboard-live.json (endpoint found
   via network tab, not documented): replaces Epoch's laggier rows when ≥20
   parse, else keeps Epoch; adds `ci` (95% half-width) to score recs, synthesizes
   meta for models Epoch lacks (org by slug prefix), sets bench `via` shown in
   provenance. 390px fixed: selects clamp, .grp wraps (tested via iframe —
   window resize can't go below ~1560).

Not built (didn't clear the "wanted" bar): side-by-side compare view;
superlative top-4 cards.

Steal-list sources for later: artificialanalysis.ai (superlative cards,
estimated-* flags), llm-stats.com (compare flow), epoch.ai/benchmarks,
deepswe.datacurve.ai (version toggle).

Verification ritual per deploy (site is shared): the 49,728-state full sweep in
`scripts/audit_browser.py --full` (all benchmarks × axis pairs × views, plus
range/weights/best/frontier-only filters) with a window error trap, plus eyeball
of tooltip/readout/table in the browser. Preview against the Go server
(`go build && ./pareto -root . -every 0`), NOT python http.server — python
sends no cache headers and Chrome's heuristic cache serves stale data.js
(hard-reload to recover).

## Data-integrity pass (shipped 2026-08-08)

- Observations are lossless: exact model revisions, source ids/links, benchmark
  versions, configuration dimensions, cost semantics, dates, and uncertainty are
  retained. The old unconditional `(model, effort) -> maximum score` dedupe is gone.
- Reported-cost axes always include every configuration. "Best configuration"
  remains available for non-cost views only.
- OpenRouter joins are exact-revision or explicit-alias only; batch/free/thinking
  routes and ambiguous keys are withheld.
- Epoch score/cost/uncertainty columns are explicit contracts. Unknown schemas are
  quarantined and visible in `data/quality.json`.
- Publication is gated on counts, observation identity, dates, schema continuity,
  and live DeepSWE completeness/freshness. `data.js` remains the final atomic
  commit marker.
- Snapshot fetch age, evaluation age, and source fallback status are distinct in
  the UI. Unknown evaluation dates never render as fresh.

## Evidence + availability pass (2026-08-08)

- The page remains a data workbench, not a chatbot. A searchable benchmark combobox and
  collapsible evidence summary expose model/run counts plus date, uncertainty, trace,
  resource-metric, and version coverage before the chart is interpreted.
- Evaluation date is an axis and best-observed-score history preset. Artificial Analysis coding
  and agentic indices join the existing intelligence axis. Dominated point details
  name a nearby observation that is at least as good on both current axes.
- Source timestamps, additional uncertainty schemas, METR versions, run logs,
  notes, country/compute notes, and allowlisted execution metrics survive ingestion.
  Tooltips and sufficiently covered table columns re-present them.
- Keyboard-focusable SVG points, chart title/description, and explicit control
  labels make the same evidence usable without pointer-only interaction.
- `data.json`, a versioned schema glossary, filtered CORS API, OpenAPI, llms files,
  robots/sitemap, canonical links, and Dataset JSON-LD make the dataset available
  independently of the page. GitHub Actions verifies Go, frontend syntax, OpenAPI,
  snapshot parity, and data invariants.

## Exhaustive interaction pass (2026-08-08)

- Benchmark search and selection are one grouped, keyboard-accessible combobox.
  The no-hash default is DeepSWE mean reported cost/task vs score, last 12 months,
  all weights, best configuration.
- “Eval records” is now “eval date vs score” and is described as best observed
  score as evaluation dates advance. It is available only when a benchmark has
  at least three dated observations across at least two distinct dates; DeepSWE's
  53 same-day runs no longer produce a one-point pseudo-history.
- Unavailable axes and presets are disabled per benchmark, time belongs on X, and
  stale or invalid bookmarked pairs fall back to a meaningful supported pair.
- Regression paths discard values outside their visible scale and uncertainty bars
  clamp to the visible/log-safe domain. This prevents non-finite SVG coordinates on
  extreme context/price fits and score intervals that cross zero.
- `scripts/audit_browser.py` is the release gate: quick mode covers all 11,544
  ordered benchmark/axis scatter combinations; full mode covers 49,728 rendered
  axis, view, and filter states.
- The default hierarchy is controls → answer → chart. Coverage details and full
  frontier membership remain accessible disclosures instead of permanently
  occupying several rows above the visualization.

## History

A claude.ai artifact build of this page exists (see memory `pareto-artifact-url`)
— it was a mis-aimed deliverable, superseded by pareto.ehrlich.dev. Ignore it
unless Bryan asks. Deploy is `./deploy.sh`; the Cloudflare-backed production site
is live at pareto.ehrlich.dev.
