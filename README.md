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

The fetch writes `data/data.js` (the snapshot the viewer reads) and appends one line
per run to `data/price_history.jsonl` (price time-travel, for later). In production
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

## Joining models across sources

Epoch model ids (`claude-opus-5_high`) are matched to OpenRouter slugs
(`anthropic/claude-opus-5`) by normalization. `fetch.py` prints the top unmatched
models per run — most are deprecated models with no current API price (correct: they
get no cost axis rather than a stale fake price). For genuine mismatches, add an entry
to `aliases.json`: `{"epoch-slug": "openrouter-slug"}`.
