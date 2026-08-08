// Data pipeline: port of fetch.py. Pulls Epoch AI benchmark + models data and
// OpenRouter prices, joins by normalized slug, writes data/data.js and appends
// data/price_history.jsonl. Stdlib only; every datum stamped with provenance.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
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

var efforts = map[string]bool{
	"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true,
	"max": true, "promax": true, "best": true, "none": true, "unknown": true,
	"default": true, "thinking": true, "nonthinking": true,
}

var evalDateCols = []string{"Date of evaluation", "Evaluation date", "Run date"}

// Epoch's benchmark bundle has no machine-readable schema. Keep the score
// contract explicit so a new metadata column cannot silently become a score.
// Unknown benchmarks are quarantined and surfaced in the quality report.
var benchmarkScoreCols = map[string]string{
	"adversarial_nli":          "Score",
	"aider_polyglot":           "Percent correct",
	"ale_bench":                "Performance",
	"algotune":                 "Score",
	"apex_agents":              "Pass@1 score",
	"arc_agi_2":                "Score",
	"arc_agi":                  "Score",
	"arc_ai2":                  "Challenge score",
	"balrog":                   "Average progress",
	"bbh":                      "Average",
	"blueprint_bench_2":        "Score",
	"bool_q":                   "Score",
	"btf3":                     "Pooled score",
	"cad_eval":                 "Overall pass (%)",
	"chess_puzzles":            "mean_score",
	"cl_bench":                 "Overall",
	"cl_bench_life":            "Overall",
	"common_sense_qa_2":        "Score",
	"critpt":                   "Accuracy",
	"cursorbench":              "Score",
	"cybench":                  "Unguided % Solved",
	"deepresearchbench":        "Average score",
	"deepswe":                  "Pass@1",
	"enigma_eval":              "Accuracy",
	"epoch_capabilities_index": "ECI Score",
	"exploitbench":             "Mean capability",
	"fictionlivebench":         "120k token score",
	"forecastbench":            "Overall score",
	"frontiercode":             "Main score",
	"frontiermath":             "mean_score",
	"frontiermath_tier_4":      "mean_score",
	"frontierswe":              "Dominance",
	"gbaeval":                  "Overall score",
	"gdp_pdf":                  "GDP.pdf score",
	"gdpval":                   "Win Rate (%)",
	"geobench":                 "ACW Avg Score",
	"gpqa_diamond":             "mean_score",
	"gsm8k":                    "EM",
	"gso":                      "Score OPT@1",
	"hella_swag":               "Overall accuracy",
	"hle":                      "Accuracy",
	"lambada":                  "Score",
	"lech_mazur_writing":       "Mean score",
	"live_bench":               "Global average",
	"math_level_5":             "mean_score",
	"metr_time_horizons":       "Time horizon",
	"mindcube":                 "Overall score",
	"mmlu":                     "EM",
	"mystery_game_puzzles":     "mean_score",
	"open_book_qa":             "Accuracy",
	"os_world":                 "Score",
	"osworld_2":                "Binary accuracy",
	"otis_mock_aime_2024_2025": "mean_score",
	"piqa":                     "Score",
	"posttrainbench":           "Average (%)",
	"proofbench":               "Accuracy",
	"rli":                      "Score",
	"scicode":                  "Score",
	"science_qa":               "Score",
	"simplebench":              "Score (AVG@5)",
	"simpleqa_verified":        "mean_score",
	"spatialviz_bench":         "Overall score",
	"superglue":                "Score",
	"surface_evolver_bench":    "Mean score",
	"swe_bench_verified":       "mean_score",
	"terminalbench":            "Accuracy mean",
	"the_agent_company":        "% Score",
	"trivia_qa":                "EM",
	"vending_bench_2":          "Score",
	"video_mme":                "Overall (no subtitles)",
	"vpct":                     "Correct",
	"webdev_arena":             "Arena Score",
	"weirdml":                  "Accuracy",
	"wino_grande":              "Accuracy",
}

type costSpec struct {
	Column string
	Label  string
	Basis  string
}

// Costs stay in their upstream denominator. The viewer no longer calls totals,
// runs, or ambiguous source fields "$ per task".
var benchmarkCosts = map[string]costSpec{
	"aider_polyglot":        {"Cost", "Reported cost (USD)", "reported"},
	"ale_bench":             {"Cost", "Reported cost (USD)", "reported"},
	"arc_agi":               {"Cost per task", "$ per task", "task"},
	"arc_agi_2":             {"Cost per task", "$ per task", "task"},
	"critpt":                {"Cost", "Reported cost (USD)", "reported"},
	"cursorbench":           {"Cost per task", "$ per task", "task"},
	"deepswe":               {"Mean cost (USD)", "Mean $ per task", "task"},
	"osworld_2":             {"Estimated cost (USD)", "Estimated cost (USD)", "estimated"},
	"proofbench":            {"Cost per test (USD)", "$ per test", "test"},
	"surface_evolver_bench": {"Total cost (USD)", "Total run cost (USD)", "total"},
	"the_agent_company":     {"Average costs", "Average reported cost (USD)", "average"},
	"weirdml":               {"Cost per run", "$ per run", "run"},
}

