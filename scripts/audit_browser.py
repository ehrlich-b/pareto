#!/usr/bin/env python3
"""Exhaustive rendered-state audit. Requires Python Playwright + Chromium."""

import argparse
import json
import time

from playwright.sync_api import sync_playwright


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("url", nargs="?", default="http://127.0.0.1:8082/")
    parser.add_argument("--full", action="store_true", help="cover every view/frontier/filter mode")
    args = parser.parse_args()
    mode = "full" if args.full else "quick"
    started = time.time()

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch()
        page = browser.new_page(viewport={"width": 1440, "height": 1000})
        page_errors = []
        page.on("pageerror", lambda exc: page_errors.append(str(exc)))
        page.goto(args.url, wait_until="networkidle")
        result = page.evaluate(
            """(mode) => {
              const issues = [], stats = {renders:0, empty:0, scatter:0, table:0};
              const benchIds = Object.keys(D.benchmarks).sort(), axes = Object.keys(AXES);
              const original = {bench:state.bench,x:state.x,y:state.y,range:state.range,weights:state.weights,best:state.best,fonly:state.fonly,view:state.view};
              const check = phase => {
                stats.renders++;
                const root = state.view === "scatter" ? document.querySelector("#chart") : document.querySelector("#tablewrap");
                const html = root ? root.innerHTML : "", context = {phase,bench:state.bench,x:state.x,y:state.y,view:state.view,fonly:state.fonly,range:state.range,weights:state.weights,best:state.best};
                if (/NaN|Infinity/.test(html)) issues.push({...context,problem:"non-finite SVG/HTML"});
                if (!document.querySelector("#readout")?.innerText || !document.querySelector("#note")?.innerText) issues.push({...context,problem:"missing readout/note"});
                if (state.view === "scatter") {
                  stats.scatter++;
                  if (document.querySelectorAll("#chart svg").length !== 1) issues.push({...context,problem:"scatter SVG count"});
                  if (html.includes("no data for this axis combination")) stats.empty++;
                } else {
                  stats.table++;
                  if (document.querySelectorAll("#tablewrap table.data").length !== 1) issues.push({...context,problem:"table count"});
                }
              };
              state.range="all"; state.weights="all"; state.best=true; state.labSel=null; state.offLabs.clear(); state.offOrgs.clear();
              for (const benchId of benchIds) {
                state.bench=benchId;
                for (const x of axes) for (const y of axes) {
                  if (x === y) continue;
                  state.x=x; state.y=y; state.view="scatter"; state.fonly=false;
                  try { render(); check("axes"); } catch (error) { issues.push({phase:"axes",bench:benchId,x,y,problem:String(error.stack||error)}); }
                  if (mode === "full") for (const view of ["scatter","table"]) for (const fonly of [false,true]) {
                    if (view === "scatter" && !fonly) continue;
                    state.view=view; state.fonly=fonly;
                    try { render(); check("axes-modes"); } catch (error) { issues.push({phase:"axes-modes",bench:benchId,x,y,view,fonly,problem:String(error.stack||error)}); }
                  }
                }
                if (mode === "full") {
                  state.x=D.benchmarks[benchId].has_cost?"taskcost":"blended"; state.y="score"; state.fonly=false;
                  for (const range of ["all","24","12","6"]) for (const weights of ["all","open","closed"]) for (const best of [false,true]) for (const view of ["scatter","table"]) {
                    Object.assign(state,{range,weights,best,view});
                    try { render(); check("filters"); } catch (error) { issues.push({phase:"filters",bench:benchId,range,weights,best,view,problem:String(error.stack||error)}); }
                  }
                  state.range="all"; state.weights="all"; state.best=true;
                }
              }
              Object.assign(state, original); render();
              return {benchmarks:benchIds.length,axes:axes.length,stats,issues:issues.slice(0,200),issueCount:issues.length};
            }""",
            mode,
        )
        browser.close()

    result["pageErrors"] = page_errors
    result["seconds"] = round(time.time() - started, 2)
    print(json.dumps(result, indent=2))
    return 1 if result["issueCount"] or page_errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
