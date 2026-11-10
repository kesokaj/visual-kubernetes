package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// uiFiles holds the page. `pnpm -C web build` writes the client build into ui/dist (the
// client outDir in web/vite.config.ts, pinned by TestEmbedDirMatchesViteOutDir); ui/dist is
// gitignored, so a fresh clone still builds and serves notBuiltPage instead.
//
//go:embed all:ui
var uiFiles embed.FS

// uiDistDir is where the client build lands inside uiFiles.
const uiDistDir = "ui/dist"

// notBuiltPage answers `/` when the binary was built without the page.
const notBuiltPage = "visual-kubernetes: this binary was built without the page; " +
	"run `pnpm -C web build` and rebuild. The stream is at /api/stream\n"

// distFS is the embedded client build, or nil when the binary has none.
func distFS() fs.FS {
	sub, err := fs.Sub(uiFiles, uiDistDir)
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// withPage serves the API paths from api and every other path from the page in dist: a
// file when one matches, else index.html so client routes survive a reload.
func withPage(api http.Handler, dist fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/healthz", api)
	mux.Handle("/", pageHandler(dist))
	return mux
}

// pageHandler serves the SPA. It answers GET and HEAD only; the page is read-only like
// everything else here.
func pageHandler(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	csp := shellCSP(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if dist == nil {
			h.Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(notBuiltPage)) // a failed write means the client left
			return
		}
		h.Set("Content-Security-Policy", csp)
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if isFile(dist, name) && name != "index.html" {
			if strings.HasPrefix(name, "assets/") {
				h.Set("Cache-Control", "public, max-age=31536000, immutable") // hashed names
			}
			files.ServeHTTP(w, r)
			return
		}
		h.Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, dist, "index.html")
	})
}

// shellCSP hashes the inline scripts of the build's index.html once. An unreadable shell gets
// a policy with no hashes: its inline scripts are blocked rather than allowed blind.
func shellCSP(dist fs.FS) string {
	if dist == nil {
		return pageCSP(nil)
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return pageCSP(nil)
	}
	return pageCSP(index)
}

// isFile reports whether name is a regular file in dist (directories fall back to the page).
func isFile(dist fs.FS, name string) bool {
	if name == "" {
		return false
	}
	st, err := fs.Stat(dist, name)
	return err == nil && st.Mode().IsRegular()
}
