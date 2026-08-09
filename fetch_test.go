package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestModelIdentityPreservesRevisions(t *testing.T) {
	cases := map[string]string{
		"gpt-4o-2024-05-13":          "gpt-4o-2024-05-13",
		"gpt-4o-2024-11-20":          "gpt-4o-2024-11-20",
		"gemini-3-flash-preview":     "gemini-3-flash-preview",
		"Claude 3.5 Sonnet 20241022": "claude-3-5-sonnet-2024-10-22",
	}
	for input, want := range cases {
		if got := modelKey(input); got != want {
			t.Errorf("modelKey(%q) = %q, want %q", input, got, want)
		}
	}
	if got := familySlug("o1-preview-2024-09-12"); got != "o1" {
		t.Fatalf("familySlug collapsed to %q, want o1", got)
	}
}

func TestDatacurveDeepSWERevisionPin(t *testing.T) {
	score, cost := 0.5331858407079646, 0.10023560370265487
	row := datacurveDeepSWERow{
		Model: "deepseek-v4-flash", Config: "mini_swe_agent_deepseek_v4_flash_max",
		Effort: "max", PassAt1: &score, MeanCost: &cost,
		NPassed: 241, NAttempted: 452, NRuns: 4,
	}
	if got := datacurveModelKey(row); got != "deepseek-v4-flash-0731" {
		t.Fatalf("known Datacurve run mapped to %q", got)
	}

	// A future rerun behind the same moving provider alias must not inherit the
	// April checkpoint's metadata or remain incorrectly pinned to 0731.
	row.NPassed++
	if got := datacurveModelKey(row); got != datacurveUnversionedDeepSeekFlash {
		t.Fatalf("unknown Datacurve run mapped to %q", got)
	}

	other := datacurveDeepSWERow{Model: "gpt-5-6-sol"}
	if got := datacurveModelKey(other); got != "gpt-5-6-sol" {
		t.Fatalf("unrelated model mapped to %q", got)
	}

	// Pointer addresses differ across decodes, but published observation ids must
	// remain stable as long as the source values do.
	score2, cost2 := score, cost
	row2 := row
	row2.PassAt1, row2.MeanCost = &score2, &cost2
	got1 := observationID("deepswe-datacurve", datacurveObservationIdentity(row))
	got2 := observationID("deepswe-datacurve", datacurveObservationIdentity(row2))
	if got1 != got2 {
		t.Fatalf("Datacurve observation id is unstable: %q != %q", got1, got2)
	}
}

