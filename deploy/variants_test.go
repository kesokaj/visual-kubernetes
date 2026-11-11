package deploy

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestInstallKubeSystemVariant(t *testing.T) {
	no, yes := false, true
	checkFileBothWays(t, kubeSystemPath, podViolationsIn(kubeSystemNS, false), map[string]mutation{
		"includes Namespace kube-system": func(objs []runtime.Object) []runtime.Object {
			return append(objs, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: kubeSystemNS}})
		},
		"object in wrong namespace": func(objs []runtime.Object) []runtime.Object {
			service(objs).Namespace = appName
			return objs
		},
		"runAsNonRoot false": func(objs []runtime.Object) []runtime.Object {
			dep, _ := appPodIn(objs, kubeSystemNS)
			dep.Spec.Template.Spec.SecurityContext.RunAsNonRoot = &no
			return objs
		},
		"writable root": func(objs []runtime.Object) []runtime.Object {
			containerIn(objs, kubeSystemNS).SecurityContext.ReadOnlyRootFilesystem = &no
			return objs
		},
		"privilege escalation": func(objs []runtime.Object) []runtime.Object {
			containerIn(objs, kubeSystemNS).SecurityContext.AllowPrivilegeEscalation = &yes
			return objs
		},
		"capabilities kept": func(objs []runtime.Object) []runtime.Object {
			containerIn(objs, kubeSystemNS).SecurityContext.Capabilities.Drop = nil
			return objs
		},
		"latest image": func(objs []runtime.Object) []runtime.Object {
			containerIn(objs, kubeSystemNS).Image = imageRepo + ":latest"
			return objs
		},
		"liveness on /healthz": func(objs []runtime.Object) []runtime.Object {
			containerIn(objs, kubeSystemNS).LivenessProbe.ProbeHandler = corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromString("http")}}
			return objs
		},
		"readiness not gated": func(objs []runtime.Object) []runtime.Object {
			containerIn(objs, kubeSystemNS).ReadinessProbe = nil
			return objs
		},
	})

	checkFileBothWays(t, kubeSystemPath, ingressViolationsIn(kubeSystemNS, true), map[string]mutation{
		"no NetworkPolicy": without[*networkingv1.NetworkPolicy],
		"empty podSelector isolates whole kube-system": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.PodSelector = metav1.LabelSelector{}
			return objs
		},
		"allow-all ingress rule": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
			return objs
		},
		"egress only": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
			return objs
		},
	})
}

func TestInstallLBRequiresSourceRanges(t *testing.T) {
	no := false
	checkFileBothWays(t, lbPath, podViolations, map[string]mutation{
		"no PSA label": func(objs []runtime.Object) []runtime.Object {
			delete(only[*corev1.Namespace](objs)[0].Labels, "pod-security.kubernetes.io/enforce")
			return objs
		},
		"writable root": func(objs []runtime.Object) []runtime.Object {
			container(objs).SecurityContext.ReadOnlyRootFilesystem = &no
			return objs
		},
	})

	checkFileBothWays(t, lbPath, lbViolations, map[string]mutation{
		"empty loadBalancerSourceRanges": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.LoadBalancerSourceRanges = nil
			return objs
		},
		"0.0.0.0/0": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.LoadBalancerSourceRanges = []string{"0.0.0.0/0"}
			return objs
		},
		"::/0": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.LoadBalancerSourceRanges = []string{"::/0"}
			return objs
		},
		"split 0.0.0.0/1 + 128.0.0.0/1": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.LoadBalancerSourceRanges = []string{"0.0.0.0/1", "128.0.0.0/1"}
			return objs
		},
		"malformed CIDR": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.LoadBalancerSourceRanges = []string{"not-a-cidr"}
			return objs
		},
		"non-canonical CIDR": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.LoadBalancerSourceRanges = []string{"203.0.113.5/24"}
			return objs
		},
		"ClusterIP instead of LoadBalancer": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.Type = corev1.ServiceTypeClusterIP
			return objs
		},
		"externalTrafficPolicy Cluster": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyCluster
			return objs
		},
		"externalIPs": func(objs []runtime.Object) []runtime.Object {
			service(objs).Spec.ExternalIPs = []string{"203.0.113.7"}
			return objs
		},
		"missing --logs=false": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = nil
			return objs
		},
		"--logs=true overrides --logs=false": func(objs []runtime.Object) []runtime.Object {
			container(objs).Args = []string{"--logs=false", "--logs=true"}
			return objs
		},
		"custom command": func(objs []runtime.Object) []runtime.Object {
			container(objs).Command = []string{"/visual-kubernetes"}
			return objs
		},
		"no NetworkPolicy": without[*networkingv1.NetworkPolicy],
		"allow-all ingress rule": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
			return objs
		},
		"ingress rule missing from": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress[0].From = nil
			return objs
		},
		"ingress rule allows podSelector": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress[0].From = []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}
			return objs
		},
		"ingress rule ipBlock 0.0.0.0/0": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress[0].From = []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"}}}
			return objs
		},
		"ingress ipBlock disagrees with loadBalancerSourceRanges": func(objs []runtime.Object) []runtime.Object {
			policy(objs).Spec.Ingress[0].From = []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "203.0.113.9/32"}}}
			return objs
		},
		"wrong ingress port": func(objs []runtime.Object) []runtime.Object {
			port := intstr.FromInt32(9090)
			policy(objs).Spec.Ingress[0].Ports[0].Port = &port
			return objs
		},
	})
}

func TestManifestsShareClusterRoleAndImage(t *testing.T) {
	inst := decodeFile(t, installPath)
	ks := decodeFile(t, kubeSystemPath)
	lb := decodeFile(t, lbPath)

	wantRules := role(inst).Rules
	wantImg := container(inst).Image
	for _, m := range []struct {
		name string
		objs []runtime.Object
		ns   string
	}{
		{kubeSystemPath, ks, kubeSystemNS},
		{lbPath, lb, appName},
	} {
		if !reflect.DeepEqual(role(m.objs).Rules, wantRules) {
			t.Errorf("%s ClusterRole rules differ from %s", m.name, installPath)
		}
		if got := containerIn(m.objs, m.ns).Image; got != wantImg {
			t.Errorf("%s image = %q, want %q", m.name, got, wantImg)
		}
	}
	mut := role(lb).DeepCopy()
	mut.Rules = mut.Rules[:1]
	if reflect.DeepEqual(mut.Rules, wantRules) {
		t.Error("must-fail: truncated ClusterRole rules compared equal")
	}
}
