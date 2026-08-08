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
