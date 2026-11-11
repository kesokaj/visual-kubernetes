package model

import (
	"fmt"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
)

// InternetID is the synthetic entity that stands for traffic from outside the cluster. It is
// drawn only while at least one unfolded entrypoint exists (plan D4, D10), and it is never an
// object, so /api/objects never serves it.
const InternetID = "ext/internet"

// addInternet puts ext/internet with one routes edge per entrypoint target. It runs after
// fold so a folded entrypoint never keeps the globe alive or links into a folded app.
func (b *builder) addInternet() {
	targets := map[string]bool{}
	for _, e := range b.v.entities {
		if e.Folded || !isEntrypoint(e) {
			continue
		}
		t := e.ID
		if e.AppID != "" {
			t = e.AppID // merged into its app (D15): the app is what the internet reaches
		}
		targets[t] = true
	}
	if len(targets) == 0 {
		return
	}
	status := fmt.Sprintf("%d entrypoints", len(targets))
	if len(targets) == 1 {
		status = "1 entrypoint"
	}
	b.put(Entity{
		ID: InternetID, Kind: "Internet", Group: "ext", Name: "internet",
		Status: status, FilterGroup: Ingress,
	})
	for _, t := range slices.Sorted(maps.Keys(targets)) {
		b.edge(EdgeRoutes, InternetID, t)
	}
}

// isEntrypoint reports whether traffic from outside the cluster can enter through e: a
// LoadBalancer or NodePort Service, an Ingress or a Gateway. HTTPRoutes are reached through
// their parent Gateway, so they are not entrypoints themselves.
func isEntrypoint(e Entity) bool {
	switch {
	case e.Group == CoreGroup && e.Kind == "Service":
		return e.Status == string(corev1.ServiceTypeLoadBalancer) || e.Status == string(corev1.ServiceTypeNodePort)
	case e.Group == "networking.k8s.io" && e.Kind == "Ingress":
		return true
	case e.Group == "gateway.networking.k8s.io" && e.Kind == "Gateway":
		return true
	}
	return false
}
