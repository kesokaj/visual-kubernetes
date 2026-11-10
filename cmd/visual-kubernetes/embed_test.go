package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// testDist stands in for a page build: the shell and one hashed asset.
var testDist = fstest.MapFS{
	"index.html":         {Data: []byte("<!doctype html><title>shell</title>")},
	"assets/app-abc.js":  {Data: []byte("console.log(1)")},
	"assets/nested/x.js": {Data: []byte("x")},
}

func apiStub() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "api "+r.URL.Path)
	})
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestWithPageRoutes(t *testing.T) {
	h := withPage(apiStub(), testDist)
	cases := []struct {
		name, method, target string
		code                 int
		body                 string // substring
		cache                string
	}{
		{"root serves the shell", "GET", "/", 200, "<title>shell</title>", "no-cache"},
		{"client route falls back to the shell", "GET", "/apps/shop/web", 200, "<title>shell</title>", "no-cache"},
		{"hashed asset is served immutable", "GET", "/assets/app-abc.js", 200, "console.log(1)", "public, max-age=31536000, immutable"},
		{"directory falls back to the shell", "GET", "/assets/nested/", 200, "<title>shell</title>", "no-cache"},
		{"traversal is redirected to a clean path", "GET", "/../../etc/passwd", 307, "/etc/passwd", ""},
		{"stream goes to the api", "GET", "/api/stream", 200, "api /api/stream", ""},
		{"objects go to the api", "GET", "/api/objects?id=x", 200, "api /api/objects", ""},
		{"healthz goes to the api", "GET", "/healthz", 200, "api /healthz", ""},
		{"writes are refused", "POST", "/", 405, "method not allowed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := get(t, h, c.method, c.target)
			if rec.Code != c.code {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, c.code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), c.body) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), c.body)
			}
			if got := rec.Header().Get("Cache-Control"); got != c.cache {
				t.Errorf("Cache-Control = %q, want %q", got, c.cache)
			}
		})
	}
}

func TestPageHeadersKeepRequestsOnOrigin(t *testing.T) {
	rec := get(t, withPage(apiStub(), testDist), "GET", "/")
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestPageNotBuilt(t *testing.T) {
	h := withPage(apiStub(), nil)
	rec := get(t, h, "GET", "/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pnpm -C web build") {
		t.Fatalf("got %d %q, want the not-built page", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "GET", "/healthz"); rec.Body.String() != "api /healthz" {
		t.Errorf("healthz without a page = %q, want the api", rec.Body.String())
	}
}

// TestEmbedDirMatchesViteOutDir pins the go:embed directory to the client outDir in
// web/vite.config.ts, so a changed build config cannot silently ship a binary without the page.
func TestEmbedDirMatchesViteOutDir(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "vite.config.ts"))
	if err != nil {
		t.Fatalf("reading the build config: %v", err)
	}
	m := regexp.MustCompile(`(?m)^export const clientOutDir = '([^']+)'`).FindSubmatch(src)
	if m == nil {
		t.Fatal("web/vite.config.ts has no `export const clientOutDir = '...'` line")
	}
	got, err := filepath.Abs(filepath.Join("..", "..", "web", string(m[1])))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.FromSlash(uiDistDir))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("vite client outDir resolves to %q, embed reads %q", got, want)
	}
}

// TestPageHandlerConfinesPaths calls the page handler without the mux (which would redirect
// first) so a dot-dot path reaches path.Clean and still resolves inside the build.
func TestPageHandlerConfinesPaths(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.URL.Path = "/../../assets/app-abc.js"
	pageHandler(testDist).ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Fatalf("got %d %q, want the asset served from inside the build", rec.Code, rec.Body.String())
	}
}
