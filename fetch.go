// Data pipeline: port of fetch.py. Pulls Epoch AI benchmark + models data and
// OpenRouter prices, joins by normalized slug, writes data/data.js and appends
// data/price_history.jsonl. Stdlib only; every datum stamped with provenance.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	epochBenchURL  = "https://epoch.ai/data/benchmark_data.zip"
	epochModelsURL = "https://epoch.ai/data/ai_models.zip"
	openrouterURL  = "https://openrouter.ai/api/v1/models"
)

// Column 2 is the score, unless col 2 is one of these, then col 3.
var nonScoreCol2 = map[string]bool{"Scaffold": true, "Tools": true, "Harness": true, "Agent": true}

var efforts = map[string]bool{
	"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true,
	"max": true, "promax": true, "best": true, "none": true, "unknown": true,
	"default": true, "thinking": true, "nonthinking": true,
}

var costCols = []string{"Mean cost (USD)", "Cost per task", "Total cost (USD)", "Cost"}
var evalDateCols = []string{"Date of evaluation", "Evaluation date", "Run date"}

// Pretty names for benchmarks whose auto-title-cased filename reads badly.
var benchNames = map[string]string{
	"epoch_capabilities_index": "Epoch Capabilities Index (ECI)",
	"deepswe":                  "DeepSWE",
	"ale_bench":                "ALE-Bench",
	"algotune":                 "AlgoTune",
	"apex_agents":              "APEX Agents",
	"cl_bench":                 "CL-Bench",
	"cl_bench_life":            "CL-Bench Life",
	"critpt":                   "CritPt",
	"vpct":                     "VPCT",
	"rli":                      "RLI",
	"btf3":                     "BTF3",
	"gso":                      "GSO",
	"hella_swag":               "HellaSwag",
	"swe_bench_verified":       "SWE-bench Verified",
	"terminalbench":            "Terminal-Bench",
	"aider_polyglot":           "Aider Polyglot",
	"gpqa_diamond":             "GPQA Diamond",
	"hle":                      "Humanity's Last Exam",
	"frontiermath":             "FrontierMath",
	"frontiermath_tier_4":      "FrontierMath Tier 4",
	"arc_agi_external":         "ARC-AGI",
	"arc_agi_2":                "ARC-AGI-2",
	"otis_mock_aime_2024_2025": "OTIS Mock AIME 24-25",
	"metr_time_horizons":       "METR Time Horizons",
	"simplebench":              "SimpleBench",
	"simpleqa_verified":        "SimpleQA Verified",
	"osworld_2":                "OSWorld 2",
	"os_world":                 "OSWorld",
	"scicode":                  "SciCode",
	"cybench":                  "Cybench",
	"mmlu":                     "MMLU",
	"gsm8k":                    "GSM8K",
	"math_level_5":             "MATH Level 5",
	"vending_bench_2":          "Vending-Bench 2",
	"weirdml":                  "WeirdML",
	"webdev_arena":             "WebDev Arena",
	"livebench":                "LiveBench",
	"live_bench":               "LiveBench",
	"balrog":                   "BALROG",
	"wino_grande":              "WinoGrande",
	"bbh":                      "BIG-Bench Hard",
	"gdpval":                   "GDPval",
}

// Benchmarks surfaced at the top of the picker; the rest sit under "All".
var featured = map[string]bool{
	"epoch_capabilities_index": true, "deepswe": true, "swe_bench_verified": true,
	"terminalbench": true, "aider_polyglot": true, "gpqa_diamond": true, "hle": true,
	"frontiermath": true, "arc_agi_2": true, "otis_mock_aime_2024_2025": true,
	"simplebench": true, "metr_time_horizons": true, "osworld_2": true,
	"scicode": true, "cybench": true, "gdpval": true,
}

var orgNorm = []struct{ key, canon string }{
	{"google", "Google DeepMind"}, {"meta", "Meta"}, {"moonshot", "Moonshot AI"},
	{"zhipu", "Z.ai"}, {"z.ai", "Z.ai"}, {"z-ai", "Z.ai"}, {"mistral", "Mistral"},
	{"alibaba", "Alibaba"}, {"deepseek", "DeepSeek"}, {"xai", "xAI"}, {"x.ai", "xAI"},
}

