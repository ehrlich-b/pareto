// pareto: serves the explorer, a read-only JSON API, and periodically refreshes
// its own data snapshot. A failed refresh keeps the last good publication.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var refreshMu sync.Mutex

type snapshotCache struct {
	mu      sync.Mutex
	modTime time.Time
	doc     map[string]any
}

func (c *snapshotCache) load(root string) (map[string]any, error) {
	path := filepath.Join(root, "data", "data.json")
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.doc != nil && fi.ModTime().Equal(c.modTime) {
		return c.doc, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	c.doc, c.modTime = doc, fi.ModTime()
	return doc, nil
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("json response: %v", err)
	}
}

func apiError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]any{"error": message, "status": status})
}

func queryInt(r *http.Request, name string, fallback, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n < 0 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

func copyRecord(in map[string]any, id string) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	if out["id"] == nil {
		out["id"] = id
	}
	return out
}

func newHandler(root string) http.Handler {
	mux := http.NewServeMux()
	cache := &snapshotCache{}
	fs := http.FileServer(http.Dir(root))

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusMethodNotAllowed, "GET or HEAD required")
			return
		}
		doc, err := cache.load(root)
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, "snapshot unavailable")
			return
		}
		quality, _ := doc["quality"].(map[string]any)
		jsonResponse(w, http.StatusOK, map[string]any{
			"status": "ok", "snapshot_status": quality["status"],
			"generated_at": quality["generated_at"], "schema_version": doc["schema_version"],
		})
	})

	mux.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusMethodNotAllowed, "GET or HEAD required")
			return
		}
		doc, err := cache.load(root)
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, "snapshot unavailable")
			return
		}
		provenance, _ := doc["provenance"].(map[string]any)
		benchmarks, _ := doc["benchmarks"].(map[string]any)
		models, _ := doc["models"].(map[string]any)
		scores, _ := doc["scores"].([]any)
		jsonResponse(w, http.StatusOK, map[string]any{
			"name": "AI Pareto Explorer API", "version": "v1", "schema_version": doc["schema_version"],
			"generated_at": provenance["fetched_at"],
			"counts":       map[string]int{"benchmarks": len(benchmarks), "models": len(models), "observations": len(scores)},
			"endpoints": map[string]string{
				"snapshot": "/api/v1/snapshot", "benchmarks": "/api/v1/benchmarks",
				"models": "/api/v1/models", "observations": "/api/v1/observations",
				"quality": "/api/v1/quality", "openapi": "/openapi.json",
			},
		})
	})

	mux.HandleFunc("/api/v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusMethodNotAllowed, "GET or HEAD required")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		http.ServeFile(w, r, filepath.Join(root, "data", "data.json"))
	})
	mux.HandleFunc("/api/v1/quality", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusMethodNotAllowed, "GET or HEAD required")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		http.ServeFile(w, r, filepath.Join(root, "data", "quality.json"))
	})

	mux.HandleFunc("/api/v1/benchmarks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusMethodNotAllowed, "GET or HEAD required")
			return
		}
		doc, err := cache.load(root)
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, "snapshot unavailable")
			return
		}
		all, _ := doc["benchmarks"].(map[string]any)
		wanted := strings.TrimSpace(r.URL.Query().Get("id"))
		ids := make([]string, 0, len(all))
		for id := range all {
			if wanted == "" || id == wanted {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			if rec, ok := all[id].(map[string]any); ok {
				items = append(items, copyRecord(rec, id))
			}
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"schema_version": doc["schema_version"], "count": len(items), "benchmarks": items,
		})
	})

	mux.HandleFunc("/api/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusMethodNotAllowed, "GET or HEAD required")
			return
		}
		doc, err := cache.load(root)
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, "snapshot unavailable")
			return
		}
		all, _ := doc["models"].(map[string]any)
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		org := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("org")))
		open := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("open")))
		ids := make([]string, 0, len(all))
		for id, raw := range all {
			rec, _ := raw.(map[string]any)
			display := fmt.Sprint(rec["display"])
			if q != "" && !strings.Contains(strings.ToLower(id+" "+display), q) {
				continue
			}
			if org != "" && !strings.Contains(strings.ToLower(fmt.Sprint(rec["org"])), org) {
				continue
			}
			if open == "true" && rec["open_weights"] != true || open == "false" && rec["open_weights"] != false {
				continue
			}
			ids = append(ids, id)
		}
		sort.Strings(ids)
		total := len(ids)
		offset, limit := queryInt(r, "offset", 0, total), queryInt(r, "limit", 100, 1000)
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		items := make([]map[string]any, 0, end-offset)
		for _, id := range ids[offset:end] {
			items = append(items, copyRecord(all[id].(map[string]any), id))
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"schema_version": doc["schema_version"], "total": total, "offset": offset, "limit": limit, "models": items,
		})
	})

	mux.HandleFunc("/api/v1/observations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusMethodNotAllowed, "GET or HEAD required")
			return
		}
		doc, err := cache.load(root)
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, "snapshot unavailable")
			return
		}
		all, _ := doc["scores"].([]any)
		benchmark := strings.TrimSpace(r.URL.Query().Get("benchmark"))
		model := strings.TrimSpace(r.URL.Query().Get("model"))
		matched := make([]map[string]any, 0)
		for _, raw := range all {
			rec, _ := raw.(map[string]any)
			if benchmark != "" && rec["b"] != benchmark || model != "" && rec["m"] != model {
				continue
			}
			matched = append(matched, rec)
		}
		total := len(matched)
		offset, limit := queryInt(r, "offset", 0, total), queryInt(r, "limit", 100, 1000)
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		schema, _ := doc["schema"].(map[string]any)
		jsonResponse(w, http.StatusOK, map[string]any{
			"schema_version": doc["schema_version"], "schema": schema,
			"total": total, "offset": offset, "limit": limit, "observations": matched[offset:end],
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "GET or HEAD required", http.StatusMethodNotAllowed)
			return
		}
		// Serve the page with a mtime-versioned data URL: intermediary caches may
		// retain .js, while a new snapshot URL is unambiguously fresh.
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			page, err := os.ReadFile(filepath.Join(root, "index.html"))
			if err != nil {
				http.Error(w, "index.html unreadable", http.StatusInternalServerError)
				return
			}
			var v int64
			if fi, err := os.Stat(filepath.Join(root, "data", "data.js")); err == nil {
				v = fi.ModTime().Unix()
			}
			page = bytes.Replace(page, []byte(`src="data/data.js"`),
				[]byte(fmt.Sprintf(`src="data/data.js?v=%d"`, v)), 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(page)
			return
		}
		if r.URL.Path == "/data/" {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/data/") {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		fs.ServeHTTP(w, r)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/data/data.json" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		mux.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8082", "listen address")
	root := flag.String("root", ".", "directory holding index.html and data/")
	every := flag.Duration("every", 6*time.Hour, "data refresh interval (0 disables)")
	oneShot := flag.Bool("fetch", false, "fetch data once, write data/data.{json,js}, and exit")
	flag.Parse()

	if *oneShot {
		rep, err := fetchAndWrite(*root)
		if err != nil {
			log.Fatalf("fetch failed: %v", err)
		}
		fmt.Println(rep)
		return
	}

	// Refresh at startup only if the snapshot is missing or older than the interval.
	if fi, err := os.Stat(filepath.Join(*root, "data", "data.js")); err != nil ||
		(*every > 0 && time.Since(fi.ModTime()) > *every) {
		go refresh(*root)
	}
	if *every > 0 {
		go func() {
			ticker := time.NewTicker(*every)
			defer ticker.Stop()
			for range ticker.C {
				refresh(*root)
			}
		}()
	}

	server := &http.Server{
		Addr: *addr, Handler: newHandler(*root), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute,
	}
	log.Printf("pareto: serving %s on %s, refreshing every %s", *root, *addr, *every)
	log.Fatal(server.ListenAndServe())
}

func refresh(root string) {
	if !refreshMu.TryLock() {
		log.Printf("refresh skipped: another refresh is already running")
		return
	}
	defer refreshMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("refresh panicked (keeping last snapshot): %v", r)
		}
	}()
	rep, err := fetchAndWrite(root)
	if err != nil {
		log.Printf("refresh failed (keeping last snapshot): %v", err)
		return
	}
	log.Printf("refresh ok: %s", rep)
}
