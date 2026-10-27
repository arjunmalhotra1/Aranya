# ClusterView Operator

A Kubernetes operator written in Go with [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime). It watches Nodes and Pods, writes an aggregated summary into the status of a `ClusterView` custom resource, and serves a web UI that shows each node's status and capacity and the pods running on it.

**Live demo** on the provided DigitalOcean cluster: http://138.197.59.193

```console
$ kubectl get clusterview
NAME                 NODES   READY   PODS   AGE
clusterview-sample   3       3       19     5m
```

## What it does

- **`ClusterView` custom resource** (cluster-scoped). The spec says what to observe; the operator fills in the status.
  - `spec.namespaces`: count only pods in these namespaces. Empty means all. Nodes are always counted.
  - `spec.refreshInterval`: how often to re-check even if nothing changed (default `30s`, minimum `5s`).
  - `status`: node count, ready and not-ready nodes, pod count, pods by phase, pods per node, `Ready`/`Degraded` conditions, `observedGeneration` and `lastUpdated`.
  - Events: the operator records a `Warning Degraded` event when counting fails and a `Normal Recovered` event when it works again (only when the state changes), visible with `kubectl describe clusterview`.
- **HTTP API and web UI**, served by the operator itself on port 8090 and exposed through a LoadBalancer Service:
  - a card per node with readiness, conditions, CPU and memory allocatable versus capacity, and pod count;
  - click a node to list its pods with phase, ready containers and restart count, plus the node's recent Kubernetes Events; click a pod to see its Events, with Warnings highlighted;
  - pick a ClusterView to filter pods by its namespaces; the page refreshes every 5 seconds.

## Install

You need `kubectl` and a cluster where you can create CRDs and cluster-wide RBAC. The image is public and multi-architecture (linux/amd64 and linux/arm64).

```bash
# 1. Install the operator: CRD, RBAC, Deployment and UI Service
kubectl apply -f https://raw.githubusercontent.com/arjunmalhotra1/Aranya/main/dist/install.yaml
kubectl -n clusterview-operator-system rollout status deploy/clusterview-operator-controller-manager

# 2. Create a ClusterView
kubectl apply -f https://raw.githubusercontent.com/arjunmalhotra1/Aranya/main/config/samples/observability_v1alpha1_clusterview.yaml
```

If step 2 reports `no matches for kind "ClusterView"`, the CRD was still registering; run it again after a few seconds.

Check that it works:

```bash
kubectl get clusterview
kubectl get clusterview clusterview-sample -o yaml   # full status, including conditions
```

### Open the UI

On a cloud cluster the UI Service gets a public IP. It can take one to three minutes after the IP appears before the load balancer accepts traffic.

```bash
kubectl -n clusterview-operator-system get svc clusterview-operator-ui
# open http://<EXTERNAL-IP>
```

On clusters without a load balancer (kind, minikube), use a port-forward instead:

```bash
kubectl -n clusterview-operator-system port-forward svc/clusterview-operator-ui 8080:80
# open http://localhost:8080
```

The UI is read-only and unauthenticated, and it shows node and pod names. That is acceptable for this demo; in production it would sit behind authentication or an internal-only load balancer.

### Configure a ClusterView

```yaml
apiVersion: observability.arjunmalhotra.dev/v1alpha1
kind: ClusterView
metadata:
  name: clusterview-sample
spec:
  namespaces: [default, kube-system]   # empty or omitted: all namespaces
  refreshInterval: 30s
```

The API server validates the spec, so mistakes fail at `kubectl apply` instead of silently: each entry in `namespaces` must be a valid namespace name (lowercase letters, digits and `-`, at most 63 characters), and `refreshInterval` must be at least `5s` (it defaults to `30s` when omitted).

```console
$ kubectl patch clusterview clusterview-sample --type merge -p '{"spec":{"refreshInterval":"1s"}}'
The ClusterView "clusterview-sample" is invalid: spec.refreshInterval: Invalid value: "1s": refreshInterval must be at least 5s
```

Changes take effect on the next reconcile, within milliseconds. When `status.observedGeneration` equals `metadata.generation`, the status reflects your latest edit.

### HTTP API