var acro = map[string]string{
	"gpt": "GPT", "oss": "OSS", "glm": "GLM", "ai": "AI", "xai": "xAI",
	"hf": "HF", "moe": "MoE", "qwq": "QwQ", "mpt": "MPT", "olmo": "OLMo",
	"minimax": "MiniMax", "deepseek": "DeepSeek", "openai": "OpenAI",
	"ernie": "ERNIE", "vl": "VL", "it": "IT",
}

var (
	reNonSlug      = regexp.MustCompile(`[^a-z0-9-]`)
	reDashes       = regexp.MustCompile(`-+`)
	reDateSuffix   = regexp.MustCompile(`-20\d{2}-?\d{2}-?\d{2}$`)
	reQualSuffix   = regexp.MustCompile(`-(preview|latest|exp)$`)
	reExternal     = regexp.MustCompile(`_external$`)
	rePrettyTail   = regexp.MustCompile(`-(20\d{6}|20\d{2}-\d{2}-\d{2}|\d{4})$`)
	reDigits       = regexp.MustCompile(`^\d+$`)
	reVersionish   = regexp.MustCompile(`(?i)^[a-z]*\d+(\.\d+)*$`)
	reSizeToken    = regexp.MustCompile(`^v?\d+[bmk]$`)
	reSluggyName   = regexp.MustCompile(`^[a-z0-9.\-_]+$`)
	reVendorPrefix = regexp.MustCompile(`^[^:]+:\s*`)
)

func fetchURL(url string) ([]byte, http.Header, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "pareto-fetch/1.0")
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	return body, resp.Header, err
}

// One display name per lab — sources disagree (Google vs Google DeepMind).
func normOrg(org string) string {
	low := strings.ToLower(org)
	for _, on := range orgNorm {
		if strings.Contains(low, on.key) {
			return on.canon
		}
	}
	return org
}

// Normalize a model identifier for cross-source joining.
func normSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(".", "-", " ", "-", "_", "-").Replace(s)
	s = reNonSlug.ReplaceAllString(s, "")
	s = strings.Trim(reDashes.ReplaceAllString(s, "-"), "-")
	s = reDateSuffix.ReplaceAllString(s, "")
	s = reQualSuffix.ReplaceAllString(s, "")
	return s
}

// "claude-opus-5_high" -> ("claude-opus-5", "high")
func splitEffort(mv string) (string, string) {
	if i := strings.LastIndex(mv, "_"); i >= 0 {
		base, tail := mv[:i], strings.ToLower(mv[i+1:])
		if efforts[tail] {
			return base, tail
		}
	}
	return mv, ""
}

