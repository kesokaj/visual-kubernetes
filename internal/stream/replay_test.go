package stream

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/hubble"
	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/replay"
)

func TestLoadRecordingRejects(t *testing.T) {
	snap := `{"atMs":0,"event":"snapshot","data":{"entities":[],"edges":[],"capabilities":{}}}`
	for name, body := range map[string]string{
		"empty":                "",
		"not JSON":             "nope",
		"first not snapshot":   `{"atMs":0,"event":"upsert","data":{"entity":{"id":"x"}}}`,
		"snapshot not at 0":    `{"atMs":5,"event":"snapshot","data":{}}`,
		"missing atMs":         snap + "\n" + `{"event":"upsert","data":{"entity":{}}}`,
		"negative atMs":        snap + "\n" + `{"atMs":-1,"event":"upsert","data":{"entity":{}}}`,
		"atMs goes back":       snap + "\n" + `{"atMs":9,"event":"metrics","data":{}}` + "\n" + `{"atMs":8,"event":"metrics","data":{}}`,
		"event name injects":   snap + "\n" + `{"atMs":1,"event":"upsert\ndata: x","data":{}}`,
		"long event name":      snap + "\n" + `{"atMs":1,"event":"` + strings.Repeat("a", 33) + `","data":{}}`,
		"typed data is wrong":  snap + "\n" + `{"atMs":1,"event":"upsert","data":{"entity":"x"}}`,
		"unknown data absent":  snap + "\n" + `{"atMs":1,"event":"custom"}`,
		"event data is wrong":  snap + "\n" + `{"atMs":1,"event":"event","data":{"k8sEvent":"x"}}`,
		"event without id":     snap + "\n" + `{"atMs":1,"event":"event","data":{"k8sEvent":{"reason":"X"}}}`,
		"chain data is wrong":  snap + "\n" + `{"atMs":1,"event":"chain","data":{"steps":"x"}}`,
		"chain without id":     snap + "\n" + `{"atMs":1,"event":"chain","data":{"steps":[{"verb":"created","object":"core/Pod/a/b"}]}}`,
		"chain without steps":  snap + "\n" + `{"atMs":1,"event":"chain","data":{"id":"c","steps":[]}}`,
		"chain step no object": snap + "\n" + `{"atMs":1,"event":"chain","data":{"id":"c","steps":[{"verb":"created"}]}}`,
		"chain too long":       snap + "\n" + `{"atMs":1,"event":"chain","data":{"id":"c","steps":[` + strings.TrimSuffix(strings.Repeat(`{"verb":"created","object":"core/Pod/a/b"},`, 65), ",") + `]}}`,
		"flow data is wrong":   snap + "\n" + `{"atMs":1,"event":"flow","data":"x"}`,
		"flow without src":     snap + "\n" + `{"atMs":1,"event":"flow","data":{"dst":"b","verdict":"FORWARDED","bytes":0}}`,
		"flow self loop":       snap + "\n" + `{"atMs":1,"event":"flow","data":{"src":"a","dst":"a","verdict":"FORWARDED","bytes":0}}`,
		"flow bad verdict":     snap + "\n" + `{"atMs":1,"event":"flow","data":{"src":"a","dst":"b","verdict":"AUDIT","bytes":0}}`,
		"flow negative bytes":  snap + "\n" + `{"atMs":1,"event":"flow","data":{"src":"a","dst":"b","verdict":"FORWARDED","bytes":-1}}`,
		"flow negative count":  snap + "\n" + `{"atMs":1,"event":"flow","data":{"src":"a","dst":"b","verdict":"FORWARDED","bytes":0,"count":-1}}`,
	} {
		if _, err := LoadRecording(strings.NewReader(body)); err == nil {
			t.Errorf("%s: LoadRecording succeeded, want an error", name)
		}
	}
}

func fixture(t *testing.T, name string) *Recording {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "test", "fixtures", "streams", name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rec, err := LoadRecording(f)
	if err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
	return rec
}

// TestFixturesLoad: every committed recording is a valid --replay file.
func TestFixturesLoad(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "test", "fixtures", "streams", "*.ndjson"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, p := range paths {
		fixture(t, filepath.Base(p))
	}
}

// TestPlayTiming: the snapshot is in the hub before anyone subscribes; later steps wait for
// the first subscriber, then arrive at their offsets from it.
func TestPlayTiming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := fixture(t, "deploy-scale.ndjson")
		h := NewHub(HubOptions{})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- rec.Play(ctx, h) }()
		synctest.Wait()
		if len(h.ring) != len(rec.steps[0].events) {
			t.Fatalf("before any subscriber: ring %d, want the %d snapshot events", len(h.ring), len(rec.steps[0].events))
		}
		time.Sleep(time.Hour) // nothing plays while nobody watches
		synctest.Wait()
		if len(h.ring) != len(rec.steps[0].events) {
			t.Fatalf("idle hour: ring %d, want unchanged", len(h.ring))
		}
		sub, err := h.Subscribe("")
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()
		start := time.Now()
		for _, st := range rec.steps[1:] {
			for range st.events {
				e := <-sub.C
				if got := time.Since(start); got != time.Duration(st.atMs)*time.Millisecond {
					t.Fatalf("%s arrived at %v, want %dms", e.Type, got, st.atMs)
				}
			}
		}
		if err := <-done; err != nil {
			t.Fatalf("Play: %v", err)
		}
	})
}

func TestPlayStopsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := fixture(t, "deploy-scale.ndjson")
		h := NewHub(HubOptions{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- rec.Play(ctx, h) }()
		sub, err := h.Subscribe("")
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Play after cancel: %v", err)
		}
	})
}

func TestReplaySources(t *testing.T) {
	h := NewHub(HubOptions{})
	pod := model.Entity{ID: "core/Pod/shop/web-1", Kind: "Pod", Group: "core", Name: "web-1", Namespace: "shop", Status: "Running", FilterGroup: model.Pods}
	publish(t, h, snapshotOf(pod))
	ref := model.Ref{Group: "core", Kind: "Pod", Namespace: "shop", Name: "web-1"}
	missing := model.Ref{Group: "core", Kind: "Pod", Namespace: "shop", Name: "gone"}

	out, err := ReplayObjects{Hub: h}.Object(t.Context(), ref)
	if err != nil || !strings.Contains(string(out), "status: Running") {
		t.Errorf("ReplayObjects = %q, %v; want the entity as YAML", out, err)
	}
	if _, err := (ReplayObjects{Hub: h}).Object(t.Context(), missing); err != ErrNotFound {
		t.Errorf("ReplayObjects(missing) err = %v, want ErrNotFound", err)
	}
	rc, err := ReplayLogs{Hub: h}.Logs(t.Context(), ref, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	if string(body) != ReplayLogNote {
		t.Errorf("ReplayLogs = %q, want the note", body)
	}
	if _, err := (ReplayLogs{Hub: h}).Logs(t.Context(), missing, "", 10); err != ErrNotFound {
		t.Errorf("ReplayLogs(missing) err = %v, want ErrNotFound", err)
	}
}

// TestUnknownEventPassesThrough: types the recording does not decode (metrics, custom) replay
// as recorded.
func TestUnknownEventPassesThrough(t *testing.T) {
	body := `{"atMs":0,"event":"snapshot","data":{"entities":[],"edges":[],"capabilities":{}}}` + "\n" +
		`{"atMs":0,"event":"custom","data":{"src":"a",  "dst":"b"}}`
	rec, err := LoadRecording(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub(HubOptions{})
	publish(t, h, rec.steps[0].events...)
	last := h.ring[len(h.ring)-1]
	var got map[string]string
	if last.Type != "custom" || json.Unmarshal(last.Data, &got) != nil || got["dst"] != "b" || strings.Contains(string(last.Data), "  ") {
		t.Errorf("custom entry = %s %s, want the compact recorded data", last.Type, last.Data)
	}
}

// TestFlowReplaysTyped: a recorded flow is decoded as hubble.Flow and reaches the hub's ring
// with its endpoints, verdict and count intact.
func TestFlowReplaysTyped(t *testing.T) {
	body := `{"atMs":0,"event":"snapshot","data":{"entities":[],"edges":[],"capabilities":{}}}` + "\n" +
		`{"atMs":5,"event":"flow","data":{"src":"app/vk-demo/Deployment/web","dst":"app/vk-demo/Deployment/cache","verdict":"DROPPED","bytes":0,"count":3}}`
	rec, err := LoadRecording(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	ev := rec.steps[1].events[0]
	fl, ok := ev.Data.(hubble.Flow)
	if !ok || fl.Src != "app/vk-demo/Deployment/web" || fl.Verdict != hubble.VerdictDropped || fl.Count != 3 {
		t.Fatalf("flow decoded as %T %+v", ev.Data, ev.Data)
	}
	h := NewHub(HubOptions{})
	publish(t, h, rec.steps[0].events...)
	publish(t, h, ev)
	last := h.ring[len(h.ring)-1]
	var got hubble.Flow
	if last.Type != hubble.EventFlow || json.Unmarshal(last.Data, &got) != nil || got.Dst != "app/vk-demo/Deployment/cache" || got.Count != 3 {
		t.Errorf("ring entry = %s %s", last.Type, last.Data)
	}
}

// TestChainReplaysTyped: a recorded chain is decoded as replay.Chain and reaches the hub's
// ring with its steps intact.
func TestChainReplaysTyped(t *testing.T) {
	body := `{"atMs":0,"event":"snapshot","data":{"entities":[],"edges":[],"capabilities":{}}}` + "\n" +
		`{"atMs":5,"event":"chain","data":{"id":"c1","steps":[{"at":"2026-11-10T10:00:00.000Z","actor":"kubectl","verb":"authentication","object":"apps/Deployment/a/b","derived":true}]}}`
	rec, err := LoadRecording(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	ev := rec.steps[1].events[0]
	c, ok := ev.Data.(replay.Chain)
	if !ok || c.ID != "c1" || len(c.Steps) != 1 || !c.Steps[0].Derived || c.Steps[0].Actor != "kubectl" {
		t.Fatalf("chain decoded as %T %+v", ev.Data, ev.Data)
	}
	h := NewHub(HubOptions{})
	publish(t, h, rec.steps[0].events...)
	publish(t, h, ev)
	last := h.ring[len(h.ring)-1]
	var got replay.Chain
	if last.Type != replay.EventChain || json.Unmarshal(last.Data, &got) != nil || got.Steps[0].Object != "apps/Deployment/a/b" {
		t.Errorf("ring entry = %s %s", last.Type, last.Data)
	}
}
