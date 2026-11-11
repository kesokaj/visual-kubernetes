package hubble

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	maxPromBody = 4 << 20
	promQuery   = `rate({__name__=~"traces_service_graph_request_total|istio_requests_total|hubble_flows_processed_total"}[30s])`
)

var prometheusServices = []struct{ namespace, name string }{
	{"monitoring", "kube-prometheus-stack-prometheus"},
	{"monitoring", "prometheus-k8s"},
	{"monitoring", "prometheus-server"},
	{"prometheus", "prometheus-server"},
	{"kube-system", "prometheus"},
}

type promResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []promSample `json:"result"`
	} `json:"data"`
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

func detectPrometheus(ctx context.Context, cs kubernetes.Interface) (string, bool) {
	for _, s := range prometheusServices {
		svc, err := cs.CoreV1().Services(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) || err != nil || len(svc.Spec.Ports) == 0 {
			continue
		}
		port := int(svc.Spec.Ports[0].Port)
		if port <= 0 {
			port = 9090
		}
		host := fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace)
		return "http://" + net.JoinHostPort(host, strconv.Itoa(port)), true
	}
	return "", false
}

func queryPrometheusFlows(ctx context.Context, hc *http.Client, baseURL string, idx *modelIndex) ([]Flow, error) {
	u, err := url.Parse(baseURL + "/api/v1/query")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", promQuery)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus status %d", resp.StatusCode)
	}
	var pr promResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPromBody)).Decode(&pr); err != nil {
		return nil, err
	}
	if pr.Status != "success" {
		return nil, errors.New("prometheus query status " + pr.Status)
	}
	return foldPromSamples(pr.Data.Result, idx), nil
}

func foldPromSamples(samples []promSample, idx *modelIndex) []Flow {
	type key struct{ src, dst, verdict string }
	counts := map[key]float64{}
	for _, s := range samples {
		rate := sampleRate(s.Value)
		if rate <= 0 {
			continue
		}
		src, dst, verdict := promEndpoints(s.Metric, idx)
		if src == "" || dst == "" || src == dst {
			continue
		}
		counts[key{src, dst, verdict}] += rate
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int {
		if c := cmp.Compare(a.src, b.src); c != 0 {
			return c
		}
		if c := cmp.Compare(a.dst, b.dst); c != 0 {
			return c
		}
		return cmp.Compare(a.verdict, b.verdict)
	})
	out := make([]Flow, 0, len(keys))
	for _, k := range keys {
		c := max(int64(1), int64(math.Round(counts[k])))
		out = append(out, Flow{Src: k.src, Dst: k.dst, Verdict: k.verdict, Count: c})
	}
	return out
}

func sampleRate(val [2]any) float64 {
	s, ok := val[1].(string)
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func promEndpoints(m map[string]string, idx *modelIndex) (src, dst, verdict string) {
	if m["__name__"] == "traces_service_graph_request_failed_total" {
		return "", "", ""
	}
	verdict = VerdictForwarded
	if m["verdict"] == VerdictDropped {
		verdict = VerdictDropped
	} else if m["verdict"] != "" && m["verdict"] != VerdictForwarded {
		return "", "", ""
	}
	src = resolvePromSide(m, idx, "source_workload_namespace", "source_workload", "client_namespace", "client", "source")
	dst = resolvePromSide(m, idx, "destination_workload_namespace", "destination_workload", "server_namespace", "server", "destination")
	return src, dst, verdict
}

func resolvePromSide(m map[string]string, idx *modelIndex, ns1, n1, ns2, n2, pairKey string) string {
	if ns, name := m[ns1], m[n1]; ns != "" && name != "" {
		if id := idx.resolveName(ns, name); id != "" {
			return id
		}
	}
	if ns, name := m[ns2], m[n2]; name != "" {
		if id := idx.resolvePromPair(ns, name); id != "" {
			return id
		}
	}
	if raw := m[pairKey]; raw != "" {
		if ns, name, ok := strings.Cut(raw, "/"); ok {
			return idx.resolveName(ns, name)
		}
	}
	return ""
}

func (idx *modelIndex) resolvePromPair(ns, name string) string {
	if a, b, ok := strings.Cut(name, "/"); ok && ns == "" {
		ns, name = a, b
	} else if svc, svcNS, ok := strings.Cut(name, "."); ok && ns == "" {
		ns, name = strings.SplitN(svcNS, ".", 2)[0], svc
	}
	if ns != "" {
		return idx.resolveName(ns, name)
	}
	match := ""
	for id, e := range idx.drawn {
		if strings.HasPrefix(id, "app/") && e.Name == name {
			if match != "" {
				return ""
			}
			match = id
		}
	}
	return match
}
