package deploy

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const demoPath = "install-demo.yaml"

func demoExpectedExtraRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{"apps"},
			Resources: []string{"daemonsets", "deployments", "statefulsets"},
			Verbs:     []string{"patch", "update"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{"patch", "update"},
		},
		{
			APIGroups: []string{"autoscaling"},
			Resources: []string{"horizontalpodautoscalers"},
			Verbs:     []string{"patch", "update"},
		},
		{
			APIGroups: []string{"autoscaling.k8s.io"},
			Resources: []string{"verticalpodautoscalers"},
			Verbs:     []string{"patch", "update"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods/exec"},
			Verbs:     []string{"create", "get"},
		},
	}
}

func demoRBACViolations(baseRules []rbacv1.PolicyRule) func([]runtime.Object) []string {
	wantRules := append(slices.Clone(baseRules), demoExpectedExtraRules()...)
	return func(objs []runtime.Object) []string {
		var bad []string
		roles := only[*rbacv1.ClusterRole](objs)
		if len(roles) != 1 || roles[0].Name != appName {
			return []string{"want exactly one ClusterRole named " + appName}
		}
		if roles[0].AggregationRule != nil {
			bad = append(bad, "ClusterRole has an aggregationRule")
		}
		if !reflect.DeepEqual(roles[0].Rules, wantRules) {
			bad = append(bad, "ClusterRole rules do not match install.yaml + exact narrow demo rules")
		}
		bad = append(bad, demoForbiddenRuleViolations(roles[0].Rules)...)
		if n := len(only[*rbacv1.Role](objs)) + len(only[*rbacv1.RoleBinding](objs)); n != 0 {
			bad = append(bad, "Roles or RoleBindings present")
		}
		bindings := only[*rbacv1.ClusterRoleBinding](objs)
		if len(bindings) != 1 {
			return append(bad, "want exactly one ClusterRoleBinding")
		}
		wantRef := rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: appName}
		if bindings[0].RoleRef != wantRef {
			bad = append(bad, "ClusterRoleBinding roleRef mismatch")
		}
		wantSA := []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: appName, Namespace: appName}}
		if !reflect.DeepEqual(bindings[0].Subjects, wantSA) {
			bad = append(bad, "ClusterRoleBinding subjects mismatch")
		}
		return bad
	}
}

func demoForbiddenRuleViolations(rules []rbacv1.PolicyRule) []string {
	var bad []string
	for _, r := range rules {
		for _, res := range r.Resources {
			if res == "secrets" && (!reflect.DeepEqual(r.ResourceNames, []string{secretsAllow}) || !reflect.DeepEqual(r.Verbs, []string{"get"})) {
				bad = append(bad, "secrets rule widened in install-demo.yaml")
			}
			if strings.HasSuffix(res, "/proxy") || strings.HasSuffix(res, "/attach") || strings.HasSuffix(res, "/portforward") {
				bad = append(bad, "forbidden subresource "+res)
			}
		}
	}
	return bad
}

func demoPodViolations(wantImage string) func([]runtime.Object) []string {
	return func(objs []runtime.Object) []string {
		bad := podViolations(objs)
		dep, errs := appPod(objs)
		if dep == nil {
			return append(bad, errs...)
		}
		c := dep.Spec.Template.Spec.Containers[0]
		if c.Image != wantImage {
			bad = append(bad, "image "+c.Image+" differs from install.yaml "+wantImage)
		}
		if !reflect.DeepEqual(c.Args, []string{"--demo"}) {
			bad = append(bad, "install-demo.yaml container must set args: [--demo]")
		}
		return bad
	}
}

func nonProdNoticeViolation(raw string) string {
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "non-production") || !strings.Contains(lower, "do not") {
		return "install-demo.yaml header must warn that --demo is for non-production clusters only"
	}
	return ""
}

func withoutDemoRule(apiGroup, resource string) mutation {
	return func(objs []runtime.Object) []runtime.Object {
		r := role(objs)
		kept := make([]rbacv1.PolicyRule, 0, len(r.Rules))
		for _, rule := range r.Rules {
			if slices.Equal(rule.APIGroups, []string{apiGroup}) &&
				slices.Equal(rule.Resources, []string{resource}) &&
				slices.Equal(rule.Verbs, []string{"patch", "update"}) {
				continue
			}
			kept = append(kept, rule)
		}
		r.Rules = kept
		return objs
	}
}

