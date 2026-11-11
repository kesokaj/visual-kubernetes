// Package replay builds the causal chain of each observed change for the teaching replay
// (plan D17, Phase 5). A chain starts when a top workload (an object with no controller) is
// created, has its spec changed, or is deleted. Its first four steps are derived, because no
// watch shows them: authentication, authorization, admission and persisted, attributed to the
// object's newest managedFields manager. The observed steps follow from the watch stream:
// the root changed, its controlled children created, updated or deleted, each pod scheduled,
// started and ready, and the EndpointSlices that list those pods updated.
//
// Builder holds no client and starts no goroutine. It is not safe for concurrent use; the
// caller feeds it from one goroutine. Today only test/fixturegen does (the recorded streams);
// the live watch pump in cmd/visual-kubernetes does not call it yet (plan D48).
package replay

import (
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// EventChain is the SSE event name of a chain message (plan Interfaces § SSE events).
const EventChain = "chain"

// Step is one link of a chain: at what time which actor did what to which object. Object is a
// D13 entity id; Derived marks the steps inferred rather than observed.
type Step struct {
	At      string `json:"at"`
	Actor   string `json:"actor"`
	Verb    string `json:"verb"`
	Object  string `json:"object"`
	Derived bool   `json:"derived"`
}

// Chain is the `chain` event's data, `{id, steps[]}`. A chain is sent again, whole and under
// the same id, each time it gains steps, so a client keeps the newest copy per id.
type Chain struct {
	ID    string `json:"id"`
	Steps []Step `json:"steps"`
}

// Verbs of the steps. The derived ones are the D17 names verbatim.
const (
	VerbAuthentication   = "authentication"
	VerbAuthorization    = "authorization"
	VerbAdmission        = "admission"
	VerbPersisted        = "persisted"
	VerbCreated          = "created"
	VerbUpdated          = "updated"
	VerbDeleted          = "deleted"
	VerbScheduled        = "scheduled"
	VerbFailedScheduling = "failed scheduling"
	VerbStarted          = "started"
	VerbReady            = "ready"
)

// derivedVerbs are the API server's steps before an object is stored, in order.
var derivedVerbs = []string{VerbAuthentication, VerbAuthorization, VerbAdmission, VerbPersisted}

// Actors named when the object does not say who acted.
const (
	actorClient     = "api client"
	actorController = "kube-controller-manager"
	actorScheduler  = "default-scheduler"
	actorSlices     = "endpointslice-controller"
	actorKubelet    = "kubelet"
)

// Bounds keep a long-running server's memory flat whatever the cluster does.
const (
	maxChains = 16              // open chains; the oldest is closed past this
	MaxSteps  = 64              // steps per chain; later steps are dropped (and a longer recorded chain is refused)
	chainTTL  = 3 * time.Minute // a chain closes this long after it started
)

// stage orders the steps one batch adds to a chain, so a batch that holds several stages at
// once (a live watch batch, or a recording step) still reads in D17 order.
type stage int

const (
	stageRoot stage = iota
	stageChild
	stagePod
	stageScheduled
	stageStarted
	stageReady
	stageSlice
)

// atString formats an observation time as RFC 3339 UTC with milliseconds.
func atString(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// event wraps a copy of c as a `chain` SSE event, so later steps never alias what was sent.
func event(c *Chain) model.Event {
	steps := make([]Step, len(c.Steps))
	copy(steps, c.Steps)
	return model.Event{Type: EventChain, Data: Chain{ID: c.ID, Steps: steps}}
}
