package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func apiFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"schema_version": 2,
		"provenance":     map[string]any{"fetched_at": "2026-08-08T00:00:00Z"},
		"quality":        map[string]any{"status": "pass", "generated_at": "2026-08-08T00:00:00Z"},
		"benchmarks":     map[string]any{"bench-a": map[string]any{"id": "bench-a", "name": "Bench A", "n": 1}},
		"models":         map[string]any{"model-a": map[string]any{"display": "Model A", "org": "Test Lab", "open_weights": true}},
		"scores":         []any{map[string]any{"oid": "obs-a", "b": "bench-a", "m": "model-a", "v": 0.5}},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "data.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAPIIndexAndFilters(t *testing.T) {
	h := newHandler(apiFixture(t))
	for _, tc := range []struct {
		path string
		key  string
	}{
		{"/api/v1", "endpoints"},
		{"/api/v1/benchmarks?id=bench-a", "benchmarks"},
		{"/api/v1/models?q=model&open=true", "models"},
		{"/api/v1/observations?benchmark=bench-a", "observations"},
		{"/healthz", "snapshot_status"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", tc.path, rr.Code, rr.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out[tc.key] == nil {
			t.Fatalf("%s: missing %s in %s (%v)", tc.path, tc.key, rr.Body.String(), err)
		}
		if tc.path != "/healthz" && rr.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: CORS header missing", tc.path)
		}
	}
}

func TestAPIMethodAndPagination(t *testing.T) {
	h := newHandler(apiFixture(t))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/models", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/models?offset=99&limit=10000", nil))
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["offset"] != float64(1) || out["limit"] != float64(1000) {
		t.Fatalf("unexpected pagination: %#v", out)
	}
}
