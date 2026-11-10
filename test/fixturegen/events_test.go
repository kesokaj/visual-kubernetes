package main

import (
	"encoding/json"
	"testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// TestStreamEventsNameDrawnEntities checks the recorded Kubernetes Events (D29): the
// scenarios that should carry them do, and every Event names an entity id or owner id the
// stream has already sent, the ids the panel looks Events up by (Panel.tsx eventIds).
func TestStreamEventsNameDrawnEntities(t *testing.T) {
	want := map[string][]string{
		"deploy-scale": {
			"ScalingReplicaSet@apps/Deployment/shop/web",
			"SuccessfulCreate@apps/ReplicaSet/shop/web-7d4b9c",
			"Scheduled@core/Pod/shop/web-7d4b9c-x002",
			"Scheduled@core/Pod/shop/web-7d4b9c-x003",
		},
		"node-join": {
			"NodeReady@core/Node/_/node-3",
			"Scheduled@core/Pod/kube-system/kube-proxy-node-3",
		},
	}
	for _, name := range streamNames {
		got := map[string]bool{}
		seen := map[string]bool{}
		for _, l := range lines(t, name) {
			switch l.Event {
			case "snapshot":
				var s model.SnapshotData
				decode(t, l.Data, &s)
				for _, e := range s.Entities {
					seen[e.ID], seen[e.OwnerID] = true, true
				}
			case "upsert":
				var d model.EntityData
				decode(t, l.Data, &d)
				seen[d.Entity.ID], seen[d.Entity.OwnerID] = true, true
			case model.EventK8s:
				var d model.K8sEventData
				decode(t, l.Data, &d)
				ev := d.K8sEvent
				if ev.ID == "" || !seen[ev.ID] || ev.Type == "" || ev.Message == "" || ev.At == "" {
					t.Errorf("%s: event %+v names no entity sent so far, or lacks a field", name, ev)
				}
				got[ev.Reason+"@"+ev.ID] = true
			}
		}
		for _, w := range want[name] {
			if !got[w] {
				t.Errorf("%s: no %s event", name, w)
			}
		}
	}
}

func decode(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}
