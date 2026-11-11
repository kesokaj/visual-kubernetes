package deploy

import (
	"bytes"
	"errors"
	"io"
	"net/netip"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

// Each check returns what is wrong with a decoded manifest, so install_test.go and
// variants_test.go run it on the real file (must pass) and on mutated copies (must fail).

const (
	installPath    = "install.yaml"
	kubeSystemPath = "install-kube-system.yaml"
	lbPath         = "install-lb.yaml"
	probeRBAC      = "../test/kind/probe-rbac.yaml"
	appName        = "visual-kubernetes"
	kubeSystemNS   = "kube-system"
	imageRepo      = "europe-north1-docker.pkg.dev/exact-loon-6dim/visual-kubernetes/visual-kubernetes"
	secretsAllow   = "hubble-relay-client-certs"
)

// releaseImage is the repository plus a semver tag; a release asset may add an @sha256 digest.
var releaseImage = regexp.MustCompile(`^` + regexp.QuoteMeta(imageRepo) + `:v\d+\.\d+\.\d+(@sha256:[0-9a-f]{64})?$`)

func decodeFile(t *testing.T, path string) []runtime.Object {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 4096)
	var objs []runtime.Object
	for {
		var raw runtime.RawExtension
		err := dec.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("%s: reading YAML stream: %v", path, err)
		}
		if len(bytes.TrimSpace(raw.Raw)) == 0 {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(raw.Raw, nil, nil)
		if err != nil {
			t.Fatalf("%s: decoding a document: %v", path, err)
		}
		objs = append(objs, obj)
	}
	if len(objs) == 0 {
		t.Fatalf("%s: decoded 0 objects", path)
	}
	return objs
}

func only[T runtime.Object](objs []runtime.Object) []T {
	var out []T
	for _, o := range objs {
		if v, ok := o.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// readOnlyViolations lists every rule that could write, read Secrets broadly, or reach the
// kubelet's proxy (R1, R2). A `*` anywhere in a string counts, so `nodes/*` (which grants
// nodes/proxy) and a nonResourceURL `/*` fail like a bare `*`.
func readOnlyViolations(rules []rbacv1.PolicyRule) []string {
	var bad []string
	for _, r := range rules {
		for _, v := range r.Verbs {
			if !slices.Contains([]string{"get", "list", "watch"}, v) {
				bad = append(bad, "verb "+v)
			}
		}
		for _, field := range [][]string{r.APIGroups, r.Resources, r.ResourceNames, r.NonResourceURLs} {
			if slices.ContainsFunc(field, func(s string) bool { return strings.Contains(s, "*") }) {
				bad = append(bad, "wildcard in "+strings.Join(field, ","))
			}
		}
		for _, res := range r.Resources {
			switch {
			case res == "secrets" && !reflect.DeepEqual(r.ResourceNames, []string{secretsAllow}):
				bad = append(bad, "secrets without the one named resourceName")
			case res == "secrets" && !reflect.DeepEqual(r.Verbs, []string{"get"}):
				bad = append(bad, "secrets with list or watch")
			// get on these upgrades to a WebSocket stream into the pod or node, so a read
			// verb still opens a shell, a port or a proxy.
			case strings.HasSuffix(res, "/proxy"), strings.HasSuffix(res, "/exec"), strings.HasSuffix(res, "/attach"), strings.HasSuffix(res, "/portforward"):
				bad = append(bad, "subresource "+res)
			}
		}
	}
	return bad
}

// rbacViolationsIn checks that the only access granted is the one read-only, non-aggregated
// ClusterRole, bound once to the app's ServiceAccount in ns.
func rbacViolationsIn(ns string) func([]runtime.Object) []string {
	return func(objs []runtime.Object) []string {
		var bad []string
		roles := only[*rbacv1.ClusterRole](objs)
		if len(roles) != 1 || roles[0].Name != appName {
			return []string{"want exactly one ClusterRole named " + appName}
		}
		if roles[0].AggregationRule != nil {
			bad = append(bad, "ClusterRole has an aggregationRule (its rules would come from other roles)")
		}
		bad = append(bad, readOnlyViolations(roles[0].Rules)...)
		if n := len(only[*rbacv1.Role](objs)) + len(only[*rbacv1.RoleBinding](objs)); n != 0 {
			bad = append(bad, "Roles or RoleBindings present (all access is the one ClusterRole)")
		}
		bindings := only[*rbacv1.ClusterRoleBinding](objs)
		if len(bindings) != 1 {
			return append(bad, "want exactly one ClusterRoleBinding")
		}
		want := rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: appName}
		if bindings[0].RoleRef != want {
			bad = append(bad, "ClusterRoleBinding roleRef is not the "+appName+" ClusterRole")
		}
		sa := []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: appName, Namespace: ns}}
		if !reflect.DeepEqual(bindings[0].Subjects, sa) {
			bad = append(bad, "ClusterRoleBinding subjects are not only "+ns+"/"+appName)
		}
		return bad
	}
}

