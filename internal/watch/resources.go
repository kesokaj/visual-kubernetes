package watch

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// namedGroups are the API groups the ClusterRole grants get/list/watch on (plan Interfaces
// § RBAC, D16); kinds of any other group, CRDs included, are never watched.
var namedGroups = map[string]bool{
	"":                          true,
	"apps":                      true,
	"batch":                     true,
	"autoscaling":               true,
	"autoscaling.k8s.io":        true,
	"networking.k8s.io":         true,
	"gateway.networking.k8s.io": true,
	"discovery.k8s.io":          true,
	"coordination.k8s.io":       true,
	"events.k8s.io":             true,
	"policy":                    true,
	"storage.k8s.io":            true,
	"metrics.k8s.io":            true,
}

// excluded resources are never listed or watched even though their group is named: Secrets
// (PRD R2; the one named Hubble secret is read by name, never watched).
var excluded = map[schema.GroupResource]bool{{Group: "", Resource: "secrets"}: true}

// Resources returns the preferred version of every list- and watchable resource of the named
// groups, sorted, without subresources or excluded resources. A group whose discovery failed
// (an unavailable aggregated API) is reported in partial and left out; err is fatal.
func Resources(ctx context.Context, d discovery.DiscoveryInterface) (gvrs []schema.GroupVersionResource, partial, err error) {
	lists, err := discovery.ServerPreferredResourcesWithContext(ctx, discovery.ToDiscoveryInterfaceWithContext(d))
	if err != nil {
		if !discovery.IsGroupDiscoveryFailedError(err) {
			return nil, nil, fmt.Errorf("discovering resources: %w", err)
		}
		partial = err
	}
	for _, l := range lists {
		gv, perr := schema.ParseGroupVersion(l.GroupVersion)
		if perr != nil || !namedGroups[gv.Group] {
			continue
		}
		for _, r := range l.APIResources {
			if wanted(gv.Group, r) {
				gvrs = append(gvrs, gv.WithResource(r.Name))
			}
		}
	}
	slices.SortFunc(gvrs, func(a, b schema.GroupVersionResource) int {
		return strings.Compare(a.String(), b.String())
	})
	return gvrs, partial, nil
}

func wanted(group string, r metav1.APIResource) bool {
	if strings.Contains(r.Name, "/") || excluded[schema.GroupResource{Group: group, Resource: r.Name}] {
		return false
	}
	return slices.Contains(r.Verbs, "list") && slices.Contains(r.Verbs, "watch")
}
