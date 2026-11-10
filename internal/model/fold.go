package model

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
)

// The S5 demo-size limits (PRD S5, plan Failure modes). Beyond any of them, system-namespace
// apps fold first, then the smallest apps, until the pods and bubbles left are within limits.
const (
	NodeLimit   = 20
	PodLimit    = 500
	BubbleLimit = 150
)

// Folded-app counters shown in the page corner while any app is folded.
const (
	FoldedAppsID = "counter/folded.apps"
	FoldedPodsID = "counter/folded.pods"
)

// fold marks folded apps, and their pods and satellites, with Folded and adds the counters.
// Nodes never fold: a node count over the limit only folds the system apps.
func (b *builder) fold() {
	totalPods := len(b.pods)
	if len(b.nodes) <= NodeLimit && totalPods <= PodLimit && len(b.apps) <= BubbleLimit {
		return
	}
	folded, visPods := b.chooseFolded(totalPods)
	if len(folded) == 0 {
		return
	}
	b.markFolded(folded, totalPods-visPods)
}

// chooseFolded picks the apps to fold: every system-namespace app, then the smallest apps
// until the visible pods and bubbles are within limits. It returns them and the pods left.
func (b *builder) chooseFolded(totalPods int) (map[string]bool, int) {
	folded := map[string]bool{}
	visPods, visApps := totalPods, len(b.apps)
	foldApp := func(id string) {
		folded[id] = true
		visPods -= b.apps[id].pods
		visApps--
	}
	var rest []*app
	for _, id := range slices.Sorted(maps.Keys(b.apps)) {
		if isSystemNamespace(b.apps[id].top.Namespace) {
			foldApp(id)
		} else {
			rest = append(rest, b.apps[id])
		}
	}
	// Smallest first; ties by id so the same cluster always folds the same apps.
	slices.SortStableFunc(rest, func(x, y *app) int {
		return cmp.Or(cmp.Compare(x.pods, y.pods), cmp.Compare(x.id, y.id))
	})
	for _, a := range rest {
		if visPods <= PodLimit && visApps <= BubbleLimit {
			break
		}
		foldApp(a.id)
	}
	return folded, visPods
}

// markFolded sets Folded on the folded apps and everything they own, and adds the counters.
func (b *builder) markFolded(folded map[string]bool, foldedPods int) {
	for id, e := range b.v.entities {
		if folded[e.ID] || folded[e.AppID] {
			e.Folded = true
			b.v.entities[id] = e
		}
	}
	n := len(folded)
	b.put(Entity{
		ID: FoldedAppsID, Kind: "Counter", Group: "counter", Name: "folded.apps",
		Status: fmt.Sprintf("%d apps folded", n), FilterGroup: System, Count: n,
	})
	if foldedPods > 0 {
		b.put(Entity{
			ID: FoldedPodsID, Kind: "Counter", Group: "counter", Name: "folded.pods",
			Status: fmt.Sprintf("%d pods folded", foldedPods), FilterGroup: System, Count: foldedPods,
		})
	}
}