// serviceViolationsIn checks R7's default: one ClusterIP Service in ns and nothing exposed outside.
func serviceViolationsIn(ns string) func([]runtime.Object) []string {
	return func(objs []runtime.Object) []string {
		svcs := only[*corev1.Service](objs)
		if len(svcs) != 1 || svcs[0].Namespace != ns || svcs[0].Name != appName {
			return []string{"want exactly one Service " + ns + "/" + appName}
		}
		var bad []string
		if svcs[0].Spec.Type != corev1.ServiceTypeClusterIP {
			bad = append(bad, "Service type "+string(svcs[0].Spec.Type)+", want ClusterIP")
		}
		if len(svcs[0].Spec.ExternalIPs) > 0 {
			bad = append(bad, "Service has externalIPs")
		}
		return bad
	}
}

func appPodIn(objs []runtime.Object, ns string) (*appsv1.Deployment, []string) {
	deps := only[*appsv1.Deployment](objs)
	if len(deps) != 1 || deps[0].Name != appName || deps[0].Namespace != ns {
		return nil, []string{"want one Deployment " + ns + "/" + appName}
	}
	if len(deps[0].Spec.Template.Spec.Containers) != 1 {
		return nil, []string{"want one container"}
	}
	return deps[0], nil
}

func appPod(objs []runtime.Object) (*appsv1.Deployment, []string) { return appPodIn(objs, appName) }

// ingressViolationsIn checks that a NetworkPolicy in ns denies all ingress to the app's pod and
// no policy allows ingress. When requireScopedSelector is true (kube-system), an empty podSelector
// is also rejected so coredns and metrics-server are never isolated by mistake.
func ingressViolationsIn(ns string, requireScopedSelector bool) func([]runtime.Object) []string {
	return func(objs []runtime.Object) []string {
		dep, bad := appPodIn(objs, ns)
		if dep == nil {
			return bad
		}
		podLabels := labels.Set(dep.Spec.Template.Labels)
		denied := false
		for _, np := range only[*networkingv1.NetworkPolicy](objs) {
			if requireScopedSelector && np.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"] != appName {
				bad = append(bad, "NetworkPolicy "+np.Name+": kube-system policy must scope podSelector to app.kubernetes.io/name="+appName)
			}
			sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
			if err != nil {
				bad = append(bad, "NetworkPolicy "+np.Name+": bad podSelector")
				continue
			}
			if np.Namespace != dep.Namespace || !sel.Matches(podLabels) {
				continue
			}
			if len(np.Spec.Ingress) > 0 {
				bad = append(bad, "NetworkPolicy "+np.Name+" allows ingress")
			}
			if slices.Contains(np.Spec.PolicyTypes, networkingv1.PolicyTypeIngress) && len(np.Spec.Ingress) == 0 {
				denied = true
			}
		}
		if !denied {
			bad = append(bad, "no NetworkPolicy denies all ingress to the app's pod")
		}
		return bad
	}
}

