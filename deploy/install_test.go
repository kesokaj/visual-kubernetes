// Package deploy holds the install manifests; these tests pin what PRD R1, R2, R7 and S1
// require of them, so an edit that widens RBAC or exposes the page fails here first.
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
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	installPath  = "install.yaml"
	probeRBAC    = "../test/kind/probe-rbac.yaml"
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

// readOnlyViolations lists every rule that could write, read Secrets broadly, or reach
// the kubelet's proxy (R1, R2).
func readOnlyViolations(rules []rbacv1.PolicyRule) []string {
	var bad []string
	for _, r := range rules {
		for _, v := range r.Verbs {
			if !slices.Contains([]string{"get", "list", "watch"}, v) {
				bad = append(bad, "verb "+v)
			}
		}
		for _, field := range [][]string{r.APIGroups, r.Resources, r.ResourceNames, r.NonResourceURLs} {
			if slices.Contains(field, "*") {
				bad = append(bad, "wildcard in "+strings.Join(field, ","))
			}
		}
		for _, res := range r.Resources {
			switch {
			case res == "secrets" && !reflect.DeepEqual(r.ResourceNames, []string{secretsAllow}):
				bad = append(bad, "secrets without the one named resourceName")
			case res == "secrets" && !reflect.DeepEqual(r.Verbs, []string{"get"}):
				bad = append(bad, "secrets with list or watch")
			case strings.HasSuffix(res, "/proxy"), strings.HasSuffix(res, "/exec"), strings.HasSuffix(res, "/attach"):
				bad = append(bad, "subresource "+res)
			}
		}
	}
	return bad
}

func TestInstallManifestReadOnly(t *testing.T) {
	objs := decodeFile(t, installPath)
	roles := only[*rbacv1.ClusterRole](objs)
	if len(roles) != 1 {
		t.Fatalf("ClusterRoles = %d, want 1", len(roles))
	}
	if n := len(only[*rbacv1.Role](objs)); n != 0 {
		t.Errorf("Roles = %d, want 0 (all access is the one read-only ClusterRole)", n)
	}
	if bad := readOnlyViolations(roles[0].Rules); len(bad) > 0 {
		t.Errorf("ClusterRole is not read-only: %v", bad)
	}
}

// TestReadOnlyCheckFailsOnMutations is the must-fail side of TestInstallManifestReadOnly.
func TestReadOnlyCheckFailsOnMutations(t *testing.T) {
	cases := map[string]rbacv1.PolicyRule{
		"write verb":     {APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "patch"}},
		"all secrets":    {APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}},
		"secret listing": {APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{secretsAllow}, Verbs: []string{"get", "list"}},
		"wildcard":       {APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"get"}},
		"nodes/proxy":    {APIGroups: []string{""}, Resources: []string{"nodes/proxy"}, Verbs: []string{"get"}},
		"pods/exec":      {APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"get"}},
	}
	for name, r := range cases {
		if bad := readOnlyViolations([]rbacv1.PolicyRule{r}); len(bad) == 0 {
			t.Errorf("%s: no violation reported", name)
		}
	}
}

func TestInstallServiceIsClusterIP(t *testing.T) {
	svcs := only[*corev1.Service](decodeFile(t, installPath))
	if len(svcs) != 1 {
		t.Fatalf("Services = %d, want 1", len(svcs))
	}
	if typ := svcs[0].Spec.Type; typ != corev1.ServiceTypeClusterIP {
		t.Errorf("Service type = %q, want ClusterIP (R7: reached only through port-forward)", typ)
	}
}

func TestInstallPodIsRestricted(t *testing.T) {
	objs := decodeFile(t, installPath)
	nss := only[*corev1.Namespace](objs)
	if len(nss) != 1 || nss[0].Name != "visual-kubernetes" || nss[0].Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Fatalf("want one Namespace visual-kubernetes with PSA enforce=restricted (S1), got %+v", nss)
	}
	deps := only[*appsv1.Deployment](objs)
	if len(deps) != 1 || deps[0].Name != "visual-kubernetes" || deps[0].Namespace != "visual-kubernetes" {
		t.Fatalf("want one Deployment visual-kubernetes/visual-kubernetes, got %d", len(deps))
	}
	spec := deps[0].Spec.Template.Spec
	if len(spec.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(spec.Containers))
	}
	c := spec.Containers[0]
	sc := c.SecurityContext
	switch {
	case spec.SecurityContext == nil || spec.SecurityContext.RunAsNonRoot == nil || !*spec.SecurityContext.RunAsNonRoot:
		t.Error("pod must set runAsNonRoot")
	case sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem:
		t.Error("container must set readOnlyRootFilesystem")
	case sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation:
		t.Error("container must set allowPrivilegeEscalation: false")
	case sc.Capabilities == nil || !reflect.DeepEqual(sc.Capabilities.Drop, []corev1.Capability{"ALL"}):
		t.Error("container must drop ALL capabilities")
	}
	if !releaseImage.MatchString(c.Image) {
		t.Errorf("image = %q, want %s:vX.Y.Z (a released tag, never latest)", c.Image, imageRepo)
	}
}

// TestProbeRBACMatchesInstall keeps the kind probe's ClusterRole identical to the one users
// install, so the in-cluster probe tests the access the app really gets.
func TestProbeRBACMatchesInstall(t *testing.T) {
	inst := only[*rbacv1.ClusterRole](decodeFile(t, installPath))
	probe := only[*rbacv1.ClusterRole](decodeFile(t, probeRBAC))
	if len(inst) != 1 || len(probe) != 1 {
		t.Fatalf("ClusterRoles: install %d, probe %d, want 1 each", len(inst), len(probe))
	}
	if !reflect.DeepEqual(inst[0].Rules, probe[0].Rules) {
		t.Errorf("test/kind/probe-rbac.yaml rules differ from deploy/install.yaml")
	}
}
