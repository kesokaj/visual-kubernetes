// Command fixturegen writes the recorded event streams the UI tests replay (plan D14):
// one NDJSON file per scenario under test/fixtures/streams, each a snapshot record at
// atMs 0 followed by the delta events of every later step. Scenarios are built from
// modeltest objects applied to a fresh model, never from a cluster, so a rerun rewrites
// the committed files byte for byte (TestFixturesAreCurrent fails when they drift).
//
//	go run ./test/fixturegen [-out test/fixtures/streams]
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// step is a set of object changes the cluster makes atMs after the recording starts.
type step struct {
	atMs    int64
	changes []model.Change
}

// scenario is one recorded stream: the capabilities its snapshot reports and its steps,
// the first of which (atMs 0) becomes the snapshot.
type scenario struct {
	name  string
	caps  map[string]model.Capability
	steps []step
}

func main() {
	out := flag.String("out", filepath.Join("test", "fixtures", "streams"), "directory the stream files are written to")
	flag.Parse()
	if err := generate(*out); err != nil {
		fmt.Fprintln(os.Stderr, "fixturegen:", err)
		os.Exit(1)
	}
}

// generate writes every scenario to dir as <name>.ndjson.
func generate(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, s := range scenarios() {
		if err := writeFile(filepath.Join(dir, s.name+".ndjson"), s); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

func writeFile(path string, s scenario) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	if err := record(bw, s); err != nil {
		f.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// record replays s through a fresh model and writes one JSON record per line.
func record(w io.Writer, s scenario) error {
	if len(s.steps) == 0 || s.steps[0].atMs != 0 {
		return fmt.Errorf("the first step must be at 0 ms")
	}
	enc := json.NewEncoder(w)
	m := model.New()
	m.Apply(s.steps[0].changes...)
	snap := m.SnapshotEvent(s.caps)
	if err := enc.Encode(model.Record{AtMs: 0, Event: snap.Type, Data: snap.Data}); err != nil {
		return err
	}
	last := int64(0)
	for _, st := range s.steps[1:] {
		if st.atMs <= last {
			return fmt.Errorf("step at %d ms is not after %d ms", st.atMs, last)
		}
		last = st.atMs
		for _, ev := range model.DeltaEvents(m.Apply(st.changes...)) {
			if err := enc.Encode(model.Record{AtMs: st.atMs, Event: ev.Type, Data: ev.Data}); err != nil {
				return err
			}
		}
	}
	return nil
}

// upserts wraps objects as add-or-update changes.
func upserts(objs ...runtime.Object) []model.Change {
	out := make([]model.Change, len(objs))
	for i, o := range objs {
		out[i] = model.Change{Obj: o}
	}
	return out
}

// deletes wraps objects as delete changes.
func deletes(objs ...runtime.Object) []model.Change {
	out := make([]model.Change, len(objs))
	for i, o := range objs {
		out[i] = model.Change{Obj: o, Deleted: true}
	}
	return out
}

// capabilities is the probe report of a healthy cluster without Hubble, with overrides.
func capabilities(overrides map[string]model.Capability) map[string]model.Capability {
	caps := map[string]model.Capability{
		"discovery":         {State: "ok", Detail: "13 groups"},
		"leases":            {State: "ok", Detail: "kube-node-lease readable"},
		"events":            {State: "ok", Detail: "events.k8s.io/v1"},
		"endpointslices":    {State: "ok", Detail: "discovery.k8s.io/v1"},
		"metrics.k8s.io":    {State: "ok", Detail: "v1beta1"},
		"kubelet":           {State: "ok", Detail: "node node-1 (10.0.0.11:10250): summary ok, 4 pods, 4 with network bytes; TLS verified"},
		"apiserver-metrics": {State: "ok", Detail: "/metrics readable"},
		"readyz":            {State: "ok", Detail: "ok"},
		"hubble-relay":      {State: "missing", Detail: "no hubble-relay Service in kube-system"},
	}
	for k, v := range overrides {
		caps[k] = v
	}
	return caps
}
