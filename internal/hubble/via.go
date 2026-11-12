package hubble

import (
	"slices"

	"github.com/cilium/cilium/api/v1/flow"
	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// appServices maps each drawn app to the drawn, unfolded Services that front it: the Service is
// app-owned (AppID, satellites.go D15) or selects it among several apps (EdgeSelects). Sorted.
func appServices(drawn map[string]model.Entity, edges []model.Edge) map[string][]string {
	out := map[string][]string{}
	add := func(app, svc string) {
		if e, ok := drawn[svc]; !ok || e.Folded || !isService(e) {
			return
		}
		if !slices.Contains(out[app], svc) {
			out[app] = append(out[app], svc)
		}
	}
	for id, e := range drawn {
		if e.AppID != "" {
			add(e.AppID, id)
		}
	}
	for _, ed := range edges {
		if ed.Kind == model.EdgeSelects {
			add(ed.To, ed.From)
		}
	}
	for _, svcs := range out {
		slices.Sort(svcs)
	}
	return out
}

func isService(e model.Entity) bool {
	return e.Kind == "Service" && e.Group == model.CoreGroup
}

// via is the drawn, unfolded Service a flow from src to dst was addressed to, or "" (D9).
// Hubble's destination_service wins when it fronts dst; it is empty when kube-proxy DNATs the
// ClusterIP before Cilium sees the packet, so the model then infers the Service: dst's only
// Service, or the only one whose target port matches the flow's L4 destination port. Traffic sent
// straight to a pod IP is drawn through the Service too (accepted trade-off).
func (idx *modelIndex) via(f *flow.Flow, src, dst string) string {
	if dst == model.InternetID {
		return ""
	}
	cands := idx.services[dst]
	pick := ""
	if svc := f.GetDestinationService(); svc != nil && svc.GetNamespace() != "" && svc.GetName() != "" {
		if id := model.ID(model.CoreGroup, "Service", svc.GetNamespace(), svc.GetName()); slices.Contains(cands, id) {
			pick = id
		}
	}
	if pick == "" {
		pick = idx.inferService(cands, l4DestinationPort(f))
	}
	if pick == src || pick == dst {
		return ""
	}
	return pick
}

func (idx *modelIndex) inferService(cands []string, port uint32) string {
	if len(cands) == 1 {
		return cands[0]
	}
	if port == 0 {
		return ""
	}
	match := ""
	for _, id := range cands {
		if !idx.servesPort(id, port) {
			continue
		}
		if match != "" {
			return ""
		}
		match = id
	}
	return match
}

// servesPort reports whether the Service forwards to the given pod port: an integer targetPort,
// or port when targetPort is unset. A named targetPort never matches (container ports are not read).
func (idx *modelIndex) servesPort(id string, port uint32) bool {
	obj, ok := idx.model.Object(id)
	if !ok {
		return false
	}
	svc, ok := obj.(*corev1.Service)
	if !ok {
		return false
	}
	for _, p := range svc.Spec.Ports {
		target := p.Port
		if p.TargetPort.IntVal != 0 || p.TargetPort.StrVal != "" {
			target = p.TargetPort.IntVal // 0 for a named port
		}
		if target != 0 && uint32(target) == port {
			return true
		}
	}
	return false
}

func l4DestinationPort(f *flow.Flow) uint32 {
	l4 := f.GetL4()
	if tcp := l4.GetTCP(); tcp != nil {
		return tcp.GetDestinationPort()
	}
	if udp := l4.GetUDP(); udp != nil {
		return udp.GetDestinationPort()
	}
	return 0
}

// dominantVia is the most counted via of a bucket, ties broken by ID; "" when none.
func dominantVia(vias map[string]int64) string {
	best, n := "", int64(0)
	for id, c := range vias {
		if c > n || (c == n && id < best) {
			best, n = id, c
		}
	}
	return best
}
