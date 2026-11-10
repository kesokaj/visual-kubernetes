package main

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// scriptSrc returns the script-src directive of a policy.
func scriptSrc(t *testing.T, csp string) string {
	t.Helper()
	for _, d := range strings.Split(csp, ";") {
		if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src ") {
			return d
		}
	}
	t.Fatalf("CSP %q has no script-src", csp)
	return ""
}

// TestPageCSPAllowsOnlyHashedInlineScripts: the shell's own inline bootstrap runs by hash,
// and nothing else inline does (security review phase 3, finding 3).
func TestPageCSPAllowsOnlyHashedInlineScripts(t *testing.T) {
	shell := `<!doctype html><script data-tsr-stream-part="">self.$R=1</script>` +
		`<script type="module" async src="/assets/a.js"></script><script> </script>` +
		`<SCRIPT>b()</SCRIPT>`
	dist := fstest.MapFS{"index.html": {Data: []byte(shell)}}
	rec := get(t, withPage(apiStub(), dist), "GET", "/")
	got := scriptSrc(t, rec.Header().Get("Content-Security-Policy"))
	want := "script-src 'self' " + hashOf("self.$R=1") + " " + hashOf("b()")
	if got != want {
		t.Errorf("script-src = %q\nwant        %q", got, want)
	}
	if strings.Contains(got, "unsafe-inline") {
		t.Errorf("script-src %q allows any inline script", got)
	}
}

// TestShellCSPFailsClosed: a build whose shell cannot be read gets no script hashes.
func TestShellCSPFailsClosed(t *testing.T) {
	got := scriptSrc(t, shellCSP(fstest.MapFS{}))
	if got != "script-src 'self'" {
		t.Errorf("unreadable shell: script-src = %q, want only 'self'", got)
	}
}

// activeSVG matches markup that could run code or link anywhere from an icon. Any href is
// refused outright (the icons link nothing), so obfuscated schemes need no special case.
var activeSVG = regexp.MustCompile(`(?i)<\s*script|<\s*foreignobject|\son[a-z]+\s*=|href\s*=`)

// activeIcons names the SVG files in icons that carry active or remote content.
func activeIcons(t *testing.T, icons fs.FS) []string {
	t.Helper()
	names, err := fs.Glob(icons, "*.svg")
	if err != nil || len(names) == 0 {
		t.Fatalf("no icons found (err %v)", err)
	}
	var bad []string
	for _, n := range names {
		src, err := fs.ReadFile(icons, n)
		if err != nil {
			t.Fatal(err)
		}
		if activeSVG.Match(src) {
			bad = append(bad, n)
		}
	}
	return bad
}

// TestVendoredIconsCarryNoActiveContent pins the premise of the icon sprite, which injects
// the vendored SVGs as markup: they hold shapes only, no scripts, handlers or remote links.
func TestVendoredIconsCarryNoActiveContent(t *testing.T) {
	planted := fstest.MapFS{
		"ok.svg":     {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`)},
		"script.svg": {Data: []byte(`<svg><script>alert(1)</script></svg>`)},
		"onload.svg": {Data: []byte(`<svg onload="alert(1)"></svg>`)},
		"remote.svg": {Data: []byte(`<svg><use href="https://x.example/a.svg#i"/></svg>`)},
		"case.svg":   {Data: []byte(`<svg><Script>alert(1)</Script></svg>`)},
		"scheme.svg": {Data: []byte(`<svg><a xlink:href="java&#9;script:alert(1)"><path/></a></svg>`)},
	}
	if got := strings.Join(activeIcons(t, planted), ","); got != "case.svg,onload.svg,remote.svg,scheme.svg,script.svg" {
		t.Fatalf("check is blind: flagged %q, want case,onload,remote,scheme,script", got)
	}
	icons := os.DirFS(filepath.Join("..", "..", "web", "src", "icons"))
	if bad := activeIcons(t, icons); len(bad) > 0 {
		t.Errorf("vendored icons with active or remote content: %v", bad)
	}
}
