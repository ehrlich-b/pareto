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
console.log(`frontend ok: ${checked} inline script parsed · OpenAPI JSON valid`);