type uncertaintySpec struct {
	Kind string
	A    string
	B    string
	C    string
}

var benchmarkUncertainty = map[string]uncertaintySpec{
	"apex_agents":              {Kind: "se", A: "Pass@1 Standard Error"},
	"balrog":                   {Kind: "se", A: "Average Standard error"},
	"btf3":                     {Kind: "ci", A: "Pooled 95% CI low", B: "Pooled 95% CI high"},
	"chess_puzzles":            {Kind: "se", A: "stderr"},
	"cl_bench":                 {Kind: "sd", A: "Overall std dev"},
	"cl_bench_life":            {Kind: "sd", A: "Overall std dev"},
	"deepswe":                  {Kind: "half", A: "95% CI half-width"},
	"enigma_eval":              {Kind: "ci", A: "CI Lower Bound", B: "CI Upper Bound"},
	"forecastbench":            {Kind: "ci", A: "Overall 95% CI low", B: "Overall 95% CI high"},
	"frontiermath":             {Kind: "se", A: "stderr"},
	"frontiermath_tier_4":      {Kind: "se", A: "stderr"},
	"gpqa_diamond":             {Kind: "se", A: "stderr"},
	"hle":                      {Kind: "se", A: "Accuracy Standard Error"},
	"math_level_5":             {Kind: "se", A: "stderr"},
	"mystery_game_puzzles":     {Kind: "se", A: "stderr"},
	"otis_mock_aime_2024_2025": {Kind: "se", A: "stderr"},
	"proofbench":               {Kind: "se", A: "Accuracy Standard Error"},
	"simpleqa_verified":        {Kind: "se", A: "stderr"},
	"swe_bench_verified":       {Kind: "se", A: "stderr"},
	"webdev_arena":             {Kind: "ci", A: "95% CI Low", B: "95% CI High", C: "Score 95% CI"},
}

var configColumns = []struct{ Column, Key string }{
	{"Scaffold", "scaffold"}, {"Tools", "tools"}, {"Harness", "harness"},
	{"Agent", "agent"}, {"Reasoning effort", "effort"}, {"Reasoning level", "effort"},
	{"Reasoning", "reasoning"}, {"Tool setting", "tools"}, {"Step budget", "budget"},
	{"Shots", "shots"}, {"Provider", "provider"}, {"Edit format", "format"},
}

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
	reCompactDate  = regexp.MustCompile(`-(20\d{2})(\d{2})(\d{2})$`)
	reQualSuffix   = regexp.MustCompile(`-(preview|latest|exp)$`)
	reExternal     = regexp.MustCompile(`_external$`)
	rePrettyTail   = regexp.MustCompile(`-(20\d{6}|20\d{2}-\d{2}-\d{2}|\d{4})$`)
	reDigits       = regexp.MustCompile(`^\d+$`)
	reVersionish   = regexp.MustCompile(`(?i)^[a-z]*\d+(\.\d+)*$`)
	reSizeToken    = regexp.MustCompile(`^v?\d+[bmk]$`)
	reSluggyName   = regexp.MustCompile(`^[a-z0-9.\-_]+$`)
	reVendorPrefix = regexp.MustCompile(`^[^:]+:\s*`)
	rePlusMinus    = regexp.MustCompile(`^=?\+([0-9.]+)\s*/\s*-([0-9.]+)$`)
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

// modelKey normalizes punctuation without erasing a dated revision or a
// preview qualifier. It is the lossless identity used by score rows.
func modelKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(".", "-", " ", "-", "_", "-").Replace(s)
	s = reNonSlug.ReplaceAllString(s, "")
	s = strings.Trim(reDashes.ReplaceAllString(s, "-"), "-")
	s = reCompactDate.ReplaceAllString(s, "-$1-$2-$3")
	return s
}

// familySlug is only a grouping/candidate key. It must never be used as the
// identity of an observation or as an unqualified cross-source join.
func familySlug(s string) string {
	s = modelKey(s)
	s = reDateSuffix.ReplaceAllString(s, "")
	s = reQualSuffix.ReplaceAllString(s, "")
	return s
}

// normSlug remains as a compatibility name for exact punctuation
// normalization. Unlike the old implementation, it preserves revisions.
func normSlug(s string) string { return modelKey(s) }

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

func parseDateValue(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			if t.After(time.Now().UTC().Add(24 * time.Hour)) {
				return "", false
			}
			return t.UTC().Format("2006-01-02"), true
		}
	}
	return "", false
}