func ingressViolations(objs []runtime.Object) []string {
	return ingressViolationsIn(appName, false)(objs)
}

// podViolationsIn checks PSA restricted (when ownNamespace is true; kube-system must not carry a
// Namespace object), namespaced object placement, a released image and readiness/liveness probes.
func podViolationsIn(ns string, ownNamespace bool) func([]runtime.Object) []string {
	return func(objs []runtime.Object) []string {
		var bad []string
		nss := only[*corev1.Namespace](objs)
		if ownNamespace {
			if len(nss) != 1 || nss[0].Name != ns || nss[0].Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
				bad = append(bad, "want one Namespace "+ns+" with PSA enforce=restricted")
			}
		} else if len(nss) != 0 {
			bad = append(bad, "kube-system variant must not include a Namespace object")
		}
		for _, o := range objs {
			m, err := meta.Accessor(o)
			if err == nil && m.GetNamespace() != "" && m.GetNamespace() != ns {
				bad = append(bad, m.GetName()+" has namespace "+m.GetNamespace()+", want "+ns)
			}
		}
		dep, errs := appPodIn(objs, ns)
		if dep == nil {
			return append(bad, errs...)
		}
		spec := dep.Spec.Template.Spec
		c := spec.Containers[0]
		if psc := spec.SecurityContext; psc == nil || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
			bad = append(bad, "pod must set runAsNonRoot")
		}
		sc := c.SecurityContext
		if sc == nil {
			bad = append(bad, "container has no securityContext")
		} else {
			if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
				bad = append(bad, "container must set readOnlyRootFilesystem")
			}
			if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
				bad = append(bad, "container must set allowPrivilegeEscalation: false")
			}
			if sc.Capabilities == nil || !reflect.DeepEqual(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) {
				bad = append(bad, "container must drop ALL capabilities")
			}
		}
		if !releaseImage.MatchString(c.Image) {
			bad = append(bad, "image "+c.Image+" is not "+imageRepo+":vX.Y.Z")
		}
		if p := c.ReadinessProbe; p == nil || p.HTTPGet == nil || p.HTTPGet.Path != "/healthz" {
			bad = append(bad, "readiness must be httpGet /healthz")
		}
		if p := c.LivenessProbe; p == nil || (p.HTTPGet != nil && p.HTTPGet.Path == "/healthz") {
			bad = append(bad, "liveness must exist and must not wait for the sync (/healthz)")
		}
		return bad
	}
}

func podViolations(objs []runtime.Object) []string { return podViolationsIn(appName, true)(objs) }

// lbViolations checks PRD R7's opt-in LoadBalancer variant (ADR-008, D37, D38, D47): a
// LoadBalancer Service with externalTrafficPolicy: Local and narrow, valid
// loadBalancerSourceRanges (never empty, 0.0.0.0/0 or ::/0), --logs=false on the container,
// and an ingress NetworkPolicy limited to TCP port http from those same ipBlock CIDRs.
func lbViolations(objs []runtime.Object) []string {
	var bad []string
	svcs := only[*corev1.Service](objs)
	if len(svcs) != 1 || svcs[0].Namespace != appName || svcs[0].Name != appName {
		return []string{"want one Service " + appName + "/" + appName}
	}
	svc := svcs[0]
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		bad = append(bad, "Service type "+string(svc.Spec.Type)+", want LoadBalancer")
	}
	if svc.Spec.ExternalTrafficPolicy != corev1.ServiceExternalTrafficPolicyLocal {
		bad = append(bad, "Service externalTrafficPolicy "+string(svc.Spec.ExternalTrafficPolicy)+", want Local")
	}
	if len(svc.Spec.ExternalIPs) > 0 {
		bad = append(bad, "Service has externalIPs")
	}
	if len(svc.Spec.LoadBalancerSourceRanges) == 0 {
		bad = append(bad, "loadBalancerSourceRanges must not be empty")
	}
	for _, raw := range svc.Spec.LoadBalancerSourceRanges {
		if msg := sourceCIDRViolation(raw); msg != "" {
			bad = append(bad, msg)
		}
	}
	dep, errs := appPod(objs)
	if dep == nil {
		return append(bad, errs...)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if len(c.Command) > 0 {
		bad = append(bad, "container must not override command")
	}
	if !slices.Contains(c.Args, "--logs=false") {
		bad = append(bad, "container must pass --logs=false")
	}
	for _, a := range c.Args {
		if a != "--logs=false" && (a == "--logs" || a == "-logs" || strings.HasPrefix(a, "--logs=") || strings.HasPrefix(a, "-logs=")) {
			bad = append(bad, "conflicting logs flag "+a)
		}
	}
	bad = append(bad, lbIngressViolations(objs, dep, svc.Spec.LoadBalancerSourceRanges)...)
	return bad
}

