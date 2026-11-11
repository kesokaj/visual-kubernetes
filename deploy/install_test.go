// Package deploy holds the install manifests; these tests pin what PRD R1, R2, R7 and S1
// require of them, so an edit that widens RBAC or exposes the page fails here first. Every
// check runs on the real file (must pass) and on mutated copies of it (must fail).
package deploy

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type mutation func(objs []runtime.Object) []runtime.Object

func role(objs []runtime.Object) *rbacv1.ClusterRole { return only[*rbacv1.ClusterRole](objs)[0] }
func binding(objs []runtime.Object) *rbacv1.ClusterRoleBinding {
	return only[*rbacv1.ClusterRoleBinding](objs)[0]
}
func policy(objs []runtime.Object) *networkingv1.NetworkPolicy {
	return only[*networkingv1.NetworkPolicy](objs)[0]
}
func service(objs []runtime.Object) *corev1.Service { return only[*corev1.Service](objs)[0] }
func containerIn(objs []runtime.Object, ns string) *corev1.Container {
	dep, _ := appPodIn(objs, ns)
	return &dep.Spec.Template.Spec.Containers[0]
}
func container(objs []runtime.Object) *corev1.Container { return containerIn(objs, appName) }

func addRule(r rbacv1.PolicyRule) mutation {
	return func(objs []runtime.Object) []runtime.Object {
		role(objs).Rules = append(role(objs).Rules, r)
		return objs
	}
}

func without[T runtime.Object](objs []runtime.Object) []runtime.Object {
	var out []runtime.Object
	for _, o := range objs {
		if _, ok := o.(T); !ok {
			out = append(out, o)
		}
	}
	return out
}

// checkFileBothWays runs check on path (no violation allowed) and on each mutated copy (at
// least one violation required). The mutants assume the file's shape, so a failing file stops
// the test before them.
func checkFileBothWays(t *testing.T, path string, check func([]runtime.Object) []string, mutants map[string]mutation) {
	t.Helper()
	if bad := check(decodeFile(t, path)); len(bad) > 0 {
		t.Fatalf("%s: %v", path, bad)
	}
	for name, mutate := range mutants {
		if bad := check(mutate(decodeFile(t, path))); len(bad) == 0 {
			t.Errorf("%s must-fail %q: no violation reported", path, name)
		}
	}
}

func checkBothWays(t *testing.T, check func([]runtime.Object) []string, mutants map[string]mutation) {
	t.Helper()
	checkFileBothWays(t, installPath, check, mutants)
}

func rbacMutants(ns string) map[string]mutation {
	return map[string]mutation{
		"write verb":        addRule(rbacv1.PolicyRule{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "patch"}}),
		"all secrets":       addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}}),
		"secret listing":    addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{secretsAllow}, Verbs: []string{"get", "list"}}),
		"bare wildcard":     addRule(rbacv1.PolicyRule{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"get"}}),
		"nodes/* wildcard":  addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"nodes/*"}, Verbs: []string{"get"}}),
		"/* nonResourceURL": addRule(rbacv1.PolicyRule{NonResourceURLs: []string{"/*"}, Verbs: []string{"get"}}),
		"nodes/proxy":       addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"nodes/proxy"}, Verbs: []string{"get"}}),
		"pods/exec":         addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"get"}}),
		"pods/attach":       addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/attach"}, Verbs: []string{"get"}}),
		"pods/portforward":  addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/portforward"}, Verbs: []string{"get"}}),
		"aggregationRule": func(objs []runtime.Object) []runtime.Object {
			role(objs).AggregationRule = &rbacv1.AggregationRule{ClusterRoleSelectors: []metav1.LabelSelector{{MatchLabels: map[string]string{"rbac.example/aggregate": "true"}}}}
			return objs
		},
		"binding to cluster-admin": func(objs []runtime.Object) []runtime.Object {
			binding(objs).RoleRef.Name = "cluster-admin"
			return objs
		},
		"second binding": func(objs []runtime.Object) []runtime.Object {
			extra := binding(objs).DeepCopy()
			extra.Name, extra.RoleRef.Name = "extra", "cluster-admin"
			return append(objs, extra)
		},
		"binding to another subject": func(objs []runtime.Object) []runtime.Object {
			binding(objs).Subjects = append(binding(objs).Subjects, rbacv1.Subject{Kind: rbacv1.GroupKind, Name: "system:authenticated"})
			return objs
		},
		"extra Role": func(objs []runtime.Object) []runtime.Object {
			return append(objs, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: ns}})
		},
		"no ClusterRole": without[*rbacv1.ClusterRole],
	}
}

