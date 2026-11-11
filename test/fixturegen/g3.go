package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// The G3 perf fixture (PRD G3, plan D42): 150 apps holding 1,000 pods on four nodes. The page
// draws at most 150 bodies (web/src/web/folding.ts MAX_BUBBLES), and the hub and the nodes are
// bodies too, so five of the apps are Deployments scaled to zero: the page's budget drops
// exactly those, and draws 150 bubbles carrying all 1,000 pod dots.
const (
	g3Name  = "g3-scale"
	g3Apps  = 150
	g3Pods  = 1000
	g3Nodes = 4
	g3Idle  = 5 // apps scaled to zero, the ones the bubble budget drops
)

// g3Scale writes the G3 fixture as one snapshot record. The live model folds past S5's 500
// pods (internal/model/fold.go PodLimit), so the whole set never goes through one model:
// each app is derived in a model of its own, with the nodes, and the views are merged, so
// every entity and edge is still exactly what the model makes of its objects.
func g3Scale(w io.Writer) error {
	ents := map[string]model.Entity{}
	edges := map[string]model.Edge{}
	ns := nodeNames(g3Nodes)
	merge := func(objs []runtime.Object) error {
		m := model.New()
		m.Apply(upserts(objs...)...)
		es, eds := m.Snapshot()
		for _, e := range es {
			if strings.HasPrefix(e.ID, "counter/") || e.Folded {
				return fmt.Errorf("app view holds %s (folded %v): the merge would miscount", e.ID, e.Folded)
			}
			ents[e.ID] = e
		}
		for _, e := range eds {
			edges[e.ID] = e
		}
		return nil
	}
	busy := g3Apps - g3Idle
	for i := range g3Apps {
		replicas := 0
		if i < busy {
			replicas = g3Pods / busy
			if i < g3Pods%busy {
				replicas++
			}
		}
		d := newDeployment(fmt.Sprintf("team-%c", 'a'+i%4), fmt.Sprintf("app-%03d", i), replicas, ns, i)
		if err := merge(append(nodes(ns), d.objects()...)); err != nil {
			return err
		}
	}
	snap := model.SnapshotData{Capabilities: capabilities(nil)}
	for _, id := range slices.Sorted(maps.Keys(ents)) {
		snap.Entities = append(snap.Entities, ents[id])
	}
	for _, id := range slices.Sorted(maps.Keys(edges)) {
		snap.Edges = append(snap.Edges, edges[id])
	}
	return json.NewEncoder(w).Encode(model.Record{AtMs: 0, Event: "snapshot", Data: snap})
}