func inferEffort(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for e := range efforts {
		if e == "unknown" || e == "none" || e == "default" {
			continue
		}
		if strings.HasSuffix(name, "("+e+")") || strings.HasSuffix(name, " "+e) {
			return e
		}
	}
	return ""
}

func observationID(fid string, row []string) string {
	sum := sha256.Sum256([]byte(fid + "\x00" + strings.Join(row, "\x1f")))
	return fmt.Sprintf("%s-%x", fid, sum[:8])
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

type parseDiagnostics struct {
	Warnings      []string
	Quarantined   []string
	InvalidDates  int
	MetaConflicts int
	Benchmarks    map[string]map[string]any
}

func (d *parseDiagnostics) warn(s string) {
	for _, prev := range d.Warnings {
		if prev == s {
			return
		}
	}
	if len(d.Warnings) < 100 {
		d.Warnings = append(d.Warnings, s)
	}
}

func setMeta(meta map[string]any, key string, value any, slug string, d *parseDiagnostics) {
	if value == nil || value == "" {
		return
	}
	if prev, ok := meta[key]; ok {
		if a, okA := prev.(float64); okA {
			if b, okB := value.(float64); okB {
				scale := math.Max(math.Abs(a), math.Abs(b))
				if math.Abs(a-b) <= math.Max(1, scale)*1e-12 {
					return
				}
			}
		}
		if fmt.Sprint(prev) == fmt.Sprint(value) {
			return
		}
		msg := fmt.Sprintf("model metadata conflict: %s %s=%v/%v", slug, key, prev, value)
		seen := false
		for _, w := range d.Warnings {
			seen = seen || w == msg
		}
		if !seen {
			d.MetaConflicts++
			d.warn(msg)
		}
		return
	}
	if _, ok := meta[key]; !ok {
		meta[key] = value
	}
}

func addUncertainty(rec map[string]any, row []string, idx map[string]int, spec uncertaintySpec) bool {
	switch spec.Kind {
	case "ci":
		lo, okLo := parseFloat(cell(row, idx, spec.A))
		hi, okHi := parseFloat(cell(row, idx, spec.B))
		if okLo && okHi && lo <= hi {
			rec["lo"], rec["hi"] = lo, hi
			return true
		}
		if spec.C != "" {
			m := rePlusMinus.FindStringSubmatch(strings.TrimSpace(cell(row, idx, spec.C)))
			v, valueOK := rec["v"].(float64)
			if len(m) == 3 && valueOK {
				plus, okPlus := parseFloat(m[1])
				minus, okMinus := parseFloat(m[2])
				if okPlus && okMinus {
					rec["lo"], rec["hi"] = v-minus, v+plus
					return true
				}
			}
		}
	case "half":
		if v, ok := parseFloat(cell(row, idx, spec.A)); ok && v > 0 {
			rec["ci"] = v
			return true
		}
	case "se", "sd":
		if v, ok := parseFloat(cell(row, idx, spec.A)); ok && v >= 0 {
			rec[spec.Kind] = v
			return true
		}
	}
	return false
}

func rowSourceURL(row []string, idx map[string]int) string {
	for _, c := range []string{"Source link", "Source Link", "Source URL"} {
		if v := strings.TrimSpace(cell(row, idx, c)); strings.HasPrefix(v, "http") {
			return v
		}
	}
	if v := strings.TrimSpace(cell(row, idx, "Source")); strings.HasPrefix(v, "http") {
		return v
	}
	return ""
}

func rowBenchmarkVersion(row []string, idx map[string]int) string {
	for _, c := range []string{"Benchmark version", "LiveBench Version", "Version", "Dataset version"} {
		if v := strings.TrimSpace(cell(row, idx, c)); v != "" {
			return v
		}
	}
	return ""
}

// parseEpochBenchmarks returns lossless observations and revision-safe model
// metadata from benchmark_data.zip. Unsupported schemas are quarantined and
// reported rather than guessed positionally.
func parseEpochBenchmarks(zipBytes []byte) (map[string]any, []map[string]any, map[string]map[string]any, parseDiagnostics, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, nil, nil, parseDiagnostics{}, err
	}
	benchmarks := map[string]any{}
	scores := []map[string]any{}
	modelMeta := map[string]map[string]any{}
	diag := parseDiagnostics{Benchmarks: map[string]map[string]any{}}

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
			return nil, nil, nil, diag, err
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, nil, nil, diag, err
		}
		header, rows, err := csvRows(raw)
		if err != nil {
			return nil, nil, nil, diag, fmt.Errorf("%s: %w", name, err)
		}
		if len(rows) == 0 || len(header) == 0 || header[0] != "Model version" {
			diag.Quarantined = append(diag.Quarantined, fid)
			diag.warn(fmt.Sprintf("quarantined %s: empty data or missing Model version", fid))
			continue
		}
		idx := colIndex(header)
		scoreCol, known := benchmarkScoreCols[fid]
		if !known || scoreCol == "" {
			diag.Quarantined = append(diag.Quarantined, fid)
			diag.warn(fmt.Sprintf("quarantined %s: no approved score schema", fid))
			continue
		}
		if _, ok := idx[scoreCol]; !ok {
			diag.Quarantined = append(diag.Quarantined, fid)
			diag.warn(fmt.Sprintf("quarantined %s: expected score column %q missing", fid, scoreCol))
			continue
		}
		cost := benchmarkCosts[fid]
		evalCol := ""
		for _, c := range evalDateCols {
			if _, ok := idx[c]; ok {
				evalCol = c
				break
			}
		}

		n, datedN, costN, uncertaintyN := 0, 0, 0, 0
		latestEval := ""
		srcURL := ""
		versions := map[string]bool{}
		for _, row := range rows {
			rowSrc := rowSourceURL(row, idx)
			if srcURL == "" && rowSrc != "" {
				srcURL = rowSrc
			}
			mv := strings.TrimSpace(cell(row, idx, "Model version"))
			score, scoreOK := parseFloat(cell(row, idx, scoreCol))
			if mv == "" || !scoreOK {
				continue
			}
			baseSlug, effort := splitEffort(mv)
			slug := modelKey(baseSlug)
			if effort == "" {
				effort = strings.ToLower(strings.TrimSpace(cell(row, idx, "Reasoning effort")))
			}
			if effort == "" {
				effort = strings.ToLower(strings.TrimSpace(cell(row, idx, "Reasoning level")))
			}
			disp := strings.TrimSpace(cell(row, idx, "Display name"))
			if disp == "" {
				disp = strings.TrimSpace(cell(row, idx, "Name"))
			}
			if effort == "" {
				effort = inferEffort(disp)
			}

			rec := map[string]any{
				"oid": observationID(fid, row), "m": slug, "b": fid, "v": score, "sm": mv,
			}
			if sid := strings.TrimSpace(cell(row, idx, "id")); sid != "" {
				rec["sid"] = sid
			} else if sid := strings.TrimSpace(cell(row, idx, "UUID")); sid != "" {
				rec["sid"] = sid
			}
			if disp != "" {
				rec["dn"] = disp
			}
			if effort != "" && effort != "unknown" {
				rec["e"] = effort
			}
			cfg := map[string]any{}
			for _, cc := range configColumns {
				if v := strings.TrimSpace(cell(row, idx, cc.Column)); v != "" && strings.ToLower(v) != "unknown" {
					if _, exists := cfg[cc.Key]; !exists {
						cfg[cc.Key] = v
					}
				}
			}
			if effort != "" && effort != "unknown" {
				cfg["effort"] = effort
			}
			if len(cfg) > 0 {
				rec["cfg"] = cfg
			}
			if cost.Column != "" {
				if amount, ok := parseFloat(cell(row, idx, cost.Column)); ok && amount >= 0 {
					rec["c"] = amount
					costN++
				}
			}
			if spec, ok := benchmarkUncertainty[fid]; ok && addUncertainty(rec, row, idx, spec) {
				uncertaintyN++
			}
			if tok, ok := parseFloat(cell(row, idx, "Mean output tokens")); ok && tok != 0 {
				rec["ot"] = int64(math.RoundToEven(tok))
			}
			if steps, ok := parseFloat(cell(row, idx, "Mean agent steps")); ok && steps != 0 {
				rec["st"] = roundN(steps, 1)
			}
			if evalCol != "" {
				if ed := strings.TrimSpace(cell(row, idx, evalCol)); ed != "" {
					if date, ok := parseDateValue(ed); ok {
						rec["d"] = date
						datedN++
						if date > latestEval {
							latestEval = date
						}
					} else {
						diag.InvalidDates++
						diag.warn(fmt.Sprintf("invalid evaluation date in %s: %q", fid, ed))
					}
				}
			}
			if rowSrc != "" {
				rec["src"] = rowSrc
			}
			if bv := rowBenchmarkVersion(row, idx); bv != "" {
				rec["bv"] = bv
				versions[bv] = true
			}
			scores = append(scores, rec)
			n++

			meta := modelMeta[slug]
			if meta == nil {
				meta = map[string]any{}
				modelMeta[slug] = meta
			}
			if rd := strings.TrimSpace(cell(row, idx, "Release date")); rd != "" {
				if date, ok := parseDateValue(rd); ok {
					rec["rd"] = date
					setMeta(meta, "release_date", date, slug, &diag)
				} else {
					diag.InvalidDates++
					diag.warn(fmt.Sprintf("invalid release date for %s: %q", slug, rd))
				}
			}
			if org := strings.TrimSpace(cell(row, idx, "Organization")); org != "" {
				setMeta(meta, "org", normOrg(org), slug, &diag)
			}
			if flop, ok := parseFloat(cell(row, idx, "Training compute (FLOP)")); ok && flop != 0 {
				setMeta(meta, "flop", flop, slug, &diag)
			}
			if acc := cell(row, idx, "Model accessibility"); acc != "" {
				setMeta(meta, "open_weights", strings.Contains(strings.ToLower(acc), "open"), slug, &diag)
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
				"has_cost": costN > 0, "dated_n": datedN, "uncertainty_n": uncertaintyN,
				"featured": featured[fid], "source_status": "ok",
			}
			if costN > 0 {
				b["cost_col"], b["cost_label"], b["cost_basis"] = cost.Column, cost.Label, cost.Basis
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
			if len(versions) > 0 {
				var vv []string
				for v := range versions {
					vv = append(vv, v)
				}
				sort.Strings(vv)
				b["versions"] = vv
			}
			benchmarks[fid] = b
			diag.Benchmarks[fid] = map[string]any{
				"source_rows": len(rows), "parsed_rows": n, "dated_rows": datedN,
				"cost_rows": costN, "uncertainty_rows": uncertaintyN, "score_col": scoreCol,
			}
		}
	}
	return benchmarks, scores, modelMeta, diag, nil
}

