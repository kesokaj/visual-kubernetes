package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/kubelet"
)

var certErr = &url.Error{Op: "Get", URL: "https://172.18.0.3:10250/stats/summary",
	Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}

type mutate func(d *Deps, cs *fake.Clientset, kl *fakeKubelet)

func TestProbeStates(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		objs       []runtime.Object
		mutate     mutate
		want       State
		wantDetail string
	}{
		{"discovery ok", "discovery", nil, nil, OK, "v1.37.0"},
		{"discovery denied", "discovery", nil, verbErr("get", "version", errForbidden), Denied, ""},
		{"discovery unreachable", "discovery", nil, verbErr("get", "version", errRefused), Unreachable, ""},

		{"leases ok", "leases", nil, nil, OK, "leader leases: kube-scheduler, kube-controller-manager"},
		{"leases managed control plane", "leases", nil, verbList("leases", &coordinationv1.LeaseList{}), OK, "no control-plane leader leases"},
		{"leases denied", "leases", nil, verbErr("list", "leases", errForbidden), Denied, ""},
		{"leases unreachable", "leases", nil, verbErr("list", "leases", errUnavailable), Unreachable, ""},

		{"events ok", "events", nil, nil, OK, "list ok"},
		{"events denied", "events", nil, verbErr("list", "events", errForbidden), Denied, ""},
		{"events missing", "events", nil, verbErr("list", "events", errNotFound), Missing, ""},

		{"endpointslices ok", "endpointslices", nil, nil, OK, "list ok"},
		{"endpointslices denied", "endpointslices", nil, verbErr("list", "endpointslices", errForbidden), Denied, ""},

		{"metrics ok", "metrics.k8s.io", nil, nil, OK, "2 node metrics"},
		{"metrics missing", "metrics.k8s.io", nil, rawErr("/apis/metrics.k8s.io/v1beta1/nodes", errNotFound), Missing, "no metrics-server"},
		{"metrics denied", "metrics.k8s.io", nil, rawErr("/apis/metrics.k8s.io/v1beta1/nodes", errForbidden), Denied, ""},
		{"metrics unreachable", "metrics.k8s.io", nil, rawErr("/apis/metrics.k8s.io/v1beta1/nodes", errUnavailable), Unreachable, ""},

		{"kubelet ok", "kubelet", nil, nil, OK, "1 with network bytes; TLS verified"},
		{"kubelet unreachable", "kubelet", nil, kubeletErr(fmt.Errorf("kubelet: GET: %w", errRefused)), Unreachable, ""},
		{"kubelet denied", "kubelet", nil, kubeletErr(&kubelet.StatusError{Code: 403}), Denied, ""},
		{"kubelet cert refused", "kubelet", nil, kubeletErr(certErr), Error, "certificate"},
		{"kubelet no client", "kubelet", nil, func(d *Deps, _ *fake.Clientset, _ *fakeKubelet) {
			d.Kubelet, d.KubeletErr = nil, kubelet.ErrNoCA
		}, Error, "no CA bundle"},
		{"kubelet local mode", "kubelet", nil, func(d *Deps, _ *fake.Clientset, _ *fakeKubelet) {
			d.Kubelet, d.KubeletErr = nil, ErrLocalMode
		}, Unreachable, "never sent to nodes"},
		{"kubelet no nodes", "kubelet", nil, verbList("nodes", &corev1.NodeList{}), Missing, "no node"},
		{"kubelet nodes denied", "kubelet", nil, verbErr("list", "nodes", errForbidden), Denied, ""},

		{"apiserver metrics ok", "apiserver-metrics", nil, nil, OK, "apiserver_resource_objects present"},
		{"apiserver metrics legacy name", "apiserver-metrics", nil, rawBody("/metrics", "x 1\napiserver_storage_objects{resource=\"pods\"} 3\n"), OK, "apiserver_storage_objects"},
		{"apiserver metrics missing gauge", "apiserver-metrics", nil, rawBody("/metrics", "x 1\n"), Missing, ""},
		{"apiserver metrics denied", "apiserver-metrics", nil, rawErr("/metrics", errForbidden), Denied, ""},

		{"readyz ok", "readyz", nil, nil, OK, "ok"},
		{"readyz denied", "readyz", nil, rawErr("/readyz", errForbidden), Denied, ""},
		{"readyz not ready", "readyz", nil, rawBody("/readyz", "[-]etcd failed"), Error, "etcd"},
		{"readyz unreachable", "readyz", nil, rawErr("/readyz", fmt.Errorf("Get: %w", errRefused)), Unreachable, ""},

		{"hubble missing", "hubble-relay", nil, nil, Missing, "kube-system/hubble-relay or gke-managed-dpv2-observability/hubble-relay"},
		{"hubble cilium ok", "hubble-relay", []runtime.Object{hubbleService("kube-system")}, nil, OK, "hubble-relay.kube-system.svc:443"},
		{"hubble dpv2 ok", "hubble-relay", []runtime.Object{hubbleService("gke-managed-dpv2-observability")}, nil, OK, "gke-managed-dpv2-observability.svc:443"},
		{"hubble unreachable", "hubble-relay", []runtime.Object{hubbleService("kube-system")}, func(d *Deps, _ *fake.Clientset, _ *fakeKubelet) {
			d.Dial = dialRefused
		}, Unreachable, ""},
		{"hubble denied", "hubble-relay", nil, verbErr("get", "services", errForbidden), Denied, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, cs, kl := healthyDeps(tt.objs...)
			if tt.mutate != nil {
				tt.mutate(&d, cs, kl)
			}
			r := find(t, Run(t.Context(), d), tt.source)
			if r.State != tt.want {
				t.Errorf("state = %s (%s), want %s", r.State, r.Detail, tt.want)
			}
			if !strings.Contains(r.Detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", r.Detail, tt.wantDetail)
			}
		})
	}
}

