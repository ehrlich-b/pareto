// pareto: serves the explorer and periodically refreshes its own data snapshot.
// A failed refresh keeps the last good data/data.js and retries next tick.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8082", "listen address")
	root := flag.String("root", ".", "directory holding index.html and data/")
	every := flag.Duration("every", 6*time.Hour, "data refresh interval (0 disables)")
	oneshot := flag.Bool("fetch", false, "fetch data once, write data/data.js, and exit")
	flag.Parse()

	if *oneshot {
		rep, err := fetchAndWrite(*root)
		if err != nil {
			log.Fatalf("fetch failed: %v", err)
		}
		fmt.Println(rep)
		return
	}

	// refresh at startup only if the snapshot is missing or older than the interval
	if fi, err := os.Stat(filepath.Join(*root, "data", "data.js")); err != nil ||
		(*every > 0 && time.Since(fi.ModTime()) > *every) {
		go refresh(*root)
	}
	if *every > 0 {
		go func() {
			for range time.Tick(*every) {
				refresh(*root)
			}
		}()
	}

	fs := http.FileServer(http.Dir(*root))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve the page with a mtime-versioned data URL: Cloudflare rewrites our
		// no-cache to a 4h browser TTL on .js, so a fixed data/data.js URL goes
		// stale in browsers between refreshes — a new URL per snapshot cannot.
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			page, err := os.ReadFile(filepath.Join(*root, "index.html"))
			if err != nil {
				http.Error(w, "index.html unreadable", http.StatusInternalServerError)
				return
			}
			var v int64
			if fi, err := os.Stat(filepath.Join(*root, "data", "data.js")); err == nil {
				v = fi.ModTime().Unix()
			}
			page = bytes.Replace(page, []byte(`src="data/data.js"`),
				[]byte(fmt.Sprintf(`src="data/data.js?v=%d"`, v)), 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(page)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/data/") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fs.ServeHTTP(w, r)
	})
	log.Printf("pareto: serving %s on %s, refreshing every %s", *root, *addr, *every)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func refresh(root string) {
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