// parseEpochModels enriches exact model revisions from both the direct model
// name and Epoch's explicit "Model versions (benchmarks)" mapping.
func parseEpochModels(zipBytes []byte) (map[string]map[string]any, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	raws := map[string][]byte{}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base == "notable_ai_models.csv" || base == "frontier_ai_models.csv" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			raw, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			raws[base] = raw
		}
	}
	if raws["notable_ai_models.csv"] == nil {
		return nil, fmt.Errorf("notable_ai_models.csv not in ai_models.zip")
	}
	out := map[string]map[string]any{}
	merge := func(key string, rec map[string]any) {
		if key == "" || len(rec) == 0 {
			return
		}
		if out[key] == nil {
			out[key] = map[string]any{}
		}
		for k, v := range rec {
			if _, exists := out[key][k]; !exists {
				out[key][k] = v
			}
		}
	}
	for _, filename := range []string{"notable_ai_models.csv", "frontier_ai_models.csv"} {
		raw := raws[filename]
		if raw == nil {
			continue
		}
		header, rows, err := csvRows(raw)
		if err != nil {
			return nil, err
		}
		idx := colIndex(header)
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
			if rd, ok := parseDateValue(cell(row, idx, "Publication date")); ok {
				rec["release_date"] = rd
			}
			if org := strings.TrimSpace(cell(row, idx, "Organization")); org != "" {
				rec["org"] = normOrg(org)
			}
			if flop, ok := parseFloat(cell(row, idx, "Training compute (FLOP)")); ok && flop > 0 {
				rec["flop"] = flop
			}
			merge(modelKey(name), rec)
			if filename == "frontier_ai_models.csv" {
				for _, v := range strings.Split(cell(row, idx, "Model versions (benchmarks)"), ",") {
					merge(modelKey(v), rec)
				}
			}
		}
	}
	return out, nil
}

