#!/usr/bin/env node

import fs from "node:fs";

const html = fs.readFileSync("index.html", "utf8");
const scripts = [...html.matchAll(/<script([^>]*)>([\s\S]*?)<\/script>/gi)];
if (!scripts.length) throw new Error("index.html contains no scripts");
let checked = 0;
for (const [, attrs, source] of scripts) {
  if (/\bsrc\s*=/.test(attrs) || /application\/ld\+json/.test(attrs)) continue;
  // Parse without executing; browser globals and the generated snapshot are not
  // available in CI, but syntax regressions should still fail the build.
  new Function(source);
  checked++;
}
for (const file of ["openapi.json"]) JSON.parse(fs.readFileSync(file, "utf8"));
if (checked === 0) throw new Error("no inline application script checked");
const required = [
  ['id="benchcombo"', "searchable benchmark combobox"],
  ['class="evidence-details"', "collapsible evidence coverage"],
  ['class="frontier-details"', "collapsible frontier model list"],
  ['bench: "deepswe", x: "taskcost", y: "score", range: "12"', "DeepSWE cost/score default"],
  ['data-x="eval_date" data-y="score"', "evaluation-date history preset"],
  ["function ensureValidAxes()", "per-benchmark axis validation"],
  ["function projectBound(scale, ax, value)", "safe uncertainty projection"],
];
for (const [needle, feature] of required) {
  if (!html.includes(needle)) throw new Error(`missing ${feature}`);
}
for (const legacy of ['id="benchfind"', '>eval records<']) {
  if (html.includes(legacy)) throw new Error(`legacy control/copy remains: ${legacy}`);
}
console.log(`frontend ok: ${checked} inline script parsed · OpenAPI JSON valid`);