func parseFloat(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	v = strings.NewReplacer(",", "", "%", "", "$", "").Replace(v)
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func benchDisplay(fid string) string {
	if n, ok := benchNames[fid]; ok {
		return n
	}
	words := strings.Split(strings.ReplaceAll(fid, "_", " "), " ")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// "claude-3-5-sonnet-20241022" -> "Claude 3.5 Sonnet" (fallback when no clean name).
func prettify(slug string) string {
	s := rePrettyTail.ReplaceAllString(slug, "")
	var parts []string
	for _, p := range strings.Split(s, "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for len(parts) > 0 && efforts[parts[len(parts)-1]] {
		parts = parts[:len(parts)-1]
		if len(parts) > 0 && parts[len(parts)-1] == "no" {
			parts = parts[:len(parts)-1]
		}
	}
	var out []string
	for _, t := range parts {
		switch {
		case len(out) > 0 && reDigits.MatchString(t) && reVersionish.MatchString(out[len(out)-1]):
			out[len(out)-1] += "." + t // claude 3 5 -> claude 3.5
		case acro[t] != "":
			out = append(out, acro[t])
		case reSizeToken.MatchString(t):
			out = append(out, strings.ToUpper(t)) // 405b -> 405B
		default:
			out = append(out, strings.ToUpper(t[:1])+t[1:])
		}
	}
	if len(out) == 0 {
		return slug
	}
	return strings.Join(out, " ")
}

// csvRows reads a CSV with BOM stripping and DictReader-like leniency.
func csvRows(data []byte) ([]string, [][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	all, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(all) == 0 {
		return nil, nil, nil
	}
	return all[0], all[1:], nil
}

// colIndex maps column name -> position; duplicates keep the last, like DictReader.
func colIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	return idx
}

func cell(row []string, idx map[string]int, col string) string {
	i, ok := idx[col]
	if !ok || i >= len(row) {
		return ""
	}
	return row[i]
}

func roundN(f float64, n int) float64 {
	p := math.Pow(10, float64(n))
	return math.Round(f*p) / p
}

// parseEpochBenchmarks returns (benchmarks, scores, modelMeta) from benchmark_data.zip.
func parseEpochBenchmarks(zipBytes []byte) (map[string]any, []map[string]any, map[string]map[string]any, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, nil, nil, err
	}
	benchmarks := map[string]any{}
	scores := []map[string]any{}
	modelMeta := map[string]map[string]any{}
	costSeen := map[string]bool{}

	var names []string
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		files[f.Name] = f
	}
	sort.Strings(names)

	for _, name := range names {
		if !strings.HasSuffix(name, ".csv") || strings.HasPrefix(name, "additional_") {
			continue
		}
		base := filepath.Base(name)
		if base == "" {
			continue
		}
		fid := reExternal.ReplaceAllString(strings.TrimSuffix(base, ".csv"), "")

		rc, err := files[name].Open()
		if err != nil {
			return nil, nil, nil, err
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, nil, nil, err
		}
		header, rows, err := csvRows(raw)
		if err != nil || len(rows) == 0 || len(header) == 0 || header[0] != "Model version" {
			continue
		}
		idx := colIndex(header)
		scoreCol := header[1]
		if len(header) > 2 && nonScoreCol2[header[1]] {
			scoreCol = header[2]
		}
		costCol := ""
		for _, c := range costCols {
			if _, ok := idx[c]; ok {
				costCol = c
				break
			}
		}
		evalCol := ""
		for _, c := range evalDateCols {
			if _, ok := idx[c]; ok {
				evalCol = c
				break
			}
		}

		n := 0
		latestEval := ""
		srcURL := ""
		for _, row := range rows {
			if srcURL == "" {
				for _, c := range []string{"Source link", "Source Link", "Source"} {
					if v := strings.TrimSpace(cell(row, idx, c)); strings.HasPrefix(v, "http") {
						srcURL = v
						break
					}
				}
			}
			mv := strings.TrimSpace(cell(row, idx, "Model version"))
			score, scoreOK := parseFloat(cell(row, idx, scoreCol))
			if mv == "" || !scoreOK {
				continue
			}
			baseSlug, effort := splitEffort(mv)
			slug := normSlug(baseSlug)
			if effort == "" {
				effort = strings.ToLower(strings.TrimSpace(cell(row, idx, "Reasoning effort")))
			}

			rec := map[string]any{"m": slug, "b": fid, "v": score}
			if effort != "" && effort != "unknown" {
				rec["e"] = effort
			}
			if costCol != "" {
				if cost, ok := parseFloat(cell(row, idx, costCol)); ok {
					rec["c"] = cost
					costSeen[fid] = true
				}
			}
			if tok, ok := parseFloat(cell(row, idx, "Mean output tokens")); ok && tok != 0 {
				rec["ot"] = int64(math.RoundToEven(tok))
			}
			if steps, ok := parseFloat(cell(row, idx, "Mean agent steps")); ok && steps != 0 {
				rec["st"] = roundN(steps, 1)
			}
			if evalCol != "" {
				if ed := strings.TrimSpace(cell(row, idx, evalCol)); ed != "" {
					rec["d"] = ed
					if ed > latestEval {
						latestEval = ed
					}
				}
			}
			scores = append(scores, rec)
			n++

			meta := modelMeta[slug]
			if meta == nil {
				meta = map[string]any{}
				modelMeta[slug] = meta
			}
			if rd := strings.TrimSpace(cell(row, idx, "Release date")); rd != "" && meta["release_date"] == nil {
				meta["release_date"] = rd
			}
			if org := strings.TrimSpace(cell(row, idx, "Organization")); org != "" && meta["org"] == nil {
				meta["org"] = normOrg(org)
			}
			if flop, ok := parseFloat(cell(row, idx, "Training compute (FLOP)")); ok && flop != 0 && meta["flop"] == nil {
				meta["flop"] = flop
			}
			if acc := cell(row, idx, "Model accessibility"); acc != "" {
				if _, ok := meta["open_weights"]; !ok {
					meta["open_weights"] = strings.Contains(strings.ToLower(acc), "open")
				}
			}
			disp := strings.TrimSpace(cell(row, idx, "Display name"))
			if disp == "" {
				disp = strings.TrimSpace(cell(row, idx, "Name"))
			}
			if disp != "" && meta["display"] == nil {
				// per-row display names often bake in effort; keep base-model-ish ones only
				if effort == "" || !strings.Contains(strings.ToLower(disp), effort) {
					meta["display"] = disp
				}
			}
		}

		if n > 0 {
			b := map[string]any{
				"id": fid, "name": benchDisplay(fid), "score_col": scoreCol, "n": n,
				"has_cost": costCol != "" && costSeen[fid],
				"featured": featured[fid],
			}
			if latestEval != "" {
				b["latest_eval"] = latestEval
			} else {
				b["latest_eval"] = nil
			}
			if srcURL != "" {
				b["source_url"] = srcURL
			} else {
				b["source_url"] = nil
			}
			benchmarks[fid] = b
		}
	}
	return benchmarks, scores, modelMeta, nil
}

