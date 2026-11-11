package hubble

import (
	"slices"
	"strings"

	"github.com/cilium/cilium/api/v1/flow"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// Cilium's reserved world identities, from the iota order in
// github.com/cilium/cilium@v1.18.16/pkg/identity/numericidentity.go:95-133 (World = 2,
// WorldIPv4 = 9, WorldIPv6 = 10). Kept local so the resolver does not import pkg/identity.
const (
	identityWorld     = 2
	identityWorldIPv4 = 9
	identityWorldIPv6 = 10
	// identityKubeAPIServer = 7 carries reserved:world too (pkg/labels/labels.go:108-111),
	// but it is the cluster's API server, never the internet.
	identityKubeAPIServer = 7
)

const apiServerLabel = "reserved:kube-apiserver"

// worldLabel prefixes Cilium's reserved world labels: reserved:world, reserved:world-ipv4 and
// reserved:world-ipv6 (pkg/labels/labels.go:31-40).
const worldLabel = "reserved:world"

// isWorld reports whether a Hubble endpoint is outside the cluster. A namespaced endpoint is
// never the world, whatever identity it claims.
func isWorld(ep *flow.Endpoint) bool {
	if ep == nil || ep.GetNamespace() != "" || ep.GetIdentity() == identityKubeAPIServer {
		return false
	}
	if slices.Contains(ep.GetLabels(), apiServerLabel) {
		return false
	}
	switch ep.GetIdentity() {
	case identityWorld, identityWorldIPv4, identityWorldIPv6:
		return true
	}
	for _, l := range ep.GetLabels() {
		if strings.HasPrefix(l, worldLabel) {
			return true
		}
	}
	return false
}

// internet returns ext/internet when the model draws it (only with an entrypoint, plan D4),
// else "" so the flow is dropped like any other unresolved endpoint.
func (idx *modelIndex) internet() string {
	if idx.isDrawn(model.InternetID) {
		return model.InternetID
	}
	return ""
}

// promExternal reports whether one Prometheus side names the world: an empty namespace with a
// name of "world" or reserved:world* in any of the side's name labels (plan D11).
func promExternal(m map[string]string, ns1, n1, ns2, n2, pairKey string) bool {
	world := func(v string) bool { return v == "world" || strings.HasPrefix(v, worldLabel) }
	return (m[ns1] == "" && world(m[n1])) || (m[ns2] == "" && world(m[n2])) || world(m[pairKey])
}
