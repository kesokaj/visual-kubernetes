package deploy

import (
	"bytes"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

// Each check returns what is wrong with a decoded manifest, so install_test.go can run it on
// the real file (must pass) and on mutated copies (must fail).

const (
	installPath  = "install.yaml"
	probeRBAC    = "../test/kind/probe-rbac.yaml"
	appName      = "visual-kubernetes"
	imageRepo    = "europe-north1-docker.pkg.dev/exact-loon-6dim/visual-kubernetes/visual-kubernetes"
	secretsAllow = "hubble-relay-client-certs"
)

// releaseImage is the repository plus a semver tag; a release asset may add an @sha256 digest.
var releaseImage = regexp.MustCompile(`^` + regexp.QuoteMeta(imageRepo) + `:v\d+\.\d+\.\d+(@sha256:[0-9a-f]{64})?$`)

func decodeFile(t *testing.T, path string) []runtime.Object {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var objs []runtime.Object
	for _, doc := range bytes.Split(b, []byte("\n---\n")) {
		if len(bytes.TrimSpace(stripComments(doc))) == 0 {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if err != nil {
			t.Fatalf("%s: decoding a document: %v", path, err)
		}
		objs = append(objs, obj)
	}
	return objs
}

func stripComments(doc []byte) []byte {
	var out [][]byte
	for _, line := range bytes.Split(doc, []byte("\n")) {
		if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("#")) {
			out = append(out, line)
		}
	}
	return bytes.Join(out, []byte("\n"))
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

// rbacViolations checks that the only access granted is the one read-only, non-aggregated
// ClusterRole, bound once to the app's own ServiceAccount.
func rbacViolations(objs []runtime.Object) []string {
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
	sa := []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: appName, Namespace: appName}}
	if !reflect.DeepEqual(bindings[0].Subjects, sa) {
		bad = append(bad, "ClusterRoleBinding subjects are not only the app's ServiceAccount")
	}
	return bad
}

// serviceViolations checks R7's default: one ClusterIP Service and nothing exposed outside.
func serviceViolations(objs []runtime.Object) []string {
	svcs := only[*corev1.Service](objs)
	if len(svcs) != 1 {
		return []string{"want exactly one Service"}
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

func appPod(objs []runtime.Object) (*appsv1.Deployment, []string) {
	deps := only[*appsv1.Deployment](objs)
	if len(deps) != 1 || deps[0].Name != appName || deps[0].Namespace != appName {
		return nil, []string{"want one Deployment " + appName + "/" + appName}
	}
	if len(deps[0].Spec.Template.Spec.Containers) != 1 {
		return nil, []string{"want one container"}
	}
	return deps[0], nil
}

// ingressViolations checks that some NetworkPolicy in the app's namespace selects its pod and
// denies all ingress, and that no policy in the file allows any ingress to it.
func ingressViolations(objs []runtime.Object) []string {
	dep, bad := appPod(objs)
	if dep == nil {
		return bad
	}
	podLabels := labels.Set(dep.Spec.Template.Labels)
	denied := false
	for _, np := range only[*networkingv1.NetworkPolicy](objs) {
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

// podViolations checks PSA restricted (S1), a released image and probes: readiness waits for
// the sync on /healthz, liveness never does (a slow sync must not restart the pod).
func podViolations(objs []runtime.Object) []string {
	var bad []string
	nss := only[*corev1.Namespace](objs)
	if len(nss) != 1 || nss[0].Name != appName || nss[0].Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		bad = append(bad, "want one Namespace "+appName+" with PSA enforce=restricted")
	}
	dep, errs := appPod(objs)
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
		return append(bad, "container has no securityContext")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		bad = append(bad, "container must set readOnlyRootFilesystem")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		bad = append(bad, "container must set allowPrivilegeEscalation: false")
	}
	if sc.Capabilities == nil || !reflect.DeepEqual(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		bad = append(bad, "container must drop ALL capabilities")
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