| Endpoint | Returns |
| --- | --- |
| `GET /api/clusterviews` | Every ClusterView with its namespace filter and status summary |
| `GET /api/nodes` | Nodes with readiness, conditions, CPU and memory capacity and allocatable, and pod count |
| `GET /api/nodes/{name}/pods` | Pods on one node with phase, restarts and ready containers |
| `GET /api/nodes/{name}/events` | The node's most recent Events (up to 20, newest first) |
| `GET /api/namespaces/{namespace}/pods/{pod}/events` | The pod's most recent Events (up to 20, newest first) |

Add `?view=<clusterview-name>` to `/api/nodes` or `/api/nodes/{name}/pods` to count only pods in that ClusterView's namespaces. An unknown node or view returns 404.

### Uninstall

```bash
kubectl delete -f https://raw.githubusercontent.com/arjunmalhotra1/Aranya/main/config/samples/observability_v1alpha1_clusterview.yaml
kubectl delete -f https://raw.githubusercontent.com/arjunmalhotra1/Aranya/main/dist/install.yaml
```

Deleting the UI Service also removes the cloud load balancer.

## Architecture

One binary runs a controller-runtime manager. The reconciler and the HTTP server share the manager's informer cache, so neither calls the Kubernetes API server per request.

```
                    Kubernetes API server
                            |  list + watch: Nodes, Pods, ClusterViews
 +--------------------------v-----------------------------------------+
 |  operator pod (controller-runtime manager)                         |
 |                                                                    |
 |      informer cache  (+ field index: pods by spec.nodeName)        |
 |           |                                   |                    |
 |      Reconciler                         HTTP server :8090          |
 |      summary.Compute                    /api/... + embedded UI     |
 |      writes ClusterView status          reads the cache only       |
 +-----------|-----------------------------------|--------------------+
             v                                   v
   kubectl get clusterview          LoadBalancer Service -> browser
```

### Reconcile flow

A reconcile is triggered by a ClusterView being created or its spec changing, by any Node or Pod event (mapped to every ClusterView; the work queue collapses bursts into one reconcile), and by `RequeueAfter: spec.refreshInterval` as a safety net.

1. Get the ClusterView. If it no longer exists, stop without error.
2. List nodes and pods from the cache. On failure, set `Ready=False` and `Degraded=True` with the error, keep the last good counts, record a `Degraded` event if it was not already degraded, and return the error so controller-runtime retries with backoff.
3. `summary.Compute` (a pure function) counts nodes, readiness, pods, phases and pods per node, applying `spec.namespaces` to pods only.
4. Set `Ready=True` and `Degraded=False` with `meta.SetStatusCondition`, which only moves `lastTransitionTime` when a value flips, and record `observedGeneration`. If it was degraded before, record a `Recovered` event after the status is written.
5. Compare with the stored status, ignoring `lastUpdated`. If nothing changed, skip the write.
6. Otherwise stamp `lastUpdated` and call `Status().Update`, re-reading and recomputing on a conflict (`retry.RetryOnConflict`).
7. Return `RequeueAfter: refreshInterval`.

## Design decisions and tradeoffs

