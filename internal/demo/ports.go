package demo

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// PortEntry describes one exposed Service port in /api/demo/ports.
type PortEntry struct {
	ID         string `json:"id"`
	Namespace  string `json:"namespace"`
	Service    string `json:"service"`
	AppID      string `json:"appId"`
	Port       int32  `json:"port"`
	Name       string `json:"name"`
	Exposure   string `json:"exposure"`
	Open       bool   `json:"open"`
	PathPrefix string `json:"pathPrefix"`
}

// PortsResponse is the JSON envelope returned by GET and POST /api/demo/ports.
type PortsResponse struct {
	Ports []PortEntry `json:"ports"`
}

// TargetResolved is the upstream address dialed for an open port.
type TargetResolved struct {
	Addr string
	Host string
}

// StatusErr pairs an HTTP status code with a plain-text message.
type StatusErr struct {
	Code int
	Msg  string
}

// Catalog lists exposed non-protected Service ports and resolves an open port's target.
type Catalog interface {
	ExposedPorts() []PortEntry
	ResolveTarget(ns, service string, port int32) (TargetResolved, *StatusErr)
	ForbiddenIPs() []netip.Addr
}

// ModelCatalog reads exposed Services from the live model cache (D9).
type ModelCatalog struct {
	Model     *model.Model
	InCluster bool
}

// ExposedPorts lists TCP ports on unfolded, non-protected Services exposed by LoadBalancer,
// NodePort, Ingress, or HTTPRoute.
func (c *ModelCatalog) ExposedPorts() []PortEntry {
	if c == nil || c.Model == nil {
		return []PortEntry{}
	}
	ents, edges := c.Model.Snapshot()
	routed := c.routedServices(ents, edges)
	out := make([]PortEntry, 0)
	for _, e := range ents {
		if e.Group != "core" || e.Kind != "Service" || e.Folded || IsProtectedNamespace(e.Namespace) {
			continue
		}
		obj, ok := c.Model.Object(e.ID)
		svc, isSvc := obj.(*corev1.Service)
		if !ok || !isSvc || len(svc.Spec.Selector) == 0 {
			continue
		}
		exp := serviceExposure(svc, routed[svc.Namespace+"/"+svc.Name])
		if exp == "" {
			continue
		}
		for _, p := range svc.Spec.Ports {
			if !servicePortAllowed(p) {
				continue
			}
			out = append(out, PortEntry{
				ID: e.ID, Namespace: svc.Namespace, Service: svc.Name, AppID: e.AppID,
				Port: p.Port, Name: p.Name, Exposure: exp,
			})
		}
	}
	return out
}

func serviceExposure(svc *corev1.Service, routedBy string) string {
	switch svc.Spec.Type {
	case corev1.ServiceTypeLoadBalancer:
		return "LoadBalancer"
	case corev1.ServiceTypeNodePort:
		return "NodePort"
	default:
		return routedBy
	}
}

func (c *ModelCatalog) routedServices(ents []model.Entity, edges []model.Edge) map[string]string {
	routed := map[string]string{}
	unfolded := map[string]model.Entity{}
	for _, e := range ents {
		if e.Folded {
			continue
		}
		unfolded[e.ID] = e
		obj, ok := c.Model.Object(e.ID)
		if !ok {
			continue
		}
		if ing, ok := obj.(*networkingv1.Ingress); ok {
			for _, key := range ingressServiceKeys(ing) {
				routed[key] = "Ingress"
			}
		} else if u, ok := obj.(*unstructured.Unstructured); ok && e.Kind == "HTTPRoute" {
			for _, key := range httpRouteServiceKeys(u) {
				if routed[key] == "" {
					routed[key] = "HTTPRoute"
				}
			}
		}
	}
	for _, edge := range edges {
		if edge.Kind == model.EdgeRoutes && strings.HasPrefix(edge.To, "core/Service/") {
			if src, ok := unfolded[edge.From]; ok && (src.Kind == "Ingress" || src.Kind == "HTTPRoute") {
				if key := strings.TrimPrefix(edge.To, "core/Service/"); routed[key] == "" {
					routed[key] = src.Kind
				}
			}
		}
	}
	return routed
}

