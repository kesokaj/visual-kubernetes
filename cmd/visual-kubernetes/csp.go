package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
)

// scriptTag matches one <script> element: its attributes and its body.
var scriptTag = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)

// srcAttr spots a script loaded from a file: those are covered by 'self', not a hash.
var srcAttr = regexp.MustCompile(`(?i)(^|\s)src\s*=`)

// pageCSP builds the policy for the page shell in index. Every request stays on this origin
// (PRD R9). Scripts run only from this origin or, for the inline bootstrap the prerendered
// TanStack shell carries, when their exact bytes hash to a value listed here: no
// 'unsafe-inline' for scripts, so injected markup cannot run code. Inline styles stay allowed
// (React writes style attributes); they cannot execute script or reach another origin.
func pageCSP(index []byte) string {
	script := append([]string{"'self'"}, inlineScriptHashes(index)...)
	return "default-src 'self'; script-src " + strings.Join(script, " ") + "; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; " +
		"object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
}

// inlineScriptHashes lists the CSP sha256 source of each non-empty inline script in index.
func inlineScriptHashes(index []byte) []string {
	var out []string
	for _, m := range scriptTag.FindAllSubmatch(index, -1) {
		if srcAttr.Match(m[1]) || len(bytes.TrimSpace(m[2])) == 0 {
			continue
		}
		sum := sha256.Sum256(m[2])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}
