# Contributing benchmarks

Thanks for helping expand Pareto. A benchmark contribution should make the
underlying observations more auditable, not merely add another name to the
picker. Preserve source rows, exact model revisions, configuration differences,
cost denominators, dates, uncertainty, and provenance whenever the publisher
provides them.

## Before you start

A suitable source is public, stable enough to refresh automatically, and clear
about what its score measures. It must identify model versions rather than only
model families. Include the source URL and its license or reuse terms in the pull
request. Do not add private data, API keys, scraped personal information, or data
whose redistribution is prohibited.

The pipeline uses the Go standard library and publishes a versioned snapshot in
`data/data.json` and `data/data.js`. Missing fields remain missing: do not infer
evaluation dates, costs, model revisions, uncertainty, or configurations.

## Local setup

You need Go, Node.js, and Git. No application dependencies are installed.

```sh
go test ./...
go vet ./...
node scripts/check_frontend.mjs
node scripts/check_snapshot.mjs
go run .
```

Then open <http://127.0.0.1:8082>. Use the Go server rather than a generic static
server because its cache behavior matches production.

## Path A: add a benchmark from Epoch AI

Epoch benchmark CSV files are read from its `benchmark_data.zip`. The normalized
benchmark id is the CSV filename without `.csv` or the optional `_external`
suffix. The first column must be `Model version`.

1. Inspect the upstream CSV header and representative rows. Identify the exact
   score column and confirm whether higher values mean better performance. Pareto
   currently assumes benchmark score is higher-is-better.
2. Add an explicit `benchmarkScoreCols` entry in `fetch.go`. This allowlist is
   mandatory: unknown schemas are quarantined instead of guessed.
3. If the source publishes execution cost, add a `benchmarkCosts` entry with the
   exact column, a truthful display label, and its denominator. Valid examples
   include `task`, `test`, `run`, `total`, `average`, or `reported`. Never turn a
   total or ambiguous amount into “$ per task.”
4. If uncertainty is available, add a `benchmarkUncertainty` entry. Supported
   forms are confidence bounds (`ci`), a 95% half-width (`half`), standard error
   (`se`), and standard deviation (`sd`). Do not convert between them silently.
5. Reuse or carefully extend the field allowlists when the CSV exposes evidence:
   `evalDateCols`, `configColumns`, `observationMetrics`, `rowSourceURL`,
   `rowTraceURL`, and `rowBenchmarkVersion`. Only map columns whose units and
   meaning match the existing field.
6. Add a `benchNames` entry if automatic title casing is poor. Add the benchmark
   to `featured` only if it belongs in the deliberately small top group.
7. Add the id to the appropriate `BENCH_CAT` group in `index.html`. Unmapped
   benchmarks remain usable under “Other,” but intentional categorization is
   preferred.
8. Add a small zipped CSV fixture test in `fetch_test.go`. Assert the score and
   every special semantic field you added: configuration, cost basis,
   uncertainty, evaluation date, trace, version, or resource metrics.

The parser automatically supplies stable observation ids, source model ids,
normalized exact-revision model ids, benchmark counts, coverage counts, and
quality diagnostics.

## Path B: add another public source

Use a dedicated fetch-and-parse function; `fetchDatacurveDeepSWE` is the existing
example. Keep source-specific structures out of the generic Epoch parser.

The adapter must:

- use a documented, stable URL and the shared bounded HTTP client behavior;
- parse into the common benchmark, observation, and exact-model records below;
- generate deterministic observation ids from stable source identity fields;
- validate score ranges, required fields, freshness, row/model counts, and any
  promised cost or uncertainty coverage before accepting the result;
- fail closed or preserve a clearly marked fallback instead of publishing a
  partial response;
- set `source_url`, `source_status`, optional `source_error`, and `via` on the
  benchmark record;
- add the source URL and attribution to snapshot provenance; and
- include parser, degraded-source, and quality-gate tests that require no live
  network access.

If the source introduces a genuinely new observation field or unit, update the
snapshot schema glossary, `llms-full.txt`, OpenAPI where applicable, and increment
`schema_version` for a backward-incompatible change.

## Common record contract

Every accepted observation needs these fields:

| Field | Meaning |
|---|---|
| `oid` | Stable, unique observation id |
| `m` | Normalized exact model-revision id |
| `b` | Benchmark id |
| `v` | Finite numeric score |
| `sm` | Source's original model id |

Preserve these optional fields when published: `sid` source row id, `dn` source
display name, `cfg` configuration, `c` reported benchmark cost, `d` evaluation
date, `evaluated_at` timestamp, `rd` release date, `ci`/`lo`/`hi`/`se`/`sd`
uncertainty, `src` source URL, `trace` run log, `bv` benchmark version, `metrics`
resource measurements, and `notes`.

Model identity is intentionally strict. `modelKey` normalizes punctuation but
must preserve dated revisions and qualifiers. Never join prices by family name.
If an exact model needs a reviewed OpenRouter mapping, add it to `aliases.json`.

## Refresh and verify

`go run . -fetch` uses the live network and rewrites the tracked snapshot plus
price history. Run it only when you intend to review and commit refreshed data.

```sh
go test ./...
go vet ./...
go run . -fetch
node scripts/check_snapshot.mjs
node scripts/check_frontend.mjs
```

Review `data/quality.json`, the fetch summary, and the generated diff. Confirm:

- the benchmark is not quarantined and its parsed/source row counts make sense;
- score, date, cost, uncertainty, trace, and metric coverage are plausible;
- no unrelated benchmark or total snapshot count collapsed;
- metadata conflicts and unmatched price joins are understood;
- the benchmark can be selected, searched, viewed as a chart and table, and
  exported; and
- unavailable axes are disabled rather than displaying a misleading chart.

For UI-affecting changes, run the quick browser audit if Python Playwright and
Chromium are installed:

```sh
python3 scripts/audit_browser.py http://127.0.0.1:8082
```

The `--full` audit is intentionally CPU-intensive; reserve it for release checks.

## Pull request checklist

- Explain the benchmark, score direction/unit, source, version, and reuse terms.
- Describe every added mapping and why its semantics match the common field.
- Include deterministic fixtures for new parsing behavior and failure cases.
- Include the generated data artifacts only when the source refresh is intended.
- Keep unrelated snapshot churn and formatting changes out of the pull request.
- Never commit credentials, cookies, private endpoints, or contributor data.

By contributing, you agree that your contribution to this repository is licensed
under the MIT License. Third-party data remains subject to its original license
and attribution requirements.
