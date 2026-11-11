# Teaching examples

Small manifests for live demos with visual-kubernetes open on the projector. Each one has a
script that applies it, drives the change the audience should watch, and cleans up.

> [!TIP]
> **Teaching replay:** Press **R** after a change to replay its causal chain at 1 step/s (**Space** pauses/resumes, **→** steps forward, **Esc** hides the caption), both on a live cluster and on `--replay test/fixtures/streams/deploy-scale.ndjson`.

The scripts use your current `kubectl` context (or `KUBECONFIG`) and print it first: point
them at a demo cluster, never a production one. Everything goes into the namespace `vk-demo`
(override with `NS=...`); `up` creates it, `down` deletes the example's objects.

| Example | What it shows | Script |
|---------|---------------|--------|
| `01-deployment.yaml` | A Deployment and Service: the create chain (API server, ReplicaSet, pods, scheduling, readiness, EndpointSlice), then a scale (the same story is recorded with its chain in `deploy-scale.ndjson`) | `01-deployment.sh up`, `scale 4`, `down` |
| `02-nodes.yaml` | One-CPU pods scaled past what the nodes hold: Pending pods (FailedScheduling); with node autoscaling (GKE Autopilot, a cluster autoscaler) new worker nodes join and take them | `02-nodes.sh up`, `more 8`, `down` |
| `03-hpa.yaml` | A HorizontalPodAutoscaler on CPU, with an idle load generator to switch on | `03-hpa.sh up`, `load`, `calm`, `down` |
| `03b-vpa.yaml` | A VerticalPodAutoscaler raising requests that are too small | `03b-vpa.sh up`, `down` |
| `04-traffic.yaml` | Three-tier service traffic (`web` → `api` → `cache`) plus a `NetworkPolicy` blocking direct `web` → `cache` calls (red drop particles) | `04-traffic.sh up`, `allow-direct`, `block-direct`, `down` |

## Requirements

- `03-hpa` needs `metrics.k8s.io` (metrics-server). GKE has it; `test/kind/up.sh` installs it
  on the kind test cluster. The HPA scales up a minute or two after `load` and back down
  about 5 minutes after `calm` (its stabilization window).
- `03b-vpa` needs the VerticalPodAutoscaler CRDs and controllers. GKE: enable vertical Pod
  autoscaling on the cluster (on by default in Autopilot). Elsewhere: install the
  `vertical-pod-autoscaler` from kubernetes/autoscaler. **Without them the API server
  rejects `03b-vpa.yaml`** (`no matches for kind "VerticalPodAutoscaler"`), and
  `03b-vpa.sh up` stops with a pointer here. Neither the scripts nor visual-kubernetes ever
  install it: the app is read-only and never enables cluster features.
- `02-nodes` on kind: the cluster has fixed nodes, so the extra pods stay Pending. That still
  shows the scheduler's FailedScheduling chain.
- `04-traffic` needs Hubble Relay (Cilium, GKE Dataplane V2, or `test/kind/up.sh --cilium`
  locally) for live flow and `DROPPED` policy particles, or an in-cluster Prometheus exposing
  OpenTelemetry `servicegraph`, Istio or Hubble flow metrics. Without a traffic source, the
  three Deployments, their Services and `cache-only-from-api` still appear in the web.

## Checking the manifests

On the kind test cluster (`test/kind/up.sh`), every manifest passes a server-side dry run
except `03b-vpa.yaml`, which fails until the VPA CRDs are installed:

```bash
for f in examples/*.yaml; do
  kubectl --kubeconfig .agents/scratch/kind/kubeconfig apply --dry-run=server -f "$f"
done
```