func sourceCIDRViolation(raw string) string {
	p, err := netip.ParsePrefix(raw)
	switch {
	case raw != strings.TrimSpace(raw) || err != nil:
		return "invalid CIDR " + raw
	case p != p.Masked():
		return "non-canonical CIDR " + raw
	case p.Bits() < 8 || p.Addr().IsUnspecified():
		return "overbroad or unspecified source range " + raw
	default:
		return ""
	}
}

func lbIngressViolations(objs []runtime.Object, dep *appsv1.Deployment, wantCIDRs []string) []string {
	var bad []string
	nps := only[*networkingv1.NetworkPolicy](objs)
	if len(nps) != 1 || nps[0].Namespace != dep.Namespace {
		return []string{"want exactly one NetworkPolicy in " + dep.Namespace}
	}
	np := nps[0]
	sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
	if err != nil || !sel.Matches(labels.Set(dep.Spec.Template.Labels)) {
		bad = append(bad, "NetworkPolicy does not select the app's pod")
	}
	if !slices.Contains(np.Spec.PolicyTypes, networkingv1.PolicyTypeIngress) || len(np.Spec.Ingress) == 0 {
		return append(bad, "LB NetworkPolicy must isolate Ingress and allow TCP http")
	}
	protoTCP := corev1.ProtocolTCP
	wantPort := intstr.FromString("http")
	wantNum := intstr.FromInt32(8080)
	var fromCIDRs []string
	for _, r := range np.Spec.Ingress {
		if len(r.From) == 0 {
			bad = append(bad, "LB NetworkPolicy ingress rule must restrict source CIDRs (from)")
		}
		for _, peer := range r.From {
			if peer.IPBlock == nil || peer.PodSelector != nil || peer.NamespaceSelector != nil {
				bad = append(bad, "LB NetworkPolicy ingress peer must be ipBlock only")
				continue
			}
			if msg := sourceCIDRViolation(peer.IPBlock.CIDR); msg != "" {
				bad = append(bad, "LB NetworkPolicy "+msg)
			}
			fromCIDRs = append(fromCIDRs, peer.IPBlock.CIDR)
		}
		if len(r.Ports) == 0 {
			bad = append(bad, "LB NetworkPolicy ingress rule has no port restriction")
		}
		for _, p := range r.Ports {
			proto := corev1.ProtocolTCP
			if p.Protocol != nil {
				proto = *p.Protocol
			}
			if proto != protoTCP || p.Port == nil || (*p.Port != wantPort && *p.Port != wantNum) {
				bad = append(bad, "LB NetworkPolicy allows a port other than TCP http/8080")
			}
		}
	}
	if !slices.Equal(fromCIDRs, wantCIDRs) {
		bad = append(bad, "LB NetworkPolicy ipBlock CIDRs do not match loadBalancerSourceRanges")
	}
	return bad
}