// parseEpochModels: parameters + open-weights from notable_ai_models.csv, by normalized name.
func parseEpochModels(zipBytes []byte) (map[string]map[string]any, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	var raw []byte
	for _, f := range zr.File {
		if filepath.Base(f.Name) == "notable_ai_models.csv" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			raw, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			break
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("notable_ai_models.csv not in ai_models.zip")
	}
	header, rows, err := csvRows(raw)
	if err != nil {
		return nil, err
	}
	idx := colIndex(header)
	out := map[string]map[string]any{}
	for _, row := range rows {
		name := strings.TrimSpace(cell(row, idx, "Model"))
		if name == "" {
			continue
		}
		rec := map[string]any{}
		if params, ok := parseFloat(cell(row, idx, "Parameters")); ok && params != 0 {
			rec["params"] = params
		}
		if ow := strings.ToLower(strings.TrimSpace(cell(row, idx, "Open model weights?"))); ow != "" {
			rec["open_weights"] = strings.HasPrefix(ow, "yes") || strings.HasPrefix(ow, "open")
		}
		if len(rec) > 0 {
			out[normSlug(name)] = rec
		}
	}
	return out, nil
}

// parseOpenRouter: pricing/context/AA-index per normalized slug.
func parseOpenRouter(raw []byte) (map[string]map[string]any, error) {
	var doc struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	str := func(v any) string { s, _ := v.(string); return s }
	num := func(v any) (float64, bool) {
		switch t := v.(type) {
		case float64:
			return t, true
		case string:
			return parseFloat(t)
		}
		return 0, false
	}
	out := map[string]map[string]any{}
	for _, m := range doc.Data {
		mid := str(m["id"])
		if strings.HasPrefix(mid, "~") || strings.Contains(mid, ":free") {
			continue
		}
		pr, _ := m["pricing"].(map[string]any)
		pIn, okIn := num(pr["prompt"])
		pOut, okOut := num(pr["completion"])
		if !okIn || !okOut || (pIn == 0 && pOut == 0) {
			continue
		}
		rec := map[string]any{
			"or_id":     mid,
			"name":      m["name"],
			"price_in":  pIn * 1e6,
			"price_out": pOut * 1e6,
			"context":   m["context_length"],
			"created":   m["created"],
		}
		var aa map[string]any
		if b, ok := m["benchmarks"].(map[string]any); ok {
			aa, _ = b["artificial_analysis"].(map[string]any)
		}
		if len(aa) > 0 {
			rec["aa"] = aa
		}
		// index under both the id slug and the canonical slug — they differ
		// (id: claude-opus-4.8, canonical: claude-4.8-opus-20260528)
		canon := str(m["canonical_slug"])
		if canon == "" {
			canon = mid
		}
		keys := map[string]bool{
			normSlug(mid[strings.LastIndex(mid, "/")+1:]):     true,
			normSlug(canon[strings.LastIndex(canon, "/")+1:]): true,
		}
		for k := range keys {
			prev := out[k]
			if prev == nil || (len(aa) > 0 && prev["aa"] == nil) {
				out[k] = rec
			}
		}
	}
	return out, nil
}