func demoRBACMutants(baseRules []rbacv1.PolicyRule) map[string]mutation {
	return map[string]mutation{
		"drop-demo-rules": func(objs []runtime.Object) []runtime.Object {
			role(objs).Rules = slices.Clone(baseRules)
			return objs
		},
		"drop-services-rule": withoutDemoRule("", "services"),
		"drop-hpa-rule":      withoutDemoRule("autoscaling", "horizontalpodautoscalers"),
		"drop-vpa-rule":      withoutDemoRule("autoscaling.k8s.io", "verticalpodautoscalers"),
		"services-widen-configmaps": func(objs []runtime.Object) []runtime.Object {
			for i := range role(objs).Rules {
				r := &role(objs).Rules[i]
				if slices.Equal(r.APIGroups, []string{""}) && slices.Equal(r.Resources, []string{"services"}) {
					r.Resources = []string{"services", "configmaps"}
				}
			}
			return objs
		},
		"services-create":    addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"create"}}),
		"services-delete":    addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"delete"}}),
		"hpa-delete":         addRule(rbacv1.PolicyRule{APIGroups: []string{"autoscaling"}, Resources: []string{"horizontalpodautoscalers"}, Verbs: []string{"delete"}}),
		"vpa-create":         addRule(rbacv1.PolicyRule{APIGroups: []string{"autoscaling.k8s.io"}, Resources: []string{"verticalpodautoscalers"}, Verbs: []string{"create"}}),
		"delete-deployments": addRule(rbacv1.PolicyRule{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"delete"}}),
		"create-pods":        addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"create"}}),
		"secrets-list":       addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list"}}),
		"pods-attach":        addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/attach"}, Verbs: []string{"create"}}),
		"pods-portforward":   addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/portforward"}, Verbs: []string{"create"}}),
		"services-proxy":     addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"services/proxy"}, Verbs: []string{"get"}}),
		"nodes-proxy":        addRule(rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"nodes/proxy"}, Verbs: []string{"get"}}),
		"aggregation-rule": func(objs []runtime.Object) []runtime.Object {
			role(objs).AggregationRule = &rbacv1.AggregationRule{}
			return objs
		},
		"wrong-subject": func(objs []runtime.Object) []runtime.Object {
			binding(objs).Subjects[0].Namespace = "default"
			return objs
		},
	}
}

func TestInstallDemoRBAC(t *testing.T) {
	raw, err := os.ReadFile(demoPath)
	if err != nil {
		t.Fatalf("read %s: %v", demoPath, err)
	}
	if v := nonProdNoticeViolation(string(raw)); v != "" {
		t.Error(v)
	}
	if nonProdNoticeViolation("# visual-kubernetes manifest\n") == "" {
		t.Error("must-fail: header without non-production warning accepted")
	}

	inst := decodeFile(t, installPath)
	demoObjs := decodeFile(t, demoPath)
	if got := rbacViolationsIn(appName)(demoObjs); len(got) == 0 {
		t.Fatal("must-fail: read-only rbacViolationsIn accepted install-demo.yaml")
	}

	checkFileBothWays(t, demoPath, demoRBACViolations(role(inst).Rules), demoRBACMutants(role(inst).Rules))
}

func TestInstallDemoPodServiceAndNetworkPolicy(t *testing.T) {
	inst := decodeFile(t, installPath)
	checkFileBothWays(t, demoPath, demoPodViolations(container(inst).Image), map[string]mutation{
		"missing-demo-arg": func(objs []runtime.Object) []runtime.Object { container(objs).Args = nil; return objs },
		"demo-false-arg": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{"--demo=false"}
			return objs
		},
		"extra-logs-false": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{"--demo", "--logs=false"}
			return objs
		},
		"missing-psa": func(objs []runtime.Object) []runtime.Object {
			delete(only[*corev1.Namespace](objs)[0].Labels, "pod-security.kubernetes.io/enforce")
			return objs
		},
		"run-as-root": func(objs []runtime.Object) []runtime.Object {
			dep, _ := appPod(objs)
			dep.Spec.Template.Spec.SecurityContext.RunAsNonRoot = nil
			return objs
		},
		"wrong-image": func(objs []runtime.Object) []runtime.Object {
			container(objs).Image = imageRepo + ":v9.9.9"
			return objs
		},
	})
	checkFileBothWays(t, demoPath, serviceViolationsIn(appName), map[string]mutation{
		"loadbalancer": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.Type = corev1.ServiceTypeLoadBalancer
			return objs
		},
		"nodeport": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.Type = corev1.ServiceTypeNodePort
			return objs
		},
	})
	checkFileBothWays(t, demoPath, ingressViolations, map[string]mutation{
		"no-policy": without[*networkingv1.NetworkPolicy],
		"allow-ingress": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
			return objs
		},
	})
}
