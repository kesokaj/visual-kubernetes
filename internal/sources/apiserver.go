package sources

import (
	"bufio"
	"bytes"
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

// readyzResult is the API server's verbose readiness report.
type readyzResult struct {
	reached bool            // a check list was parsed, whatever the HTTP status
	checks  map[string]bool // check name → passed
}

// failed lists the failing checks, sorted.
func (r readyzResult) failed() []string {
	var out []string
	for name, ok := range r.checks {
		if !ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// readyz reads `/readyz?verbose`. A failing check answers HTTP 500 with the same list, so the
// body is parsed even when the request errs. The RBAC grants the `/readyz` path only; the
// per-check paths (`/readyz/etcd`) would be denied, which is why the verbose list is read.
func (c *Collector) readyz(ctx context.Context) (readyzResult, model.Capability) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	body, err := c.d.Raw.Get(ctx, "/readyz", url.Values{"verbose": {"true"}})
	checks := parseReadyz(body)
	if len(checks) == 0 {
		if err == nil {
			return readyzResult{}, model.Capability{State: string(probe.Error), Detail: "/readyz?verbose listed no checks"}
		}
		return readyzResult{}, capability(err)
	}
	return readyzResult{reached: true, checks: checks}, model.Capability{State: string(probe.OK), Detail: "verbose checks"}
}

// parseReadyz reads lines of the form `[+]etcd ok` and `[-]etcd failed: reason withheld`.
func parseReadyz(body []byte) map[string]bool {
	checks := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if len(line) < 4 || line[0] != '[' || line[2] != ']' || (line[1] != '+' && line[1] != '-') {
			continue
		}
		name, _, _ := strings.Cut(line[3:], " ")
		if name != "" {
			checks[name] = line[1] == '+'
		}
	}
	return checks
}

// objectCountMetrics are the API server's per-resource object gauges, newest name first
// (apiserver_storage_objects was renamed apiserver_resource_objects).
var objectCountMetrics = []string{"apiserver_resource_objects", "apiserver_storage_objects"}

// apiserverMetrics reads the API server's `/metrics` and keeps the total object count for the
// apiserver bubble. A failed read keeps the last count rather than blanking it.
func (c *Collector) apiserverMetrics(ctx context.Context) model.Capability {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	body, err := c.d.Raw.Get(ctx, "/metrics", nil)
	if err != nil {
		return capability(err)
	}
	n, name, ok := parseObjectCount(body)
	if !ok {
		c.objects = nil
		return model.Capability{State: string(probe.Missing), Detail: "/metrics served without an object-count gauge"}
	}
	c.objects = &n
	return model.Capability{State: string(probe.OK), Detail: name + " present"}
}

// parseObjectCount sums the first object-count gauge present in a Prometheus text body. A
// negative sample means the API server has no count for that resource yet and is skipped.
func parseObjectCount(body []byte) (total int64, metric string, ok bool) {
	for _, m := range objectCountMetrics {
		var sum float64
		found := false
		sc := bufio.NewScanner(bytes.NewReader(body))
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			v, match := sampleValue(sc.Text(), m)
			if !match {
				continue
			}
			found = true
			if v > 0 {
				sum += v
			}
		}
		if found {
			return int64(sum), m, true
		}
	}
	return 0, "", false
}

// sampleValue parses `name{labels} value [timestamp]` or `name value` when the line is a sample
// of exactly the metric name.
func sampleValue(line, name string) (float64, bool) {
	rest, ok := strings.CutPrefix(line, name)
	if !ok || rest == "" || (rest[0] != '{' && rest[0] != ' ') {
		return 0, false
	}
	if rest[0] == '{' {
		end := strings.LastIndexByte(rest, '}')
		if end < 0 {
			return 0, false
		}
		rest = rest[end+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