func TestBenchmarkParserKeepsConfigurationsAndSemantics(t *testing.T) {
	data := zipFixture(t, map[string]string{
		"arc_agi_external.csv": "Model version,Score,Name,Cost per task,Release date,Organization,Source link,id\n" +
			"gemini-3-flash-preview,0.29,Gemini 3 Flash Preview (Low),0.0163,2025-12-17,Google DeepMind,https://arcprize.org/leaderboard,low\n" +
			"gemini-3-flash-preview,0.8467,Gemini 3 Flash Preview (High),0.1743,2025-12-17,Google DeepMind,https://arcprize.org/leaderboard,high\n",
		"terminalbench_external.csv": "Model version,Agent,Accuracy mean,Release date,Organization,Date of evaluation,Source link,id\n" +
			"o3-2025-04-16,o3 (15 steps),0.10,2025-04-16,OpenAI,2026-05-01,https://example.test/tb,a\n" +
			"o3-2025-04-16,o3 (100 steps),0.30,2025-04-16,OpenAI,2026-05-02,https://example.test/tb,b\n",
		"proofbench_external.csv": "Model version,Accuracy,Accuracy Standard Error,Cost per test (USD),Release date,Organization,id\n" +
			"gpt-5.6-sol_max,0.5,0.02,1.75,2026-07-09,OpenAI,p1\n",
	})
	benchmarks, scores, models, diag, err := parseEpochBenchmarks(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 5 || len(models) != 3 {
		t.Fatalf("got %d scores / %d models", len(scores), len(models))
	}
	if len(diag.Quarantined) != 0 {
		t.Fatalf("unexpected quarantine: %v", diag.Quarantined)
	}
	arc, _ := benchmarks["arc_agi"].(map[string]any)
	if arc["cost_basis"] != "task" || arc["cost_label"] != "$ per task" {
		t.Fatalf("bad ARC cost semantics: %#v", arc)
	}
	var low, high map[string]any
	for _, s := range scores {
		if s["b"] != "arc_agi" {
			continue
		}
		if s["e"] == "low" {
			low = s
		}
		if s["e"] == "high" {
			high = s
		}
	}
	if low == nil || high == nil || low["oid"] == high["oid"] {
		t.Fatalf("ARC configurations were not preserved: low=%#v high=%#v", low, high)
	}
	var agents []string
	for _, s := range scores {
		if s["b"] == "terminalbench" {
			cfg, _ := s["cfg"].(map[string]any)
			agents = append(agents, fmt.Sprint(cfg["agent"]))
		}
	}
	if len(agents) != 2 || agents[0] == agents[1] {
		t.Fatalf("agent configurations lost: %v", agents)
	}
	for _, s := range scores {
		if s["b"] == "proofbench" && (s["se"] != 0.02 || s["c"] != 1.75) {
			t.Fatalf("proofbench semantics missing: %#v", s)
		}
	}
}

func TestOpenRouterUsesExactStandardRoutes(t *testing.T) {
	doc := map[string]any{"data": []any{
		map[string]any{"id": "openai/gpt-4o", "canonical_slug": "openai/gpt-4o", "name": "GPT-4o", "pricing": map[string]any{"prompt": "0.0000025", "completion": "0.00001"}},
		map[string]any{"id": "openai/gpt-4o:batch", "canonical_slug": "openai/gpt-4o", "name": "GPT-4o Batch", "pricing": map[string]any{"prompt": "0.00000125", "completion": "0.000005"}},
		map[string]any{"id": "openai/gpt-4o-20240513", "canonical_slug": "openai/gpt-4o-20240513", "name": "GPT-4o May", "pricing": map[string]any{"prompt": "0.000005", "completion": "0.000015"}},
	}}
	raw, _ := json.Marshal(doc)
	models, diag, err := parseOpenRouter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(diag.Collisions) != 0 {
		t.Fatalf("batch route created collision: %v", diag.Collisions)
	}
	if got := models["gpt-4o"]["price_in"]; got != float64(2.5) {
		t.Fatalf("generic price = %v, want 2.5", got)
	}
	if got := models["gpt-4o-2024-05-13"]["price_in"]; got != float64(5) {
		t.Fatalf("dated price = %v, want 5", got)
	}
}

func TestEpochExplicitVersionMapping(t *testing.T) {
	data := zipFixture(t, map[string]string{
		"notable_ai_models.csv":  "Model,Parameters,Open model weights?\nGeneric Model,10,Yes\n",
		"frontier_ai_models.csv": "Model,Parameters,Open model weights?,Publication date,Organization,Model versions (benchmarks)\nGPT-4o,,No,2024-05-13,OpenAI,\"gpt-4o-2024-05-13,gpt-4o-2024-08-06\"\n",
	})
	models, err := parseEpochModels(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"gpt-4o-2024-05-13", "gpt-4o-2024-08-06"} {
		if models[key]["open_weights"] != false {
			t.Fatalf("explicit mapping absent for %s: %#v", key, models[key])
		}
	}
}

func TestQualityGateRejectsThinSnapshot(t *testing.T) {
	_, err := buildQuality("2026-08-08T00:00:00Z", map[string]any{}, nil, map[string]any{},
		parseDiagnostics{Benchmarks: map[string]map[string]any{}}, openRouterDiagnostics{}, previousCounts{}, "")
	if err == nil {
		t.Fatal("thin snapshot passed quality gate")
	}
}

func TestBenchmarkParserPreservesEvidenceFields(t *testing.T) {
	data := zipFixture(t, map[string]string{
		"gpqa_diamond.csv": "Model version,mean_score,Release date,Organization,Country,Training compute notes,stderr,Log viewer,Logs,Started at,Notes,id\n" +
			"model-a,0.7,2026-01-01,Test Lab,Canada,Estimated by source,0.03,https://logs.test/run-a,https://raw.test/run-a,2026-08-07T01:02:03.000Z,reviewed,a\n",
		"metr_time_horizons_external.csv": "Model version,Time horizon,Release date,Organization,CI_high,CI_low,METR version,id\n" +
			"model-b,120,2026-01-02,Test Lab,180,90,METR-Horizon-v1.1,b\n",
		"cursorbench_external.csv": "Model version,Score,Tokens per task,Steps per task,Release date,Organization,Notes,id\n" +
			"model-c,0.5,63842,76,2026-01-03,Test Lab,full run,c\n",
		"terminalbench_external.csv": "Model version,Accuracy mean,Accuracy SE,Release date,Organization,id\n" +
			"model-d,0.8,0.02,2026-01-04,Test Lab,d\n",
		"blueprint_bench_2_external.csv": "Model version,Score,Raw score standard error,Release date,Organization,id\n" +
			"model-e,0.6,0.04,2026-01-05,Test Lab,e\n",
	})
	benchmarks, scores, models, _, err := parseEpochBenchmarks(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 5 {
		t.Fatalf("got %d scores", len(scores))
	}
	byBench := map[string]map[string]any{}
	for _, score := range scores {
		byBench[score["b"].(string)] = score
	}
	gpqa := byBench["gpqa_diamond"]
	if gpqa["d"] != "2026-08-07" || gpqa["evaluated_at"] != "2026-08-07T01:02:03.000Z" || gpqa["trace"] != "https://logs.test/run-a" {
		t.Fatalf("timestamp/trace lost: %#v", gpqa)
	}
	if gpqa["notes"] != "reviewed" || models["model-a"]["country"] != "Canada" || models["model-a"]["flop_notes"] != "Estimated by source" {
		t.Fatalf("notes/model evidence lost: score=%#v model=%#v", gpqa, models["model-a"])
	}
	metr := byBench["metr_time_horizons"]
	if metr["lo"] != float64(90) || metr["hi"] != float64(180) || metr["bv"] != "METR-Horizon-v1.1" {
		t.Fatalf("METR evidence lost: %#v", metr)
	}
	metrics, _ := byBench["cursorbench"]["metrics"].(map[string]any)
	if metrics["total_tokens"] != int64(63842) || metrics["steps"] != int64(76) {
		t.Fatalf("resource metrics lost: %#v", metrics)
	}
	for _, id := range []string{"terminalbench", "blueprint_bench_2"} {
		if byBench[id]["se"] == nil {
			t.Fatalf("%s standard error lost: %#v", id, byBench[id])
		}
	}
	b, _ := benchmarks["gpqa_diamond"].(map[string]any)
	if b["dated_n"] != 1 || b["trace_n"] != 1 {
		t.Fatalf("benchmark evidence counts wrong: %#v", b)
	}
}
