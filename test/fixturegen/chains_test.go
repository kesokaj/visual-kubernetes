package main

import (
	"strings"
	"testing"

	"github.com/kesokaj/visual-kubernetes/internal/replay"
)

// TestDeployScaleCarriesTheChain: the recording the replay keys are tested on (replay.spec.ts)
// carries the scale's chain, re-sent as it grows, ending with the D17 steps: the four derived
// ones first, then the Deployment, the ReplicaSet, both pods created, scheduled, started and
// ready, and the EndpointSlice.
func TestDeployScaleCarriesTheChain(t *testing.T) {
	var last replay.Chain
	sends := 0
	for _, l := range lines(t, "deploy-scale") {
		if l.Event != replay.EventChain {
			continue
		}
		sends++
		var c replay.Chain
		decode(t, l.Data, &c)
		if last.ID != "" && c.ID != last.ID {
			t.Fatalf("second chain %s after %s: the scale is one story", c.ID, last.ID)
		}
		if len(c.Steps) < len(last.Steps) {
			t.Errorf("chain shrank from %d to %d steps", len(last.Steps), len(c.Steps))
		}
		last = c
	}
	if sends != 4 {
		t.Errorf("chain sent %d times, want 4 (one per step after the snapshot)", sends)
	}
	var got []string
	for i, s := range last.Steps {
		if s.Derived != (i < 4) {
			t.Errorf("step %d derived = %v", i, s.Derived)
		}
		got = append(got, s.Verb+" "+strings.SplitN(s.Object, "/", 3)[1])
	}
	want := "authentication Deployment|authorization Deployment|admission Deployment|persisted Deployment|" +
		"updated Deployment|updated ReplicaSet|created Pod|created Pod|scheduled Pod|scheduled Pod|" +
		"started Pod|started Pod|ready Pod|ready Pod|updated EndpointSlice"
	if strings.Join(got, "|") != want {
		t.Errorf("steps\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
	if last.Steps[0].Actor != "kube-controller-manager" {
		t.Errorf("derived actor %q, want the managedFields manager", last.Steps[0].Actor)
	}
}

// TestG3ScaleCounts holds the perf fixture to G3: 150 apps and 1,000 pods, nothing folded,
// no counters, five idle apps for the page's budget to drop, the hub and four nodes.
func TestG3ScaleCounts(t *testing.T) {
	s := snapshot(t, g3Name)
	n := map[string]int{}
	pods := map[string]int{}
	for _, e := range s.Entities {
		switch {
		case e.Folded || strings.HasPrefix(e.ID, "counter/"):
			t.Errorf("%s is folded or a counter", e.ID)
		case strings.HasPrefix(e.ID, "app/"):
			n["app"]++
		case e.Kind == "Pod":
			n["pod"]++
			pods[e.AppID]++
		default:
			n[e.Kind]++
		}
	}
	idle := 0
	for _, e := range s.Entities {
		if strings.HasPrefix(e.ID, "app/") && pods[e.ID] == 0 {
			idle++
		}
	}
	if n["app"] != g3Apps || n["pod"] != g3Pods || n["Node"] != g3Nodes || n["APIServer"] != 1 || idle != g3Idle {
		t.Errorf("g3-scale holds %v with %d idle apps; want %d apps, %d pods, %d nodes, 1 hub, %d idle", n, idle, g3Apps, g3Pods, g3Nodes, g3Idle)
	}
	if len(s.Edges) == 0 || s.Capabilities["discovery"].State != "ok" {
		t.Errorf("g3-scale has %d edges and capabilities %v", len(s.Edges), s.Capabilities)
	}
}
