package demo

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxHTMLRewriteBytes = 2 << 20
	maxProxyBodyBytes   = 8 << 20
)

var htmlAttrRe = regexp.MustCompile(`(?i)([\s"'<>])(href|src|action)(\s*=\s*)(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))`)

func scrubOutboundHeaders(h http.Header) {
	for _, k := range []string{"Cookie", "Authorization", "Proxy-Authorization", "Accept-Encoding", "Forwarded", "X-Forwarded-For", "X-Real-Ip"} {
		h.Del(k)
	}
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "x-goog-") {
			h.Del(k)
		}
	}
}

func hardenProxyResponse(resp *http.Response, prefix string, isHead bool) error {
	for _, k := range []string{"Set-Cookie", "Set-Cookie2", "X-Frame-Options", "Content-Security-Policy", "Content-Security-Policy-Report-Only", "Clear-Site-Data"} {
		resp.Header.Del(k)
	}
	reqPath := "/"
	if resp.Request != nil && resp.Request.URL != nil && resp.Request.URL.Path != "" {
		reqPath = cleanUpstreamPath(resp.Request.URL.Path)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		if rewritten, ok := rewriteLocation(loc, reqPath, prefix); ok {
			resp.Header.Set("Location", rewritten)
		} else {
			resp.Header.Del("Location")
		}
	}
	resp.Header.Set("Content-Security-Policy", ProxyCSP)
	resp.Header.Set("X-Content-Type-Options", "nosniff")
	resp.Header.Set("Referrer-Policy", "no-referrer")
	resp.Header.Set("Cache-Control", "no-store")
	if isHead || resp.Body == nil {
		return nil
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return rewriteHTMLResponse(resp, reqPath, prefix)
	}
	if resp.ContentLength > maxProxyBodyBytes {
		_ = resp.Body.Close()
		return errors.New("upstream response exceeds 8 MiB limit")
	}
	resp.Body = &cappedReadCloser{rc: resp.Body, remain: maxProxyBodyBytes}
	return nil
}

func cleanUpstreamPath(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	trailing := len(raw) > 1 && strings.HasSuffix(raw, "/")
	c := path.Clean(raw)
	if !strings.HasPrefix(c, "/") {
		c = "/" + c
	}
	if trailing && c != "/" {
		c += "/"
	}
	return c
}

// resolveProxiedURL resolves a relative or root-relative URL against reqPath, cleans dot
// segments, and clamps the result strictly beneath prefix+"/". Scheme/host/network-path URLs
// return false.
func resolveProxiedURL(raw, reqPath, prefix string) (string, bool) {
	if raw == "" || strings.ContainsAny(raw, "\r\n") || strings.HasPrefix(raw, "//") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
		return "", false
	}
	if u.Path == prefix {
		u.Path = "/"
	} else if strings.HasPrefix(u.Path, prefix+"/") {
		u.Path = strings.TrimPrefix(u.Path, prefix)
	}
	resolved := (&url.URL{Path: cleanUpstreamPath(reqPath)}).ResolveReference(u)
	inner := cleanUpstreamPath(resolved.Path)
	out := prefix + inner
	if resolved.RawQuery != "" {
		out += "?" + resolved.RawQuery
	}
	if resolved.Fragment != "" {
		out += "#" + resolved.Fragment
	}
	return out, true
}

func rewriteLocation(loc, reqPath, prefix string) (string, bool) {
	return resolveProxiedURL(strings.TrimSpace(loc), reqPath, prefix)
}

func rewriteHTMLResponse(resp *http.Response, reqPath, prefix string) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLRewriteBytes+1))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if len(raw) > maxHTMLRewriteBytes {
		return errors.New("upstream HTML exceeds 2 MiB limit")
	}
	out := rewriteHTMLPaths(raw, reqPath, prefix)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.Header.Del("Content-Encoding")
	return nil
}

func rewriteHTMLPaths(src []byte, reqPath, prefix string) []byte {
	cur := src
	for range 2 {
		next := htmlAttrRe.ReplaceAllFunc(cur, func(m []byte) []byte {
			sub := htmlAttrRe.FindSubmatch(m)
			if len(sub) < 7 {
				return m
			}
			quote, val := extractAttrValue(sub)
			trimmed := strings.TrimSpace(val)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "?") {
				return m
			}
			rewritten, ok := resolveProxiedURL(trimmed, reqPath, prefix)
			if !ok {
				return m
			}
			return []byte(string(sub[1]) + string(sub[2]) + string(sub[3]) + quote + rewritten + quote)
		})
		if bytes.Equal(next, cur) {
			break
		}
		cur = next
	}
	return cur
}

func extractAttrValue(sub [][]byte) (quote, val string) {
	switch {
	case sub[4] != nil:
		return `"`, string(sub[4])
	case sub[5] != nil:
		return `'`, string(sub[5])
	default:
		return `"`, string(sub[6])
	}
}

type cappedReadCloser struct {
	rc     io.ReadCloser
	remain int64
}

func (c *cappedReadCloser) Read(p []byte) (int, error) {
	if c.remain <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > c.remain {
		p = p[:c.remain]
	}
	n, err := c.rc.Read(p)
	c.remain -= int64(n)
	return n, err
}

func (c *cappedReadCloser) Close() error { return c.rc.Close() }