| Decision | Why | Tradeoff |
| --- | --- | --- |
| Status holds aggregates only; per-node and per-pod detail comes from the HTTP API | Status lives in etcd, which caps object size; counts stay small at any scale | Detail is not visible in `kubectl get -o yaml` |
| Cluster-scoped `ClusterView` | It summarizes Nodes, which are cluster-scoped | Needs a ClusterRole; no per-namespace views |
| Namespace filter applied when counting, not when watching | The cache is configured at startup, spec can change any time; edits apply on the next reconcile without a restart | Every pod is cached, even when filtered out |
| Watches plus a refresh interval | Watches react within milliseconds; the interval covers missed events | Two triggers; the work queue de-duplicates them |
| `GenerationChangedPredicate` on ClusterViews and skipping no-op status writes | The operator's own status writes do not wake it again; an idle cluster causes zero writes | `lastUpdated` means "numbers last changed", not "last checked" |
| Counting logic in a pure `summary` package; `IsNodeReady` and `InNamespaces` shared with the API | Table-tested in milliseconds; the UI and the status cannot disagree | One more package |
| HTTP API inside the operator, reading the shared cache, on every replica (`NeedLeaderElection` false) | No extra API-server load, one Deployment, one RBAC role | UI traffic shares the operator's process |
| Field index on `spec.nodeName` | "Pods on node X" is a lookup, not a scan of every pod | Small extra memory in the cache |
| Plain HTML and JavaScript via `go:embed`, polling every 5 seconds | No build step; one binary; the assignment values clarity over polish | Polling instead of push; hidden tabs pause it |
| Events read on demand, through the manager's uncached API reader with a field selector for the clicked node or pod | Events are numerous and short-lived; watching every Event into the cache to show a few on a click would cost memory and watch traffic | One API-server request per click; needs only `list` on events |
| Operator Events only when a ClusterView's state flips, written through the `events.k8s.io` API (`GetEventRecorder`; the core-API recorder is deprecated in controller-runtime) | Shows why a view went Degraded and when it recovered, in `kubectl describe`, without an event per reconcile | Needs `create` and `patch` on `events.k8s.io` events |
| Least-privilege RBAC: read nodes, pods and ClusterViews, list Events, create and patch Events (events.k8s.io), update ClusterView status; Leases and Events for leader election | Only what the code uses; proven by the e2e test, which deploys under the operator's own service account | Re-enabling metrics needs two extra cluster-wide permissions |
| Metrics endpoint disabled | Nothing scrapes it; its auth needs cluster-wide TokenReview and SubjectAccessReview | No Prometheus metrics until re-enabled (one kustomize line) |
| Capacity versus allocatable, no live usage | Present on every Node; no extra component | Live CPU and memory usage would need metrics-server |
| Only known pressure conditions shown as problems | Providers add their own conditions (DigitalOcean's `CiliumAgentReady=True` is healthy) | A new vendor problem condition would show as neutral |
| Public LoadBalancer without authentication | Reviewers can reach it directly | Not suitable for production as-is |
| Multi-arch image, versioned tags, single `install.yaml` | Runs on amd64 and arm64; every cluster runs a known version; one-command install | Releases are manual steps |

In operator capability-level terms this covers Level 1 (install) and Level 2 (upgrades are a tag change with a rolling update), the resilience part of Level 3 (leader election, retries, conditions), and status conditions from Level 4. Level 5 (auto-healing) is out of scope on purpose: it would need write access to nodes and pods.

### Scaling and concurrency

- **One reconcile worker is enough.** controller-runtime defaults `MaxConcurrentReconciles` to 1. Work is keyed by ClusterView name, there are usually one or two, and the work queue never processes the same key on two workers at once. Traffic does not add keys: a burst of pod events collapses into one queued reconcile per ClusterView. More workers would only help with many ClusterViews and slow reconciles (`WithOptions(controller.Options{MaxConcurrentReconciles: N})`).
- **Where it would hit limits.** Every pod event triggers a full recount of the cached pods. That costs milliseconds here; at tens of thousands of pods with heavy churn I would rate-limit reconciles or keep counts incrementally. The HTTP API also lists every pod per `/api/nodes` request, once per open tab every 5 seconds; pushing updates or caching the computed response would cut that.
- **High availability.** `replicas: 2` works as is: leader election means only one replica reconciles, and every replica serves the UI (`NeedLeaderElection` returns false), so the Service keeps answering while one pod restarts.

### What I would do next

- Push live updates to the UI with server-sent events instead of polling: add an event handler to the manager's existing Node, Pod and ClusterView informers (no extra API-server load) that feeds a small broadcaster; each browser connection gets a one-slot channel so bursts coalesce and a slow client never blocks the informer; a `GET /api/stream` handler writes `event: changed` with periodic heartbeats (load balancers drop idle connections), and the page re-fetches the existing JSON endpoints. Streams must end when the operator stops, by deriving request contexts from the manager's context, or graceful shutdown waits on them. *Why not yet:* polling every 5 seconds reads the in-memory cache, so it is cheap and current enough at this scale, while SSE adds long-lived connections through the load balancer; Kubernetes Events added more diagnostic value first.
- Pod logs in the UI, behind a flag (needs `get` on `pods/log`). *Why not yet:* logs can contain secrets and the UI is public with no login, so this should only ship behind an off-by-default flag, with the permission in an optional RBAC file, once there is authentication in front of the UI.
- A `nodeSelector` in the spec to report on a subset of nodes.
- A `make release` target or a GitHub Action that builds, tags and regenerates the installer on a git tag.
- Re-enable metrics with a Prometheus ServiceMonitor, and put authentication in front of the UI.

## Local development

### Prerequisites

Go 1.26+, Docker, [kind](https://kind.sigs.k8s.io/), `kubectl` and `make`. The build tools (controller-gen, kustomize, setup-envtest, golangci-lint) are downloaded into `./bin` by `make` on first use.

### Run the operator against a local cluster

```bash
# A 3-node kind cluster, so the UI shows several nodes
cat <<'EOF' | kind create cluster --name clusterview --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes: [{role: control-plane}, {role: worker}, {role: worker}]
EOF
kubectl config current-context   # should print kind-clusterview

make install        # install the CRD
make run            # run the operator on your machine; UI at http://localhost:8090

# in a second terminal
make deploy-sample  # create the sample ClusterView
kubectl get clusterview
```

Logging defaults to debug when running locally, which prints a line per reconcile. Use `make run-info` for production-like output, `make run-debug` to force debug, or pass any flag with `make run RUN_ARGS="--zap-encoder=json"` (flags: `--zap-log-level`, `--zap-encoder`, `--zap-devel`).

`make run` uses your kubeconfig's current context and your own permissions, not the operator's RBAC. To test the real image and RBAC, use the end-to-end tests below or deploy with `make docker-build`, `kind load docker-image` and `make deploy`.

After changing the API types or the `+kubebuilder:rbac` markers, run `make generate manifests` and commit the generated files.

### Project layout

| Path | Contents |
| --- | --- |
| `api/v1alpha1/` | `ClusterView` Go types; the CRD and DeepCopy code are generated from them |
| `internal/summary/` | Pure function that turns nodes and pods into status counts |
| `internal/controller/` | The reconciler: watches, status updates, conditions |
| `internal/apiserver/` | HTTP API and the embedded web UI (`static/`) |
| `cmd/main.go` | Wires the manager, controller, pod index and HTTP server |
| `config/` | Kustomize manifests: CRD, RBAC, Deployment, UI Service, sample |
| `dist/install.yaml` | Generated single-file installer |
| `test/e2e/` | End-to-end tests against a kind cluster |

### Testing

| Command | Runs | Needs |
| --- | --- | --- |
| `make test` | Unit tests (`summary`, HTTP handlers with a fake client) and controller tests against a real API server (envtest, downloaded into `./bin`) | Nothing else |
| `make lint` | golangci-lint | Nothing else |
| `make verify` | `make test` and `make build`, then fails if generated files or `go.mod` differ from what is committed | A clean working tree |
| `make test-e2e` | Creates a kind cluster, builds and deploys the image, checks the operator runs and a ClusterView becomes Ready, then deletes the cluster | Docker and kind; switches your kubectl context |

CI runs `make verify`, `make lint` and `make test-e2e` on every push (`.github/workflows/`).

## Build and release

The image is `ghcr.io/arjunmalhotra1/clusterview-operator`. Each release gets a new tag; a cluster only changes when its Deployment points at a new tag, which also makes rollback a one-line change.

```bash
VERSION=v0.1.2
IMG=ghcr.io/arjunmalhotra1/clusterview-operator:$VERSION

# 1. Build for linux/amd64 and linux/arm64 and push to GHCR (needs `docker login ghcr.io`)
make docker-buildx IMG=$IMG

# 2. Regenerate the installer so its Deployment uses the new image
make build-installer IMG=$IMG

# 3. Publish the installer
git add dist/install.yaml config/manager/kustomization.yaml
git commit -m "chore(release): $VERSION"
git push

# 4. Roll it out: the same command as installing
kubectl apply -f https://raw.githubusercontent.com/arjunmalhotra1/Aranya/main/dist/install.yaml
kubectl -n clusterview-operator-system rollout status deploy/clusterview-operator-controller-manager
```

`kubectl apply` only changes resources that differ, so a code release updates just the Deployment's image and Kubernetes performs a rolling update. Changes that only touch YAML (RBAC, the Service) skip step 1 and reuse the current tag. To roll back, run steps 2 to 4 with the previous tag.

Check what is running:

```bash
kubectl -n clusterview-operator-system get deploy clusterview-operator-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
```

| Command | Use | Touches a cluster |
| --- | --- | --- |
| `make docker-build` | Build an image for your machine only (for kind) | No |
| `make docker-buildx` | Build and push a multi-arch release image | No |
| `make build-installer` | Regenerate `dist/install.yaml` | No |
| `make deploy` / `make undeploy` | Developer shortcut: apply or remove the kustomize build on the current context | Yes |
| `make deploy-sample` / `make undeploy-sample` | Create or delete the sample ClusterView | Yes |

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
