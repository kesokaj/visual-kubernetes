package watch

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResources(t *testing.T) {
	got, partial, err := Resources(t.Context(), fakeDiscovery())
	if err != nil || partial != nil {
		t.Fatalf("Resources: err=%v partial=%v", err, partial)
	}
	// Not secrets (R2), not pods/log (subresource), not bindings (no list/watch), not widgets
	// (group not in the ClusterRole); sorted by String().
	want := []schema.GroupVersionResource{
		{Version: "v1", Resource: "configmaps"},
		{Version: "v1", Resource: "nodes"},
		podsGVR,
		deployGVR,
		scGVR,
	}
	if !slices.Equal(got, want) {
		t.Errorf("Resources() = %v\nwant %v", got, want)
	}
}