func ingressServiceKeys(ing *networkingv1.Ingress) []string {
	var out []string
	if d := ing.Spec.DefaultBackend; d != nil && d.Service != nil && d.Service.Name != "" {
		out = append(out, ing.Namespace+"/"+d.Service.Name)
	}
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service != nil && p.Backend.Service.Name != "" {
				out = append(out, ing.Namespace+"/"+p.Backend.Service.Name)
			}
		}
	}
	return out
}

func httpRouteServiceKeys(u *unstructured.Unstructured) []string {
	var out []string
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		refs, _ := rule["backendRefs"].([]any)
		for _, raw := range refs {
			ref, _ := raw.(map[string]any)
			kind, _ := ref["kind"].(string)
			name, _ := ref["name"].(string)
			ns, _ := ref["namespace"].(string)
			if (kind == "" || kind == "Service") && name != "" {
				if ns == "" {
					ns = u.GetNamespace()
				}
				out = append(out, ns+"/"+name)
			}
		}
	}
	return out
}

// ResolveTarget validates that (ns, service, port) is still exposed and returns its dial address.
func (c *ModelCatalog) ResolveTarget(ns, service string, port int32) (TargetResolved, *StatusErr) {
	if IsProtectedNamespace(ns) || isBlockedTargetPort(int(port)) || !hasExposedPort(c.ExposedPorts(), ns, service, port) {
		return TargetResolved{}, &StatusErr{Code: http.StatusForbidden, Msg: "port is closed in proxy gatekeeper"}
	}
	obj, ok := c.Model.Object("core/Service/" + ns + "/" + service)
	svc, isSvc := obj.(*corev1.Service)
	if !ok || !isSvc || len(svc.Spec.Selector) == 0 {
		return TargetResolved{}, &StatusErr{Code: http.StatusForbidden, Msg: "port is closed in proxy gatekeeper"}
	}
	sp, ok := findServicePort(svc, port)
	if !ok {
		return TargetResolved{}, &StatusErr{Code: http.StatusForbidden, Msg: "port is closed in proxy gatekeeper"}
	}
	host := fmt.Sprintf("%s.%s.svc:%d", service, ns, port)
	if c.InCluster {
		if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == corev1.ClusterIPNone {
			return TargetResolved{}, &StatusErr{Code: http.StatusBadGateway, Msg: "headless Service has no ClusterIP to proxy"}
		}
		return c.resolvePodEndpoint(svc, sp, host)
	}
	forbidden := c.ForbiddenIPs()
	for _, ing := range svc.Status.LoadBalancer.Ingress {
		if ip, err := netip.ParseAddr(strings.TrimSpace(ing.IP)); err == nil && !isForbiddenIP(ip.Unmap(), forbidden) {
			return TargetResolved{Addr: net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(int(port))), Host: host}, nil
		}
	}
	return TargetResolved{}, &StatusErr{
		Code: http.StatusBadGateway,
		Msg:  "in-cluster Service IP is only reachable when visual-kubernetes runs in the cluster",
	}
}

// ForbiddenIPs returns the cluster API server and (when in-cluster) Node IPs that SafeDialer must never dial.
func (c *ModelCatalog) ForbiddenIPs() []netip.Addr {
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))); err == nil {
		ips = append(ips, ip)
	}
	if c == nil || c.Model == nil {
		return ips
	}
	if obj, ok := c.Model.Object("core/Service/default/kubernetes"); ok {
		if svc, ok := obj.(*corev1.Service); ok {
			for _, raw := range append([]string{svc.Spec.ClusterIP}, svc.Spec.ClusterIPs...) {
				if ip, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil {
					ips = append(ips, ip)
				}
			}
		}
	}
	if c.InCluster {
		ents, _ := c.Model.Snapshot()
		for _, e := range ents {
			if e.Group != "core" || e.Kind != "Node" {
				continue
			}
			obj, ok := c.Model.Object(e.ID)
			if node, isNode := obj.(*corev1.Node); ok && isNode {
				for _, addr := range node.Status.Addresses {
					if addr.Type == corev1.NodeInternalIP || addr.Type == corev1.NodeExternalIP {
						if ip, err := netip.ParseAddr(strings.TrimSpace(addr.Address)); err == nil {
							ips = append(ips, ip)
						}
					}
				}
			}
		}
	}
	return ips
}

func hasExposedPort(ports []PortEntry, ns, service string, port int32) bool {
	for _, p := range ports {
		if p.Namespace == ns && p.Service == service && p.Port == port {
			return true
		}
	}
	return false
}

