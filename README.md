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
- **HTTP API and web UI**, served by the operator itself on port 8090 and exposed through a LoadBalancer Service:
  - a card per node with readiness, conditions, CPU and memory allocatable versus capacity, and pod count;
  - click a node to list its pods with phase, ready containers and restart count;
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

Changes take effect on the next reconcile, within milliseconds. When `status.observedGeneration` equals `metadata.generation`, the status reflects your latest edit.

### HTTP API

| Endpoint | Returns |
| --- | --- |
| `GET /api/clusterviews` | Every ClusterView with its namespace filter and status summary |
| `GET /api/nodes` | Nodes with readiness, conditions, CPU and memory capacity and allocatable, and pod count |
| `GET /api/nodes/{name}/pods` | Pods on one node with phase, restarts and ready containers |

Add `?view=<clusterview-name>` to either node endpoint to count only pods in that ClusterView's namespaces. An unknown node or view returns 404.

### Uninstall

```bash
kubectl delete -f https://raw.githubusercontent.com/arjunmalhotra1/Aranya/main/config/samples/observability_v1alpha1_clusterview.yaml
kubectl delete -f https://raw.githubusercontent.com/arjunmalhotra1/Aranya/main/dist/install.yaml
```

Deleting the UI Service also removes the cloud load balancer.

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
