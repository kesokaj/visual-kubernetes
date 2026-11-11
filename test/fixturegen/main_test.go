package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/replay"
)

var streamNames = []string{"deploy-scale", "g3-scale", "large-cluster", "missing-sources", "node-join"}

// TestFixturesAreCurrent fails when the committed streams differ from what the generator
// writes now, or when two runs differ: rerun `go run ./test/fixturegen` after a model change.
func TestFixturesAreCurrent(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if err := generate(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range streamNames {
		file := name + ".ndjson"
		first, second := read(t, filepath.Join(a, file)), read(t, filepath.Join(b, file))
		if !bytes.Equal(first, second) {
			t.Errorf("%s differs between two runs", file)
		}
		if committed := read(t, filepath.Join("..", "fixtures", "streams", file)); !bytes.Equal(first, committed) {
			t.Errorf("test/fixtures/streams/%s is stale; run go run ./test/fixturegen", file)
		}
	}
	entries, err := os.ReadDir(a)
	if err != nil || len(entries) != len(streamNames) {
		t.Errorf("generator wrote %d files (err %v), want %d", len(entries), err, len(streamNames))
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// line is a decoded record with its data kept raw for per-event decoding.
type line struct {
	AtMs  int64           `json:"atMs"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func lines(t *testing.T, name string) []line {
	t.Helper()
	var out []line
	sc := bufio.NewScanner(bytes.NewReader(read(t, filepath.Join("..", "fixtures", "streams", name+".ndjson"))))
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, l)
	}
	return out
}

// TestStreamShape checks every stream starts with a snapshot at 0 ms, never goes back in
// time and carries only the event types the UI understands. Every stream but g3-scale (a
// still frame for the perf probe) goes on past its snapshot.
func TestStreamShape(t *testing.T) {
	known := map[string]bool{"upsert": true, "delete": true, "edgeUpsert": true, "edgeDelete": true, model.EventK8s: true, replay.EventChain: true}
	for _, name := range streamNames {
		ls := lines(t, name)
		least := 2
		if name == g3Name {
			least = 1
		}
		if len(ls) < least || ls[0].Event != "snapshot" || ls[0].AtMs != 0 {
			t.Fatalf("%s: want a snapshot at 0 ms followed by deltas, got %d lines", name, len(ls))
		}
		for i, l := range ls[1:] {
			if !known[l.Event] || l.AtMs < ls[i].AtMs {
				t.Errorf("%s line %d: event %q at %d ms after %d ms", name, i+2, l.Event, l.AtMs, ls[i].AtMs)
			}
		}
	}
}

func snapshot(t *testing.T, name string) model.SnapshotData {
	t.Helper()
	var s model.SnapshotData
	if err := json.Unmarshal(lines(t, name)[0].Data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLargeClusterIsFolded(t *testing.T) {
	s := snapshot(t, "large-cluster")
	counts := map[string]int{}
	for _, e := range s.Entities {
		if e.Folded {
			continue
		}
		counts[e.Kind]++
		if e.ID == model.FoldedPodsID {
			counts["foldedPods"] = e.Count
		}
	}
	if counts["Pod"] > model.PodLimit || counts["foldedPods"] == 0 {
		t.Errorf("drawn pods = %d, folded pods = %d; want ≤ %d drawn and some folded", counts["Pod"], counts["foldedPods"], model.PodLimit)
	}
}

func TestMissingSourcesCapabilities(t *testing.T) {
	caps := snapshot(t, "missing-sources").Capabilities
	for source, want := range map[string]string{
		"metrics.k8s.io": "missing", "kubelet": "unreachable", "apiserver-metrics": "denied", "hubble-relay": "missing", "discovery": "ok",
	} {
		if caps[source].State != want {
			t.Errorf("capability %s = %q, want %q", source, caps[source].State, want)
		}
	}
}
