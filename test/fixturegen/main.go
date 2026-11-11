// Command fixturegen writes the recorded event streams the UI tests replay (plan D14):
// one NDJSON file per scenario under test/fixtures/streams, each a snapshot record at
// atMs 0 followed by the delta, Kubernetes `event` and `chain` records of every later step
// (the snapshot carries no Events, so a scenario's Events belong in its later steps; chains
// come from internal/replay, primed with the snapshot's objects). Scenarios are built from
// modeltest objects applied to a fresh model, never from a cluster, so a rerun rewrites the
// committed files byte for byte (TestFixturesAreCurrent fails when they drift). The one
// exception to "through the model" is g3-scale.ndjson (g3.go, plan D42), written as a
// snapshot directly so it can hold more pods than the model's S5 folding lets through.
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
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/replay"
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
		write := func(w io.Writer) error { return record(w, s) }
		if err := writeFile(filepath.Join(dir, s.name+".ndjson"), write); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	if err := writeFile(filepath.Join(dir, g3Name+".ndjson"), g3Scale); err != nil {
		return fmt.Errorf("%s: %w", g3Name, err)
	}
	return nil
}

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	if err := write(bw); err != nil {
		f.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// record replays s through a fresh model and a chain builder and writes one JSON record per
// line: each step's model events, then the chains it extended.
func record(w io.Writer, s scenario) error {
	if len(s.steps) == 0 || s.steps[0].atMs != 0 {
		return fmt.Errorf("the first step must be at 0 ms")
	}
	enc := json.NewEncoder(w)
	m := model.New()
	m.Apply(s.steps[0].changes...)
	chains := replay.New()
	chains.Prime(s.steps[0].changes...)
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
		evs := m.ApplyEvents(st.changes...)
		evs = append(evs, chains.Observe(eventBase.Add(time.Duration(st.atMs)*time.Millisecond), st.changes...)...)
		for _, ev := range evs {
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
