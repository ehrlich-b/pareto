// pareto: serves the explorer and periodically refreshes its own data snapshot.
// A failed refresh keeps the last good data/data.js and retries next tick.
package main

import (
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
		// the app rewrites the page and snapshot in place — clients must revalidate
		if r.URL.Path == "/" || r.URL.Path == "/index.html" || strings.HasPrefix(r.URL.Path, "/data/") {
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
