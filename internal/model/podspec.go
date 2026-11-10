package model

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// systemNamespaces fold first when a cluster is over the S5 limits; namespaces with a
// `gke-`/`gmp-` prefix or a `-system` suffix count too (GKE, managed Prometheus, operators).
var systemNamespaces = map[string]bool{
	"kube-system":        true,
	"kube-public":        true,
	"kube-node-lease":    true,
	"local-path-storage": true,
}

func isSystemNamespace(ns string) bool {
	return systemNamespaces[ns] ||
		strings.HasPrefix(ns, "gke-") || strings.HasPrefix(ns, "gmp-") || strings.HasSuffix(ns, "-system")
}

func nodeStatus(n *corev1.Node) string {
	s := "NotReady"
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			s = "Ready"
		}
	}
	if n.Spec.Unschedulable {
		s += ",SchedulingDisabled"
	}
	return s
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podStatus is the one word kubectl's STATUS column would show for the cases the page encodes.
func podStatus(p *corev1.Pod) string {
	if p.DeletionTimestamp != nil {
		return "Terminating"
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
	}
	if s, ok := phaseStatus(p); ok {
		return s
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason != "" {
			return c.Reason
		}
	}
	return "Pending"
}

// phaseStatus is the status a Running, Succeeded or Failed phase shows; false for Pending.
func phaseStatus(p *corev1.Pod) (string, bool) {
	switch p.Status.Phase {
	case corev1.PodRunning:
		if podReady(p) {
			return "Running", true
		}
		return "NotReady", true
	case corev1.PodSucceeded:
		return "Completed", true
	case corev1.PodFailed:
		return "Failed", true
	}
	return "", false
}

// desiredPods is the replica count the top owner asks for, or the pods seen when it has none.
func desiredPods(top runtime.Object, seen int) int {
	switch o := top.(type) {
	case *appsv1.Deployment:
		return replicas(o.Spec.Replicas)
	case *appsv1.StatefulSet:
		return replicas(o.Spec.Replicas)
	case *appsv1.ReplicaSet:
		return replicas(o.Spec.Replicas)
	case *appsv1.DaemonSet:
		return int(o.Status.DesiredNumberScheduled)
	}
	return seen
}

// replicas applies the API default of 1 to an unset replica count.
func replicas(r *int32) int {
	if r == nil {
		return 1
	}
	return int(*r)
}

// kubeRootCA is projected into every pod by the ServiceAccount admission plugin; counting it
// as a reference would make it a shared satellite of every app.
const kubeRootCA = "kube-root-ca.crt"

// specRefs returns the ConfigMaps and PVCs a pod spec references by name.
func specRefs(spec *corev1.PodSpec) (configMaps, pvcs []string) {
	configMaps, pvcs = volumeRefs(spec.Volumes)
	return append(configMaps, envRefs(spec)...), pvcs
}

// volumeRefs returns the ConfigMaps and PVCs mounted as volumes, kube-root-ca.crt excluded.
func volumeRefs(volumes []corev1.Volume) (configMaps, pvcs []string) {
	for _, v := range volumes {
		switch {
		case v.ConfigMap != nil:
			configMaps = append(configMaps, v.ConfigMap.Name)
		case v.PersistentVolumeClaim != nil:
			pvcs = append(pvcs, v.PersistentVolumeClaim.ClaimName)
		case v.Projected != nil:
			for _, s := range v.Projected.Sources {
				if s.ConfigMap != nil && s.ConfigMap.Name != kubeRootCA {
					configMaps = append(configMaps, s.ConfigMap.Name)
				}
			}
		}
	}
	return configMaps, pvcs
}

// envRefs returns the ConfigMaps containers read through envFrom or env valueFrom.
func envRefs(spec *corev1.PodSpec) (configMaps []string) {
	for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				configMaps = append(configMaps, ef.ConfigMapRef.Name)
			}
		}
		for _, ev := range c.Env {
			if ev.ValueFrom != nil && ev.ValueFrom.ConfigMapKeyRef != nil {
				configMaps = append(configMaps, ev.ValueFrom.ConfigMapKeyRef.Name)
			}
		}
	}
	return configMaps
}
