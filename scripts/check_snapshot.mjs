#!/usr/bin/env node

import fs from "node:fs";
import vm from "node:vm";

const filename = process.argv[2] || "data/data.js";
const context = { window: {} };
vm.runInNewContext(fs.readFileSync(filename, "utf8"), context, { filename });
const D = context.window.PARETO;
const fail = message => { throw new Error(message); };

if (!D || !D.benchmarks || !D.models || !Array.isArray(D.scores)) fail("snapshot envelope is incomplete");
if (!D.quality || D.quality.gates?.status !== "pass") fail("embedded quality gate is not passing");

const ids = new Set();
const byBenchmark = new Map();
let dated = 0, costed = 0, uncertain = 0;
for (const s of D.scores) {
  if (!s.oid || ids.has(s.oid)) fail(`duplicate/missing observation id: ${s.oid || "<missing>"}`);
  ids.add(s.oid);
  if (!D.benchmarks[s.b]) fail(`unknown benchmark ${s.b}`);
  if (!D.models[s.m]) fail(`unknown model ${s.m}`);
  if (!s.sm) fail(`observation ${s.oid} lacks its source model id`);
  if (typeof s.v !== "number" || !Number.isFinite(s.v)) fail(`invalid score in ${s.oid}`);
  if (s.d && !/^\d{4}-\d{2}-\d{2}$/.test(s.d)) fail(`invalid eval date in ${s.oid}`);
  if (s.rd && !/^\d{4}-\d{2}-\d{2}$/.test(s.rd)) fail(`invalid release date in ${s.oid}`);
  if (s.c != null && (!D.benchmarks[s.b].has_cost || !D.benchmarks[s.b].cost_label))
    fail(`cost semantics missing for ${s.b}`);
  byBenchmark.set(s.b, (byBenchmark.get(s.b) || 0) + 1);
  dated += s.d ? 1 : 0;
  costed += s.c != null ? 1 : 0;
  uncertain += s.ci != null || s.lo != null || s.se != null || s.sd != null ? 1 : 0;
}

for (const [id, b] of Object.entries(D.benchmarks)) {
  if ((byBenchmark.get(id) || 0) !== b.n) fail(`${id}: metadata n=${b.n}, rows=${byBenchmark.get(id) || 0}`);
}
for (const [id, m] of Object.entries(D.models)) {
  if (!m.family) fail(`${id}: missing family key`);
  if (m.price_in != null && !["exact", "alias"].includes(m.price_match)) fail(`${id}: unreviewed price match`);
}

const counts = D.quality.counts || {};
if (counts.benchmarks !== Object.keys(D.benchmarks).length || counts.models !== Object.keys(D.models).length || counts.scores !== D.scores.length)
  fail("embedded quality counts do not match payload");
for (const [key, value] of [["eval_date", dated], ["cost", costed], ["uncertainty", uncertain]]) {
  if (D.quality.coverage?.[key]?.rows !== value) fail(`${key} coverage does not match payload`);
}

console.log(`snapshot ok: ${Object.keys(D.benchmarks).length} benchmarks · ${D.scores.length} observations · ${Object.keys(D.models).length} exact models · ${costed} costs · ${uncertain} uncertainty records · ${dated} dated runs · quality ${D.quality.status}`);
