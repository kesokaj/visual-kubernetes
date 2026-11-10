// Package modeltest builds the Kubernetes objects that the model tests and test/fixturegen feed
// to the model. Builders return new objects, and the "With"/state helpers return modified
// copies, because the model keeps the pointers it is given.
package modeltest

import (
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Labels builds a label map from key, value pairs.
func Labels(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func objectMeta(ns, name string, labels map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels}
}

// ControllerRef is a controller ownerReference to the named object.
func ControllerRef(apiVersion, kind, name string) *metav1.OwnerReference {
	yes := true
	return &metav1.OwnerReference{APIVersion: apiVersion, Kind: kind, Name: name, Controller: &yes}
}

// Node is a node with a Ready condition.
func Node(name string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: objectMeta("", name, Labels("kubernetes.io/hostname", name)),
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}},
	}
}

func template(labels map[string]string, configMaps ...string) corev1.PodTemplateSpec {
	t := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "registry.k8s.io/pause:3.10"}}},
	}
	for _, cm := range configMaps {
		t.Spec.Volumes = append(t.Spec.Volumes, corev1.Volume{
			Name:         cm,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: cm}}},
		})
	}
	return t
}

// Deployment selects and templates pods with labels; its pods mount the named ConfigMaps.
func Deployment(ns, name string, replicas int32, labels map[string]string, configMaps ...string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: objectMeta(ns, name, labels),
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: template(labels, configMaps...),
		},
	}
}

// Scaled returns a copy of d with a new replica count.
func Scaled(d *appsv1.Deployment, replicas int32) *appsv1.Deployment {
	c := d.DeepCopy()
	c.Spec.Replicas = &replicas
	return c
}

// ReplicaSet is the ReplicaSet a Deployment controller would create for template hash.
func ReplicaSet(d *appsv1.Deployment, hash string) *appsv1.ReplicaSet {
	labels := Labels("pod-template-hash", hash)
	for k, v := range d.Spec.Template.Labels {
		labels[k] = v
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: objectMeta(d.Namespace, d.Name+"-"+hash, labels),
		Spec: appsv1.ReplicaSetSpec{
			Replicas: d.Spec.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: *d.Spec.Template.DeepCopy(),
		},
	}
	rs.Spec.Template.Labels = labels
	rs.OwnerReferences = []metav1.OwnerReference{*ControllerRef("apps/v1", "Deployment", d.Name)}
	return rs
}

// DaemonSet templates pods with labels and wants desired of them.
func DaemonSet(ns, name string, labels map[string]string, desired int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: objectMeta(ns, name, labels),
		Spec:       appsv1.DaemonSetSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: template(labels)},
		Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: desired},
	}
}

// Pod is an unscheduled Pending pod owned by owner (nil for a bare pod).
func Pod(ns, name string, labels map[string]string, owner *metav1.OwnerReference) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: objectMeta(ns, name, labels),
		Spec:       template(labels).Spec,
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	if owner != nil {
		p.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	return p
}

// PodOf is a Pending pod owned by rs, labelled like its template.
func PodOf(rs *appsv1.ReplicaSet, suffix string) *corev1.Pod {
	p := Pod(rs.Namespace, rs.Name+"-"+suffix, rs.Spec.Template.Labels, ControllerRef("apps/v1", "ReplicaSet", rs.Name))
	p.Spec = *rs.Spec.Template.Spec.DeepCopy()
	return p
}

// Unschedulable returns a copy of p the scheduler could not place.
func Unschedulable(p *corev1.Pod) *corev1.Pod {
	c := p.DeepCopy()
	c.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable"}}
	return c
}

// Scheduled returns a copy of p bound to node, still Pending.
func Scheduled(p *corev1.Pod, node string) *corev1.Pod {
	c := p.DeepCopy()
	c.Spec.NodeName = node
	c.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}}
	return c
}

// Running returns a copy of p in phase Running with its Ready condition set to ready.
func Running(p *corev1.Pod, ready bool) *corev1.Pod {
	c := p.DeepCopy()
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	c.Status.Phase = corev1.PodRunning
	c.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
		{Type: corev1.PodReady, Status: status},
	}
	return c
}

// Service is a ClusterIP Service; a nil or empty selector means no selector.
func Service(ns, name string, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: objectMeta(ns, name, nil),
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: selector},
	}
}

// ConfigMap is an empty ConfigMap.
func ConfigMap(ns, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: objectMeta(ns, name, nil)}
}

// ServiceAccount is a kind the page never draws; it only counts.
func ServiceAccount(ns, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{ObjectMeta: objectMeta(ns, name, nil)}
}

// HPA scales the apps/v1 target of the given kind and name.
func HPA(ns, name, kind, target string, minReplicas, maxReplicas, current int32) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: objectMeta(ns, name, nil),
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: kind, Name: target},
			MinReplicas:    &minReplicas,
			MaxReplicas:    maxReplicas,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: current},
	}
}

// NetworkPolicy selects pods by labels; nil selects every pod in the namespace.
func NetworkPolicy(ns, name string, selector map[string]string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: objectMeta(ns, name, nil),
		Spec:       networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: selector}},
	}
}

// Ingress routes / to the named Service.
func Ingress(ns, name, service string) *networkingv1.Ingress {
	return &networkingv1.Ingress{
		ObjectMeta: objectMeta(ns, name, nil),
		Spec: networkingv1.IngressSpec{
			DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: service}},
		},
	}
}