type openRouterDiagnostics struct {
	Records    int
	Standard   int
	Collisions []string
}

// parseOpenRouter indexes only ordinary (non-batch/non-free/non-thinking)
// routes by exact id and exact canonical revision. Ambiguous exact keys are
// withheld instead of allowing API order or AA metadata to choose a price.
func parseOpenRouter(raw []byte) (map[string]map[string]any, openRouterDiagnostics, error) {
	var doc struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, openRouterDiagnostics{}, err
	}
	diag := openRouterDiagnostics{Records: len(doc.Data)}
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
	owners := map[string]string{}
	ambiguous := map[string]bool{}
	for _, m := range doc.Data {
		mid := str(m["id"])
		last := mid[strings.LastIndex(mid, "/")+1:]
		if strings.HasPrefix(mid, "~") || strings.Contains(last, ":") {
			continue
		}
		pr, _ := m["pricing"].(map[string]any)
		pIn, okIn := num(pr["prompt"])
		pOut, okOut := num(pr["completion"])
		if !okIn || !okOut || (pIn == 0 && pOut == 0) {
			continue
		}
		diag.Standard++
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
		// Exact id and exact canonical revision are both valid join keys, but
		// neither is collapsed into a model-family key.
		canon := str(m["canonical_slug"])
		if canon == "" {
			canon = mid
		}
		keys := map[string]bool{
			modelKey(mid[strings.LastIndex(mid, "/")+1:]):     true,
			modelKey(canon[strings.LastIndex(canon, "/")+1:]): true,
		}
		for k := range keys {
			if ambiguous[k] {
				continue
			}
			if owner := owners[k]; owner != "" && owner != mid {
				diag.Collisions = append(diag.Collisions, fmt.Sprintf("%s: %s / %s", k, owner, mid))
				delete(out, k)
				ambiguous[k] = true
				continue
			}
			owners[k] = mid
			out[k] = rec
		}
	}
	sort.Strings(diag.Collisions)
	return out, diag, nil
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
	generated, genErr := time.Parse(time.RFC3339, doc.GeneratedAt)
	if genErr == nil {
		if generated.After(time.Now().UTC().Add(24*time.Hour)) || time.Since(generated) > 7*24*time.Hour {
			return scores, 0, fmt.Errorf("generated_at is not current: %s", doc.GeneratedAt)
		}
		date = generated.UTC().Format("2006-01-02")
	} else {
		return scores, 0, fmt.Errorf("invalid generated_at %q", doc.GeneratedAt)
	}
	var recs []map[string]any
	newMeta := map[string]map[string]any{}
	uniqueModels := map[string]bool{}
	costN, ciN := 0, 0
	for _, r := range doc.Rows {
		if r.PassAt1 == nil || r.Model == "" || *r.PassAt1 < 0 || *r.PassAt1 > 1 {
			continue
		}
		slug := modelKey(r.Model)
		uniqueModels[slug] = true
		identity := []string{r.Model, r.Effort, fmt.Sprint(r.PassAt1), fmt.Sprint(r.MeanCost)}
		rec := map[string]any{
			"oid": observationID("deepswe-datacurve", identity), "m": slug, "b": "deepswe",
			"v": roundN(*r.PassAt1, 4), "sm": r.Model, "src": "https://deepswe.datacurve.ai/",
			"bv": "v1.1",
		}
		if e := strings.ToLower(r.Effort); e != "" && e != "unknown" {
			rec["e"] = e
			rec["cfg"] = map[string]any{"effort": e}
		}
		if r.CIHalf != nil && *r.CIHalf > 0 {
			rec["ci"] = roundN(*r.CIHalf, 4)
			ciN++
		}
		if r.MeanCost != nil && *r.MeanCost > 0 {
			rec["c"] = roundN(*r.MeanCost, 4)
			costN++
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
	// Reject partial or semantically degraded live artifacts. The Epoch fallback
	// remains available, but its degraded status is emitted to the viewer.
	if len(recs) < 40 || len(uniqueModels) < 15 {
		return scores, 0, fmt.Errorf("thin artifact: %d rows / %d models", len(recs), len(uniqueModels))
	}
	if float64(ciN)/float64(len(recs)) < 0.8 || float64(costN)/float64(len(recs)) < 0.8 {
		return scores, 0, fmt.Errorf("incomplete artifact: CI %d/%d, cost %d/%d", ciN, len(recs), costN, len(recs))
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
	b["cost_col"] = "mean_cost_usd"
	b["cost_label"] = "Mean $ per task"
	b["cost_basis"] = "task"
	b["dated_n"] = len(recs)
	b["uncertainty_n"] = ciN
	b["featured"] = featured["deepswe"]
	b["source_url"] = "https://deepswe.datacurve.ai/"
	b["via"] = "Datacurve (live)"
	b["source_status"] = "ok"
	b["versions"] = []string{"v1.1"}
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

type previousCounts struct {
	Benchmarks map[string]int
	Scores     int
	Models     int
}

func loadPreviousCounts(dataDir string) previousCounts {
	out := previousCounts{Benchmarks: map[string]int{}}
	raw, err := os.ReadFile(filepath.Join(dataDir, "data.js"))
	if err != nil {
		return out
	}
	raw = bytes.TrimSpace(raw)
	raw = bytes.TrimPrefix(raw, []byte("window.PARETO = "))
	raw = bytes.TrimSuffix(raw, []byte(";"))
	var doc struct {
		Benchmarks map[string]struct {
			N int `json:"n"`
		} `json:"benchmarks"`
		Models map[string]any `json:"models"`
		Scores []any          `json:"scores"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	for id, b := range doc.Benchmarks {
		out.Benchmarks[id] = b.N
	}
	out.Scores, out.Models = len(doc.Scores), len(doc.Models)
	return out
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return roundN(100*float64(n)/float64(d), 1)
}

func buildQuality(now string, benchmarks map[string]any, scores []map[string]any, models map[string]any,
	parseDiag parseDiagnostics, orDiag openRouterDiagnostics, previous previousCounts, dcNote string) (map[string]any, error) {
	warnings := append([]string{}, parseDiag.Warnings...)
	for _, q := range parseDiag.Quarantined {
		warnings = append(warnings, "quarantined benchmark: "+q)
	}
	for _, c := range orDiag.Collisions {
		warnings = append(warnings, "ambiguous OpenRouter key withheld: "+c)
	}
	if dcNote != "" {
		warnings = append(warnings, dcNote)
	}

	oids := map[string]bool{}
	duplicateIDs, dated, costed, uncertain := 0, 0, 0, 0
	for _, s := range scores {
		if id, _ := s["oid"].(string); id == "" || oids[id] {
			duplicateIDs++
		} else {
			oids[id] = true
		}
		if s["d"] != nil {
			dated++
		}
		if s["c"] != nil {
			costed++
		}
		if s["ci"] != nil || s["lo"] != nil || s["se"] != nil || s["sd"] != nil {
			uncertain++
		}
	}

	coverageKeys := []string{"org", "release_date", "open_weights", "flop", "params", "price_in", "context"}
	coverage := map[string]any{
		"eval_date":   map[string]any{"rows": dated, "total": len(scores), "percent": pct(dated, len(scores))},
		"cost":        map[string]any{"rows": costed, "total": len(scores), "percent": pct(costed, len(scores))},
		"uncertainty": map[string]any{"rows": uncertain, "total": len(scores), "percent": pct(uncertain, len(scores))},
	}
	for _, key := range coverageKeys {
		n := 0
		for _, s := range scores {
			m, _ := models[s["m"].(string)].(map[string]any)
			if m != nil && m[key] != nil {
				n++
			}
		}
		coverage[key] = map[string]any{"rows": n, "total": len(scores), "percent": pct(n, len(scores))}
	}

	var gateFailures []string
	if len(benchmarks) < 60 {
		gateFailures = append(gateFailures, fmt.Sprintf("only %d benchmarks", len(benchmarks)))
	}
	if len(scores) < 4000 {
		gateFailures = append(gateFailures, fmt.Sprintf("only %d scores", len(scores)))
	}
	if len(models) < 400 {
		gateFailures = append(gateFailures, fmt.Sprintf("only %d models", len(models)))
	}
	if duplicateIDs > 0 {
		gateFailures = append(gateFailures, fmt.Sprintf("%d duplicate/missing observation ids", duplicateIDs))
	}
	if previous.Scores > 0 && len(scores) < previous.Scores*80/100 {
		gateFailures = append(gateFailures, fmt.Sprintf("score count fell from %d to %d", previous.Scores, len(scores)))
	}
	if len(previous.Benchmarks) > 0 && len(benchmarks) < len(previous.Benchmarks)*95/100 {
		gateFailures = append(gateFailures, fmt.Sprintf("benchmark count fell from %d to %d", len(previous.Benchmarks), len(benchmarks)))
	}
	for id, prevN := range previous.Benchmarks {
		if prevN < 10 || id == "deepswe" {
			continue
		}
		b, _ := benchmarks[id].(map[string]any)
		curN, _ := b["n"].(int)
		if b == nil || curN < prevN*60/100 {
			gateFailures = append(gateFailures, fmt.Sprintf("%s rows fell from %d to %d", id, prevN, curN))
		}
	}
	if len(gateFailures) > 0 {
		return nil, fmt.Errorf("quality gate failed: %s", strings.Join(gateFailures, "; "))
	}

	status := "pass"
	if len(warnings) > 0 || parseDiag.InvalidDates > 0 || parseDiag.MetaConflicts > 0 {
		status = "warn"
	}
	return map[string]any{
		"status": status, "generated_at": now,
		"counts": map[string]any{
			"benchmarks": len(benchmarks), "scores": len(scores), "models": len(models),
			"quarantined": len(parseDiag.Quarantined), "invalid_dates": parseDiag.InvalidDates,
			"metadata_conflicts": parseDiag.MetaConflicts, "openrouter_collisions": len(orDiag.Collisions),
		},
		"coverage": coverage, "benchmarks": parseDiag.Benchmarks, "warnings": warnings,
		"gates": map[string]any{"status": "pass", "failures": []string{}},
	}, nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

	benchmarks, scores, modelMeta, parseDiag, err := parseEpochBenchmarks(benchZip)
	if err != nil {
		return "", fmt.Errorf("parse benchmarks: %w", err)
	}
	paramsByName, err := parseEpochModels(modelsZip)
	if err != nil {
		return "", fmt.Errorf("parse models: %w", err)
	}
	orBySlug, orDiag, err := parseOpenRouter(orRaw)
	if err != nil {
		return "", fmt.Errorf("parse openrouter: %w", err)
	}
	aliases, err := loadAliases(root)
	if err != nil {
		return "", fmt.Errorf("aliases.json: %w", err)
	}

	dcReport, dcWarning := "", ""
	if spliced, n, dcErr := fetchDatacurveDeepSWE(scores, benchmarks, modelMeta); dcErr != nil {
		dcReport = fmt.Sprintf(" · deepswe: Datacurve rejected (%v), kept Epoch rows", dcErr)
		dcWarning = fmt.Sprintf("DeepSWE live source rejected; using Epoch fallback: %v", dcErr)
		if b, _ := benchmarks["deepswe"].(map[string]any); b != nil {
			b["source_status"] = "degraded"
			b["source_error"] = dcErr.Error()
			b["via"] = "Epoch AI (fallback)"
		}
	} else {
		scores = spliced
		dcReport = fmt.Sprintf(" · deepswe: %d validated live rows from Datacurve", n)
		if b, _ := benchmarks["deepswe"].(map[string]any); b != nil {
			parseDiag.Benchmarks["deepswe"] = map[string]any{
				"source_rows": n, "parsed_rows": n, "dated_rows": n, "cost_rows": n,
				"uncertainty_rows": n, "score_col": "Pass@1", "via": "Datacurve (live)",
			}
		}
	}

	// ---- join: epoch model slug -> openrouter pricing, epoch params ----
	scoreCount := map[string]int{}
	for _, s := range scores {
		scoreCount[s["m"].(string)]++
	}
	matched, aliasMatched := 0, 0
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
		m["family"] = familySlug(slug)
		orSlug := slug
		joinMethod := ""
		if a, ok := aliases[slug]; ok {
			orSlug = a
			if i := strings.LastIndex(orSlug, "/"); i >= 0 {
				orSlug = orSlug[i+1:]
			}
			orSlug = modelKey(orSlug)
			joinMethod = "alias"
		}
		if orm := orBySlug[orSlug]; orm != nil {
			matched++
			if joinMethod == "alias" {
				aliasMatched++
			} else {
				joinMethod = "exact"
			}
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
			m["price_match"] = joinMethod
		} else {
			unmatched[slug] = scoreCount[slug]
		}
		pm := paramsByName[slug]
		if pm == nil {
			pm = paramsByName[normSlug(m["display"].(string))]
		}
		if pm != nil {
			for _, key := range []string{"params", "open_weights", "release_date", "org", "flop"} {
				if _, exists := m[key]; !exists && pm[key] != nil {
					m[key] = pm[key]
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
	previous := loadPreviousCounts(dataDir)
	quality, err := buildQuality(now, benchmarks, scores, models, parseDiag, orDiag, previous, dcWarning)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"provenance": map[string]any{
			"fetched_at":  now,
			"epoch_etag":  etagAny,
			"latest_eval": latestEvalAny,
			"sources": map[string]any{
				"epoch_benchmarks":  epochBenchURL,
				"epoch_models":      epochModelsURL,
				"openrouter":        openrouterURL,
				"datacurve_deepswe": datacurveURL,
			},
			"license_note": "Benchmark data: Epoch AI, 'AI Benchmarking Hub', CC-BY 4.0. Prices: OpenRouter.",
		},
		"benchmarks": benchmarks,
		"models":     models,
		"scores":     scores,
		"quality":    quality,
	}
	if pricePrev != nil {
		payload["price_prev"] = pricePrev
	}

	blob, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	outPath := filepath.Join(dataDir, "data.js")
	qualityPath := filepath.Join(dataDir, "quality.json")
	historyPath := filepath.Join(dataDir, "price_history.jsonl")

	// Prepare the price history and both output artifacts before committing the
	// new data.js last. data.js is the publication marker used by the server.
	prices := map[string]any{}
	for slug, mAny := range models {
		m := mAny.(map[string]any)
		if pi, ok := m["price_in"]; ok {
			prices[slug] = []any{pi, m["price_out"]}
		}
	}
	histLine, err := json.Marshal(map[string]any{"date": now, "prices": prices})
	if err != nil {
		return "", err
	}
	history, err := os.ReadFile(historyPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	history = append(history, append(histLine, '\n')...)
	qualityBlob, err := json.MarshalIndent(quality, "", "  ")
	if err != nil {
		return "", err
	}
	outputs := []struct {
		path string
		data []byte
	}{
		{historyPath, history},
		{qualityPath, append(qualityBlob, '\n')},
		{outPath, append(append([]byte("window.PARETO = "), blob...), []byte(";\n")...)},
	}
	for _, o := range outputs {
		if err := os.WriteFile(o.path+".tmp", o.data, 0o644); err != nil {
			return "", err
		}
	}
	for _, o := range outputs {
		if err := os.Rename(o.path+".tmp", o.path); err != nil {
			return "", err
		}
	}

	// ---- report ----
	var rep strings.Builder
	fmt.Fprintf(&rep, "wrote %s (%d KB) · quality: %s · benchmarks: %d · scores: %d · models: %d · priced via OpenRouter: %d/%d (%d aliases)%s",
		outPath, (len(blob)+17)/1024, quality["status"], len(benchmarks), len(scores), len(models), matched, len(models), aliasMatched, dcReport)
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