func TestKubeletProbeUsesNodeAddressAndPort(t *testing.T) {
	d, _, kl := healthyDeps()
	find(t, Run(t.Context(), d), "kubelet")
	if kl.gotHost != "172.18.0.3" || kl.gotPort != 10250 {
		t.Errorf("Summary(%q, %d), want the node InternalIP and kubelet endpoint port", kl.gotHost, kl.gotPort)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want State
	}{
		{"nil", nil, OK},
		{"forbidden", errForbidden, Denied},
		{"not found", errNotFound, Missing},
		{"service unavailable", errUnavailable, Unreachable},
		// A cert error inside a *net.OpError must still read as error, not unreachable (R8 visible).
		{"cert error inside url.Error", certErr, Error},
		{"cert error inside net.OpError", &net.OpError{Op: "read", Net: "tcp", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}, Error},
		{"hostname mismatch", x509.HostnameError{Host: "10.0.0.1", Certificate: &x509.Certificate{}}, Error},
		{"conn refused", fmt.Errorf("wrap: %w", errRefused), Unreachable},
		{"dns", &net.DNSError{Err: "no such host", Name: "hubble-relay.kube-system.svc"}, Unreachable},
		{"deadline", fmt.Errorf("wrap: %w", context.DeadlineExceeded), Unreachable},
		{"http timeout", &url.Error{Op: "Get", URL: "u", Err: timeoutErr{}}, Unreachable},
		{"kubelet 401", &kubelet.StatusError{Code: 401}, Denied},
		{"kubelet 404", &kubelet.StatusError{Code: 404}, Missing},
		{"kubelet 500", &kubelet.StatusError{Code: 500}, Error},
		{"other", errors.New("boom"), Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.want {
				t.Errorf("Classify(%v) = %s, want %s", tt.err, got, tt.want)
			}
		})
	}
}

func TestRunOrderAndWrite(t *testing.T) {
	d, _, _ := healthyDeps()
	results := Run(t.Context(), d)
	if len(results) != len(sources) {
		t.Fatalf("%d results, want %d", len(results), len(sources))
	}
	for i, s := range sources {
		if results[i].Source != s.name {
			t.Errorf("results[%d] = %s, want %s", i, results[i].Source, s.name)
		}
	}
	var buf bytes.Buffer
	if err := Write(&buf, []Result{{Source: "readyz", State: OK, Detail: "ok"}, {Source: "x", State: Missing}}); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "readyz ok ok\nx missing -\n"; got != want {
		t.Errorf("Write = %q, want %q", got, want)
	}
}

func TestOneLineTruncates(t *testing.T) {
	got := oneLine("a\n  b\t" + strings.Repeat("x", 500))
	if strings.ContainsAny(got, "\n\t") || len(got) > maxDetail+len("…") || !strings.HasPrefix(got, "a b ") {
		t.Errorf("oneLine = %q", got)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func find(t *testing.T, rs []Result, source string) Result {
	t.Helper()
	for _, r := range rs {
		if r.Source == source {
			return r
		}
	}
	t.Fatalf("no result for %s", source)
	return Result{}
}

func verbErr(verb, resource string, err error) mutate {
	return func(_ *Deps, cs *fake.Clientset, _ *fakeKubelet) { failVerb(cs, verb, resource, err) }
}

func verbList(resource string, obj runtime.Object) mutate {
	return func(_ *Deps, cs *fake.Clientset, _ *fakeKubelet) {
		cs.PrependReactor("list", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, obj, nil
		})
	}
}

func rawErr(path string, err error) mutate {
	return func(d *Deps, _ *fake.Clientset, _ *fakeKubelet) { setRaw(d, path, rawResp{err: err}) }
}

func rawBody(path, body string) mutate {
	return func(d *Deps, _ *fake.Clientset, _ *fakeKubelet) { setRaw(d, path, rawResp{body: body}) }
}

func setRaw(d *Deps, path string, r rawResp) {
	raw := fakeRaw{}
	for k, v := range d.Raw.(fakeRaw) {
		raw[k] = v
	}
	raw[path] = r
	d.Raw = raw
}

func kubeletErr(err error) mutate {
	return func(_ *Deps, _ *fake.Clientset, kl *fakeKubelet) { kl.summary, kl.err = nil, err }
}

func TestOneLineStripsTerminalEscapes(t *testing.T) {
	got := oneLine("bad\x1b[2J\x1b]0;pwned\x07 body\r\n")
	if strings.ContainsAny(got, "\x1b\x07\r") || got != "bad[2J]0;pwned body" {
		t.Errorf("oneLine = %q", got)
	}
}
