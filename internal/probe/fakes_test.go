package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/kubelet"
)

// Hand-written fakes for the probe dependencies (golang skill: no mock library).

var (
	errForbidden   = apierrors.NewForbidden(schema.GroupResource{Resource: "things"}, "", errors.New("rbac denies"))
	errNotFound    = apierrors.NewNotFound(schema.GroupResource{Resource: "things"}, "x")
	errUnavailable = apierrors.NewServiceUnavailable("the server is currently unable to handle the request")
	errRefused     = &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
)

type rawResp struct {
	body string
	err  error
}

type fakeRaw map[string]rawResp

func (f fakeRaw) Get(_ context.Context, path string) ([]byte, error) {
	r, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("fakeRaw: unexpected path %s", path)
	}
	return []byte(r.body), r.err
}

type fakeKubelet struct {
	summary *kubelet.Summary
	err     error
	gotHost string
	gotPort int
}

func (f *fakeKubelet) Summary(_ context.Context, host string, port int) (*kubelet.Summary, error) {
	f.gotHost, f.gotPort = host, port
	return f.summary, f.err
}

func dialOK(context.Context, string, string) (net.Conn, error) {
	c1, c2 := net.Pipe()
	_ = c2.Close() // the far end is unused; the probe only checks the dial
	return c1, nil
}

func dialRefused(context.Context, string, string) (net.Conn, error) { return nil, errRefused }

func u64(v uint64) *uint64 { return &v }

// healthyObjects is a small kind-like cluster: one addressed node and the leader leases.
func healthyObjects() []runtime.Object {
	return []runtime.Object{
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "kind-worker"},
			Status: corev1.NodeStatus{
				Addresses:       []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "172.18.0.3"}},
				DaemonEndpoints: corev1.NodeDaemonEndpoints{KubeletEndpoint: corev1.DaemonEndpoint{Port: 10250}},
			},
		},
		&coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: "kube-scheduler", Namespace: "kube-system"}},
		&coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: "kube-controller-manager", Namespace: "kube-system"}},
	}
}

func hubbleService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "hubble-relay", Namespace: ns},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 443}}},
	}
}

// healthyDeps returns deps on which every probe except Hubble reports ok.
func healthyDeps(objs ...runtime.Object) (Deps, *fake.Clientset, *fakeKubelet) {
	cs := fake.NewClientset(append(healthyObjects(), objs...)...)
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.37.0"}
	kl := &fakeKubelet{summary: &kubelet.Summary{
		Node: kubelet.NodeStats{NodeName: "kind-worker"},
		Pods: []kubelet.PodStats{{PodRef: kubelet.PodReference{Name: "p"}, Network: &kubelet.NetworkStats{RxBytes: u64(1)}}},
	}}
	raw := fakeRaw{
		"/apis/metrics.k8s.io/v1beta1/nodes": {body: `{"items":[{},{}]}`},
		"/metrics":                           {body: "# HELP x\napiserver_resource_objects{resource=\"pods\"} 12\n"},
		"/readyz":                            {body: "ok"},
	}
	return Deps{Client: cs, Raw: raw, Kubelet: kl, Dial: dialOK}, cs, kl
}

// failVerb makes the fake clientset answer verb on resource with err.
func failVerb(cs *fake.Clientset, verb, resource string, err error) {
	cs.PrependReactor(verb, resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}