func TestInstallManifestReadOnly(t *testing.T) {
	for _, m := range []struct {
		path string
		ns   string
	}{
		{installPath, appName},
		{kubeSystemPath, kubeSystemNS},
		{lbPath, appName},
	} {
		t.Run(m.path, func(t *testing.T) {
			checkFileBothWays(t, m.path, rbacViolationsIn(m.ns), rbacMutants(m.ns))
		})
	}
}

func TestInstallDeniesAllIngress(t *testing.T) {
	checkBothWays(t, ingressViolations, map[string]mutation{
		"no NetworkPolicy": without[*networkingv1.NetworkPolicy],
		"allow-all ingress rule": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
			return objs
		},
		"egress only": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
			return objs
		},
		"selects other pods": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.PodSelector = metav1.LabelSelector{MatchLabels: map[string]string{"app": "other"}}
			return objs
		},
		"other namespace": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Namespace = "default"
			return objs
		},
		"second policy allows port 8080": func(objs []runtime.Object) []runtime.Object {
			allow := policy(objs).DeepCopy()
			port := intstr.FromInt32(8080)
			allow.Name = "allow"
			allow.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{Ports: []networkingv1.NetworkPolicyPort{{Port: &port}}}}
			return append(objs, allow)
		},
	})
}

func TestInstallServiceIsClusterIP(t *testing.T) {
	for _, m := range []struct {
		path string
		ns   string
	}{
		{installPath, appName},
		{kubeSystemPath, kubeSystemNS},
	} {
		t.Run(m.path, func(t *testing.T) {
			checkFileBothWays(t, m.path, serviceViolationsIn(m.ns), map[string]mutation{
				"LoadBalancer": func(objs []runtime.Object) []runtime.Object {
					service(objs).Spec.Type = corev1.ServiceTypeLoadBalancer
					return objs
				},
				"NodePort": func(objs []runtime.Object) []runtime.Object {
					service(objs).Spec.Type = corev1.ServiceTypeNodePort
					return objs
				},
				"externalIPs": func(objs []runtime.Object) []runtime.Object {
					service(objs).Spec.ExternalIPs = []string{"203.0.113.7"}
					return objs
				},
				"second Service": func(objs []runtime.Object) []runtime.Object {
					return append(objs, service(objs).DeepCopy())
				},
			})
		})
	}
}

func TestInstallPodIsRestricted(t *testing.T) {
	no, yes := false, true
	checkBothWays(t, podViolations, map[string]mutation{
		"no PSA label": func(objs []runtime.Object) []runtime.Object {
			delete(only[*corev1.Namespace](objs)[0].Labels, "pod-security.kubernetes.io/enforce")
			return objs
		},
		"runAsNonRoot false": func(objs []runtime.Object) []runtime.Object {
			dep, _ := appPod(objs)
			dep.Spec.Template.Spec.SecurityContext.RunAsNonRoot = &no
			return objs
		},
		"writable root": func(objs []runtime.Object) []runtime.Object {
			container(objs).SecurityContext.ReadOnlyRootFilesystem = &no
			return objs
		},
		"privilege escalation": func(objs []runtime.Object) []runtime.Object {
			container(objs).SecurityContext.AllowPrivilegeEscalation = &yes
			return objs
		},
		"capabilities kept": func(objs []runtime.Object) []runtime.Object {
			container(objs).SecurityContext.Capabilities.Drop = nil
			return objs
		},
		"latest image": func(objs []runtime.Object) []runtime.Object {
			container(objs).Image = imageRepo + ":latest"
			return objs
		},
		"liveness on /healthz": func(objs []runtime.Object) []runtime.Object {
			container(objs).LivenessProbe.ProbeHandler = corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromString("http")}}
			return objs
		},
		"no liveness": func(objs []runtime.Object) []runtime.Object {
			container(objs).LivenessProbe = nil
			return objs
		},
		"readiness not gated": func(objs []runtime.Object) []runtime.Object {
			container(objs).ReadinessProbe = nil
			return objs
		},
	})
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
	// Must-fail: one changed verb in a copy is a difference the comparison sees.
	mutated := probe[0].DeepCopy()
	mutated.Rules[0].Verbs = []string{"get"}
	if reflect.DeepEqual(inst[0].Rules, mutated.Rules) {
		t.Errorf("must-fail: a changed probe rule compared equal")
	}
}
