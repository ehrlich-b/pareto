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

The fetch writes `data/data.js` (the snapshot the viewer reads), writes
`data/quality.json` (machine-readable validation and coverage), and appends one line
per run to `data/price_history.jsonl` (price time-travel). In production
(`./deploy.sh` → pareto.ehrlich.dev) the binary runs as a systemd service behind
nginx and refreshes its own data; a failed refresh keeps the last good snapshot.

## Data sources (all free, no API keys)

| Source | Provides | License |
|---|---|---|
| [Epoch AI Benchmarking Hub](https://epoch.ai/benchmarks) | ~75 benchmark score sets, release dates, orgs, training FLOP, $/task on agentic benches | CC-BY 4.0 |
| [Epoch AI models dataset](https://epoch.ai/data) | parameter counts, open/closed weights | CC-BY 4.0 |
| [OpenRouter API](https://openrouter.ai/api/v1/models) | live $/M token prices, context windows, AA intelligence indices, clean display names | — |

## Axes

Any of these on X or Y (log-scaled where it matters): benchmark score, $ per task
(where the benchmark measured it), blended/input/output $/M tokens, release date,
parameters, training FLOP, context window, AA intelligence index. The pareto frontier
staircase is computed live for whatever pair is selected — on release-date axes it
becomes a best-score-to-date record line that stops at today. State lives in the URL
hash (copy-view-link button), so views are shareable/bookmarkable.

Viewer: click a point to pin its tooltip, `find model…` to highlight, legend chips
toggle labs, table view sorts and exports CSV. Color = Anthropic/OpenAI/Google
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
cost denominator, evaluation date, and uncertainty. The viewer may summarize to
the best configuration for non-cost views, but reported-cost axes always use every
configuration so a cheap run cannot disappear from the frontier.

The refresh is rejected before publication if core counts collapse, required
schemas disappear, observation ids collide, dates are invalid/future, or the live
DeepSWE artifact is thin, stale, out of range, or missing cost/CI coverage. Unknown
benchmark schemas are quarantined and reported. `data.js` is renamed last, so a
failed refresh leaves the last validated snapshot serving.

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
```

Tests cover revision identity, configuration preservation, cost/uncertainty
semantics, explicit Epoch mappings, OpenRouter route selection, and quality-gate
failure behavior.