func loadAliases(root string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "aliases.json"))
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	return out, json.Unmarshal(raw, &out)
}

const datacurveURL = "https://deepswe.datacurve.ai/artifacts/v1.1/leaderboard-live.json"

// Epoch ingests DeepSWE with a lag; Datacurve's live artifact adds new models,
// fresher runs, and confidence intervals. Org guesses only matter for models
// Epoch hasn't seen yet.
var dcOrgPrefix = []struct{ pre, org string }{
	{"claude", "Anthropic"}, {"gpt", "OpenAI"}, {"o1", "OpenAI"}, {"o3", "OpenAI"},
	{"o4", "OpenAI"}, {"gemini", "Google DeepMind"}, {"gemma", "Google DeepMind"},
	{"kimi", "Moonshot AI"}, {"qwen", "Alibaba"}, {"deepseek", "DeepSeek"},
	{"glm", "Z.ai"}, {"grok", "xAI"}, {"muse", "Meta"}, {"llama", "Meta"},
	{"mistral", "Mistral"},
}

// fetchDatacurveDeepSWE replaces Epoch's DeepSWE rows with the live v1.1
// leaderboard. Returns the spliced score list; any error or thin result leaves
// the Epoch rows untouched.
func fetchDatacurveDeepSWE(scores []map[string]any, benchmarks map[string]any, modelMeta map[string]map[string]any) ([]map[string]any, int, error) {
	raw, _, err := fetchURL(datacurveURL)
	if err != nil {
		return scores, 0, err
	}
	var doc struct {
		GeneratedAt string `json:"generated_at"`
		Rows        []struct {
			Model      string   `json:"model"`
			Effort     string   `json:"reasoning_effort"`
			PassAt1    *float64 `json:"pass_at_1"`
			CIHalf     *float64 `json:"ci_half"`
			MeanCost   *float64 `json:"mean_cost_usd"`
			MeanOutTok *float64 `json:"mean_output_tokens"`
			MeanSteps  *float64 `json:"mean_agent_steps"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return scores, 0, err
	}
	date := ""
	if len(doc.GeneratedAt) >= 10 {
		date = doc.GeneratedAt[:10]
	}
	var recs []map[string]any
	newMeta := map[string]map[string]any{}
	for _, r := range doc.Rows {
		if r.PassAt1 == nil || r.Model == "" {
			continue
		}
		slug := normSlug(r.Model)
		rec := map[string]any{"m": slug, "b": "deepswe", "v": roundN(*r.PassAt1, 4)}
		if e := strings.ToLower(r.Effort); e != "" && e != "unknown" {
			rec["e"] = e
		}
		if r.CIHalf != nil && *r.CIHalf > 0 {
			rec["ci"] = roundN(*r.CIHalf, 4)
		}
		if r.MeanCost != nil && *r.MeanCost > 0 {
			rec["c"] = roundN(*r.MeanCost, 4)
		}
		if r.MeanOutTok != nil && *r.MeanOutTok != 0 {
			rec["ot"] = int64(math.RoundToEven(*r.MeanOutTok))
		}
		if r.MeanSteps != nil && *r.MeanSteps != 0 {
			rec["st"] = roundN(*r.MeanSteps, 1)
		}
		if date != "" {
			rec["d"] = date
		}
		recs = append(recs, rec)
		if modelMeta[slug] == nil && newMeta[slug] == nil {
			meta := map[string]any{"display": prettify(slug)}
			for _, p := range dcOrgPrefix {
				if strings.HasPrefix(slug, p.pre) {
					meta["org"] = p.org
					break
				}
			}
			newMeta[slug] = meta
		}
	}
	// a thin artifact means something changed upstream — keep the Epoch rows
	if len(recs) < 20 {
		return scores, 0, fmt.Errorf("only %d usable rows in %s", len(recs), datacurveURL)
	}
	var out []map[string]any
	for _, s := range scores {
		if s["b"] != "deepswe" {
			out = append(out, s)
		}
	}
	out = append(out, recs...)
	for slug, meta := range newMeta {
		modelMeta[slug] = meta
	}
	hasCost := false
	for _, r := range recs {
		if _, ok := r["c"]; ok {
			hasCost = true
			break
		}
	}
	b, _ := benchmarks["deepswe"].(map[string]any)
	if b == nil {
		b = map[string]any{"id": "deepswe"}
		benchmarks["deepswe"] = b
	}
	b["name"] = "DeepSWE"
	b["score_col"] = "Pass@1"
	b["n"] = len(recs)
	b["has_cost"] = hasCost
	b["featured"] = featured["deepswe"]
	b["source_url"] = "https://deepswe.datacurve.ai/"
	b["via"] = "Datacurve (live)"
	if date != "" {
		b["latest_eval"] = date
	} else {
		b["latest_eval"] = nil
	}
	return out, len(recs), nil
}

// loadPricePrev picks a price baseline from price_history.jsonl for movement
// indicators: the newest snapshot at least 6 days old, else the oldest we have.
func loadPricePrev(dataDir string, now time.Time) map[string]any {
	raw, err := os.ReadFile(filepath.Join(dataDir, "price_history.jsonl"))
	if err != nil {
		return nil
	}
	type snap struct {
		Date   string               `json:"date"`
		Prices map[string][]float64 `json:"prices"`
	}
	var best, oldest *snap
	cut := now.Add(-6 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var s snap
		if json.Unmarshal(line, &s) != nil || s.Date == "" || len(s.Prices) == 0 {
			continue
		}
		if oldest == nil {
			cp := s
			oldest = &cp
		}
		if s.Date <= cut { // lines are chronological; the last one under the cutoff wins
			cp := s
			best = &cp
		}
	}
	pick := best
	if pick == nil {
		pick = oldest
	}
	if pick == nil {
		return nil
	}
	return map[string]any{"date": pick.Date, "prices": pick.Prices}
}

// fetchAndWrite runs the full pipeline and writes data/data.js + price_history.jsonl.
// Returns a human-readable report.
func fetchAndWrite(root string) (string, error) {
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	benchZip, benchHdr, err := fetchURL(epochBenchURL)
	if err != nil {
		return "", fmt.Errorf("epoch benchmarks: %w", err)
	}
	modelsZip, _, err := fetchURL(epochModelsURL)
	if err != nil {
		return "", fmt.Errorf("epoch models: %w", err)
	}
	orRaw, _, err := fetchURL(openrouterURL)
	if err != nil {
		return "", fmt.Errorf("openrouter: %w", err)
	}

	benchmarks, scores, modelMeta, err := parseEpochBenchmarks(benchZip)
	if err != nil {
		return "", fmt.Errorf("parse benchmarks: %w", err)
	}
	paramsByName, err := parseEpochModels(modelsZip)
	if err != nil {
		return "", fmt.Errorf("parse models: %w", err)
	}
	orBySlug, err := parseOpenRouter(orRaw)
	if err != nil {
		return "", fmt.Errorf("parse openrouter: %w", err)
	}
	aliases, err := loadAliases(root)
	if err != nil {
		return "", fmt.Errorf("aliases.json: %w", err)
	}

	dcNote := ""
	if spliced, n, dcErr := fetchDatacurveDeepSWE(scores, benchmarks, modelMeta); dcErr != nil {
		dcNote = fmt.Sprintf(" · deepswe: Datacurve fetch failed (%v), kept Epoch rows", dcErr)
	} else {
		scores = spliced
		dcNote = fmt.Sprintf(" · deepswe: %d live rows from Datacurve", n)
	}

	// ---- join: epoch model slug -> openrouter pricing, epoch params ----
	scoreCount := map[string]int{}
	for _, s := range scores {
		scoreCount[s["m"].(string)]++
	}
	matched := 0
	unmatched := map[string]int{}
	models := map[string]any{}
	for slug, meta := range modelMeta {
		disp, _ := meta["display"].(string)
		if disp == "" || reSluggyName.MatchString(disp) {
			src := disp
			if src == "" {
				src = slug
			}
			disp = prettify(src) // slug-looking name -> prettified fallback
		}
		m := map[string]any{}
		for k, v := range meta {
			m[k] = v
		}
		m["display"] = disp
		orSlug := slug
		if a, ok := aliases[slug]; ok {
			orSlug = a
		}
		if orm := orBySlug[orSlug]; orm != nil {
			matched++
			m["price_in"] = roundN(orm["price_in"].(float64), 4)
			m["price_out"] = roundN(orm["price_out"].(float64), 4)
			m["context"] = orm["context"]
			m["or_id"] = orm["or_id"]
			if nm, _ := orm["name"].(string); nm != "" {
				// OpenRouter names are clean ("Anthropic: Claude Opus 4.8");
				// Epoch's Name column is often a harness id
				m["display"] = reVendorPrefix.ReplaceAllString(nm, "")
			}
			if aa, ok := orm["aa"]; ok {
				m["aa"] = aa
			}
		} else {
			unmatched[slug] = scoreCount[slug]
		}
		pm := paramsByName[slug]
		if pm == nil {
			pm = paramsByName[normSlug(m["display"].(string))]
		}
		if pm != nil {
			if _, ok := m["params"]; !ok {
				m["params"] = pm["params"] // may be nil, matching setdefault(None)
			}
			if _, ok := m["open_weights"]; !ok {
				if ow, ok2 := pm["open_weights"]; ok2 {
					m["open_weights"] = ow
				}
			}
		}
		models[slug] = m
	}

	latestEval := ""
	for _, b := range benchmarks {
		if le, _ := b.(map[string]any)["latest_eval"].(string); le > latestEval {
			latestEval = le
		}
	}
	var latestEvalAny, etagAny any
	if latestEval != "" {
		latestEvalAny = latestEval
	}
	if etag := benchHdr.Get("Etag"); etag != "" {
		etagAny = etag
	}
	pricePrev := loadPricePrev(dataDir, time.Now().UTC())
	payload := map[string]any{
		"provenance": map[string]any{
			"fetched_at":  now,
			"epoch_etag":  etagAny,
			"latest_eval": latestEvalAny,
			"sources": map[string]any{
				"epoch_benchmarks": epochBenchURL,
				"epoch_models":     epochModelsURL,
				"openrouter":       openrouterURL,
			},
			"license_note": "Benchmark data: Epoch AI, 'AI Benchmarking Hub', CC-BY 4.0. Prices: OpenRouter.",
		},
		"benchmarks": benchmarks,
		"models":     models,
		"scores":     scores,
	}
	if pricePrev != nil {
		payload["price_prev"] = pricePrev
	}

	blob, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	outPath := filepath.Join(dataDir, "data.js")
	tmp := outPath + ".tmp"
	if err := os.WriteFile(tmp, append(append([]byte("window.PARETO = "), blob...), []byte(";\n")...), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, outPath); err != nil {
		return "", err
	}

	// append price snapshot for future time-travel
	prices := map[string]any{}
	for slug, mAny := range models {
		m := mAny.(map[string]any)
		if pi, ok := m["price_in"]; ok {
			prices[slug] = []any{pi, m["price_out"]}
		}
	}
	histLine, err := json.Marshal(map[string]any{"date": now, "prices": prices})
	if err == nil {
		if f, ferr := os.OpenFile(filepath.Join(dataDir, "price_history.jsonl"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
			f.Write(append(histLine, '\n'))
			f.Close()
		}
	}

	// ---- report ----
	var rep strings.Builder
	fmt.Fprintf(&rep, "wrote %s (%d KB) · benchmarks: %d · scores: %d · models: %d · priced via OpenRouter: %d/%d%s",
		outPath, (len(blob)+17)/1024, len(benchmarks), len(scores), len(models), matched, len(models), dcNote)
	type kv struct {
		slug string
		n    int
	}
	var top []kv
	for s, n := range unmatched {
		top = append(top, kv{s, n})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].n != top[j].n {
			return top[i].n > top[j].n
		}
		return top[i].slug < top[j].slug
	})
	if len(top) > 15 {
		top = top[:15]
	}
	if len(top) > 0 {
		rep.WriteString("\ntop unmatched (add to aliases.json if they should have prices):")
		for _, t := range top {
			fmt.Fprintf(&rep, "\n  %-45s %d scores", t.slug, t.n)
		}
	}
	return rep.String(), nil
}
