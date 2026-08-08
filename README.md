# pareto

Pareto-frontier explorer for AI models: cost vs capability vs size vs time, across ~75
benchmarks, with the age of every datum visible. The thing deepswe.datacurve.ai does,
but for any benchmark, any axis pair, and with freshness tracking.

## Use

```
go run . -fetch      # refresh the data snapshot once (stdlib only, ~10s)
go run .             # serve on 127.0.0.1:8082 and self-refresh every 6h
open index.html      # the viewer is still a static page — no build step
```

The fetch writes identical payloads to `data/data.js` (the buildless viewer) and
`data/data.json` (standard JSON for programs), writes `data/quality.json`
(machine-readable validation and coverage), and appends one line per run to
`data/price_history.jsonl` (price time-travel). In production
(`./deploy.sh` → pareto.ehrlich.dev) the binary runs as a systemd service behind
nginx and refreshes its own data; a failed refresh keeps the last good snapshot.

## Data sources (all free, no API keys)

| Source | Provides | License |
|---|---|---|
| [Epoch AI Benchmarking Hub](https://epoch.ai/benchmarks) | ~75 benchmark score sets, release dates, orgs, training FLOP, $/task on agentic benches | CC-BY 4.0 |
| [Epoch AI models dataset](https://epoch.ai/data) | parameter counts, open/closed weights | CC-BY 4.0 |
| [OpenRouter API](https://openrouter.ai/api/v1/models) | live $/M token prices, context/modalities, AA intelligence/coding/agentic indices, clean display names | — |

## Axes

Any of these on X or Y (log-scaled where it matters): benchmark score, $ per task
(where the benchmark measured it), blended/input/output $/M tokens, release or
evaluation date, parameters, training FLOP, context window, and AA intelligence,
coding, or agentic index. The pareto frontier staircase is computed live for whatever pair is selected — on time axes it
becomes a best-observed-score history that advances only across distinct, source-published
evaluation or release dates and stops at today. Benchmarks with only one distinct date do
not offer a misleading history view. State lives in the URL hash (copy-view-link button),
so views are shareable/bookmarkable. The default view is DeepSWE mean reported cost per
task vs benchmark score over the latest 12 months.

Viewer: search or browse all benchmarks in one keyboard-accessible combobox; unavailable
axes are disabled for the selected benchmark. Click or keyboard-focus a point for its
evidence; pin tooltips; use `find model…` to highlight; and use legend chips to toggle labs.
Dominated points name a nearby model that beats them on both selected axes. Table view
sorts, shows source resource/trace fields when sufficiently covered, and exports CSV.
Color = Anthropic/OpenAI/Google
(colorblind-validated 3-slot cap for scatters); xAI/Meta/Moonshot/DeepSeek get
shapes; the rest are gray. Hollow = open weights.

## Freshness

- Header: snapshot fetch date (warns at >7 days), newest eval on record, per-benchmark latest eval date.
- Tooltips: score eval age, price as-of date, model release age.
- Points with eval dates older than 90 days render dimmed. (Most Epoch rows don't
  publish eval dates; where absent, the benchmark-level date is the signal.)

## Data integrity

Every evaluation row is retained as an observation with a stable id, its exact
source model revision, source row id/link, benchmark version, configuration
(effort, agent, harness, tools, shots, budget, provider, and format where supplied),
cost denominator, evaluation date/timestamp, uncertainty, run trace, source notes,
and allowlisted resource metrics (tokens, steps, latency, runs, votes, and spend).
The viewer may summarize to
the best configuration for non-cost views, but reported-cost axes always use every
configuration so a cheap run cannot disappear from the frontier.

The refresh is rejected before publication if core counts collapse, required
schemas disappear, observation ids collide, dates are invalid/future, or the live
DeepSWE artifact is thin, stale, out of range, or missing cost/CI coverage. Unknown
benchmark schemas are quarantined and reported. `data.js` is renamed last, so a
failed refresh leaves the last validated snapshot serving.

## Public data and API

No key is required. Responses are read-only JSON with CORS enabled:

```text
GET /api/v1                                      endpoint index and counts
GET /api/v1/snapshot                             complete versioned snapshot
GET /api/v1/benchmarks?id=terminalbench          benchmark catalog
GET /api/v1/models?q=claude&open=false           model search
GET /api/v1/observations?benchmark=terminalbench observations (paginated)
GET /api/v1/quality                              quality and coverage report
GET /healthz                                     service + snapshot health
```

Collection endpoints accept `offset` and `limit` (default 100, maximum 1000).
Observation keys and resource units are defined inside the complete snapshot's
`schema` object; backward-incompatible changes increment `schema_version`.
[`openapi.json`](openapi.json), [`llms.txt`](llms.txt), and
[`llms-full.txt`](llms-full.txt) make the interface discoverable to programs and
agents. `robots.txt`, `sitemap.xml`, canonical metadata, and Dataset JSON-LD make it
crawlable without requiring the interactive page.

Evaluation freshness, source fetch freshness, and live-source fallback status are
separate fields. A missing evaluation date is displayed as unknown, never fresh.

## Joining models across sources

Model identity preserves dated revisions and qualifiers. Epoch model ids
(`claude-opus-5_high`) are split into the exact model revision plus configuration;
OpenRouter prices join only to an exact id/canonical revision or an explicit alias.
Batch, free, and thinking routes are excluded from ordinary price matching, and
ambiguous keys are withheld and reported instead of being selected by API order.

The fetch prints the top unmatched models per run. Most are deprecated models with
no current API price, so missing price is more honest than a guessed current price.
For reviewed mismatches, add an entry to `aliases.json`:
`{"epoch-exact-model-id": "provider/openrouter-exact-id"}`.

## Verification

```sh
go test ./...
go vet ./...
go run . -fetch
node scripts/check_snapshot.mjs
node scripts/check_frontend.mjs
# Optional release audit (requires Python Playwright + Chromium):
python3 scripts/audit_browser.py --full http://127.0.0.1:8082
```

Tests cover revision identity, configuration preservation, cost/uncertainty
semantics, evaluation timestamps, trace/resource preservation, explicit Epoch
mappings, OpenRouter route selection, API filters/pagination, and quality-gate
failure behavior. The browser audit renders every benchmark/axis pair plus all
range, weight, best-configuration, frontier-only, scatter, and table states. The
same non-browser checks run in GitHub Actions.
