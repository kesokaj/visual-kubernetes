package deploy

import (
	"os"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/kesokaj/visual-kubernetes/internal/iapauth"
)

const iapPath = "install-iap.yaml"

var gcpIAPIngressCIDRs = []string{"35.191.0.0/16", "130.211.0.0/22"}

func iapViolations(wantImage string) func([]runtime.Object) []string {
	return func(objs []runtime.Object) []string {
		bad := podViolations(objs)
		bad = append(bad, serviceViolationsIn(appName)(objs)...)
		dep, errs := appPod(objs)
		if dep == nil {
			return append(bad, errs...)
		}
		c := dep.Spec.Template.Spec.Containers[0]
		if c.Image != wantImage {
			bad = append(bad, "image "+c.Image+" differs from install.yaml "+wantImage)
		}
		if len(c.Command) > 0 {
			bad = append(bad, "container must not override command")
		}
		bad = append(bad, iapArgsViolations(c.Args)...)
		svcs := only[*corev1.Service](objs)
		if len(svcs) == 1 && svcs[0].Annotations["cloud.google.com/neg"] != `{"ingress": true}` {
			bad = append(bad, "Service must carry cloud.google.com/neg annotation for GKE container-native IAP")
		}
		bad = append(bad, lbIngressViolations(objs, dep, gcpIAPIngressCIDRs)...)
		return bad
	}
}

func iapArgsViolations(args []string) []string {
	var bad []string
	if !slices.Contains(args, "--demo") {
		bad = append(bad, "install-iap.yaml must pass --demo")
	}
	var aud, host string
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--iap-audience="); ok {
			aud = v
		}
		if v, ok := strings.CutPrefix(a, "--allowed-host="); ok {
			host = v
		}
	}
	if err := iapauth.ValidateAudience(aud); err != nil {
		bad = append(bad, err.Error())
	}
	if host == "" || strings.ContainsAny(host, "*:/\\?#@ \t\r\n") {
		bad = append(bad, "install-iap.yaml must set a valid bare DNS --allowed-host")
	}
	return bad
}

func TestInstallIAPVariant(t *testing.T) {
	raw, err := os.ReadFile(iapPath)
	if err != nil {
		t.Fatalf("read %s: %v", iapPath, err)
	}
	if v := nonProdNoticeViolation(string(raw)); v != "" {
		t.Error(v)
	}

	inst := decodeFile(t, installPath)
	iapObjs := decodeFile(t, iapPath)
	if got := rbacViolationsIn(appName)(iapObjs); len(got) == 0 {
		t.Fatal("must-fail: read-only rbacViolationsIn accepted install-iap.yaml")
	}
	checkFileBothWays(t, iapPath, demoRBACViolations(role(inst).Rules), demoRBACMutants(role(inst).Rules))

	checkFileBothWays(t, iapPath, iapViolations(container(inst).Image), map[string]mutation{
		"missing-demo": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{
				"--iap-audience=/projects/000000000000/global/backendServices/0000000000000000",
				"--allowed-host=visual-kubernetes.example.internal",
			}
			return objs
		},
		"missing-iap-audience": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{"--demo", "--allowed-host=visual-kubernetes.example.internal"}
			return objs
		},
		"invalid-iap-audience": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{"--demo", "--iap-audience=https://bad", "--allowed-host=visual-kubernetes.example.internal"}
			return objs
		},
		"missing-allowed-host": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{
				"--demo",
				"--iap-audience=/projects/000000000000/global/backendServices/0000000000000000",
			}
			return objs
		},
		"wildcard-allowed-host": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{
				"--demo",
				"--iap-audience=/projects/000000000000/global/backendServices/0000000000000000",
				"--allowed-host=*.example.internal",
			}
			return objs
		},
		"loadbalancer-service": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.Type = corev1.ServiceTypeLoadBalancer
			return objs
		},
		"missing-neg-annotation": func(objs []runtime.Object) []runtime.Object {
			delete(service(objs).Annotations, "cloud.google.com/neg")
			return objs
		},
		"no-networkpolicy": without[*networkingv1.NetworkPolicy],
		"podselector-peer": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress[0].From = []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}
			return objs
		},
		"public-cidr": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress[0].From = []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"}}}
			return objs
		},
		"wrong-ingress-port": func(objs []runtime.Object) []runtime.Object {
			p := intstr.FromInt32(9090)
			policy(objs).Spec.Ingress[0].Ports[0].Port = &p
			return objs
		},
	})
}
