package demo

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func isBlockedTargetPort(port int) bool {
	return port < 1 || port > 65535 || port == 10250 || port == 10255
}

func servicePortAllowed(p corev1.ServicePort) bool {
	if p.Protocol != "" && p.Protocol != corev1.ProtocolTCP {
		return false
	}
	if isBlockedTargetPort(int(p.Port)) {
		return false
	}
	if p.TargetPort.Type == intstr.Int && p.TargetPort.IntVal != 0 && isBlockedTargetPort(int(p.TargetPort.IntVal)) {
		return false
	}
	return true
}

func findServicePort(svc *corev1.Service, port int32) (corev1.ServicePort, bool) {
	for _, p := range svc.Spec.Ports {
		if p.Port == port && servicePortAllowed(p) {
			return p, true
		}
	}
	return corev1.ServicePort{}, false
}

// resolvePodEndpoint resolves a ready, non-hostNetwork Pod in svc.Namespace matching
// svc.Spec.Selector so SafeDialer dials the validated Pod IP:targetPort directly without
// kernel ClusterIP DNAT.
func (c *ModelCatalog) resolvePodEndpoint(svc *corev1.Service, sp corev1.ServicePort, host string) (TargetResolved, *StatusErr) {
	forbidden := c.ForbiddenIPs()
	ents, _ := c.Model.Snapshot()
	for _, e := range ents {
		if e.Group != "core" || e.Kind != "Pod" || e.Namespace != svc.Namespace || IsProtectedNamespace(e.Namespace) {
			continue
		}
		obj, ok := c.Model.Object(e.ID)
		pod, isPod := obj.(*corev1.Pod)
		if !ok || !isPod || pod.Spec.HostNetwork || pod.DeletionTimestamp != nil || !isReadyPod(pod) {
			continue
		}
		if !selectorMatches(svc.Spec.Selector, pod.Labels) {
			continue
		}
		targetPort, ok := resolvePodTargetPort(sp, pod)
		if !ok || isBlockedTargetPort(int(targetPort)) {
			continue
		}
		ip, ok := safePodIP(pod, forbidden)
		if !ok {
			continue
		}
		return TargetResolved{Addr: net.JoinHostPort(ip.String(), strconv.Itoa(int(targetPort))), Host: host}, nil
	}
	return TargetResolved{}, &StatusErr{Code: http.StatusBadGateway, Msg: "no ready safe Pod endpoint for Service port"}
}

func isReadyPod(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func selectorMatches(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func resolvePodTargetPort(sp corev1.ServicePort, pod *corev1.Pod) (int32, bool) {
	if sp.TargetPort.Type == intstr.String && sp.TargetPort.StrVal != "" {
		for _, ctr := range pod.Spec.Containers {
			for _, cp := range ctr.Ports {
				if cp.Name == sp.TargetPort.StrVal && (cp.Protocol == "" || cp.Protocol == corev1.ProtocolTCP) {
					return cp.ContainerPort, true
				}
			}
		}
		return 0, false
	}
	if sp.TargetPort.IntVal > 0 {
		return sp.TargetPort.IntVal, true
	}
	return sp.Port, true
}

func safePodIP(pod *corev1.Pod, forbidden []netip.Addr) (netip.Addr, bool) {
	primary, err := netip.ParseAddr(strings.TrimSpace(pod.Status.PodIP))
	if err != nil {
		return netip.Addr{}, false
	}
	primary = primary.Unmap()
	if isForbiddenIP(primary, forbidden) {
		return netip.Addr{}, false
	}
	for _, entry := range pod.Status.PodIPs {
		if ip, err := netip.ParseAddr(strings.TrimSpace(entry.IP)); err == nil && isForbiddenIP(ip.Unmap(), forbidden) {
			return netip.Addr{}, false
		}
	}
	return primary, true
}
