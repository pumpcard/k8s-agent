# AGENTS.md

## Project Overview

Kubernetes cluster metrics agent (k8s-agent) — a lightweight Go service that collects cluster-level metrics from Kubernetes (EKS, GKE, AKS) and exports them to the Pump ingestion API. Deployed via Helm as a Deployment (cluster-wide) and optionally a DaemonSet (per-node).

## Tech Stack

- **Language:** Go (module `k8s-agent`, `go 1.24`)
- **Dependencies:** `k8s.io/client-go`, `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/metrics`
- **Build:** `make build` (Docker), `make push`, `make deploy` (Helm)
- **Container:** Multi-arch Docker image (amd64/arm64), Alpine-based, non-root user
- **Deployment:** Helm chart in `charts/k8s-agent/`
- **CI/CD:** GitHub Actions — `dev.yml` (push to main), `prod.yml` (push tag `v*`)
- **Auth:** Auth0 machine-to-machine JWT (client credentials flow)

## Repository Structure

```
cmd/k8s-agent/main.go          # Entrypoint: configures K8s clients, runs collection loop
internal/
  auth/auth0.go                 # Auth0 JWT token provider (cached, thread-safe)
  cloud/
    provider.go                 # Provider interface and registry (Parse, AccountID, ProjectID)
    labels.go                   # Cloud-agnostic node label extraction
    aws/provider.go             # AWS providerID parser
    aws/metadata.go             # EC2 IMDS account ID fallback
    azure/provider.go           # Azure providerID parser
    gcp/provider.go             # GCP providerID parser
  clusterid/clusterid.go        # Cluster ID from kube-system namespace UID
  collector/
    collector.go                # Main metrics collection (nodes, pods, usage)
    karpenter.go                # Karpenter scaling event collection
  export/export.go              # Metrics export to Pump API
  pump/client.go                # HTTP client for Pump ingestion endpoint
charts/k8s-agent/               # Helm chart (Deployment, DaemonSet, RBAC, ServiceAccount)
```

## Build & Run

```bash
# Build Docker image
make build

# Build Go binaries directly (CI does this for multi-arch)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o k8s-agent-amd64 ./cmd/k8s-agent

# Deploy via Helm
make deploy

# View logs / check status
make logs
make status
```

## Key Environment Variables

| Variable | Purpose |
|---|---|
| `METRICS_EXPORT_ENDPOINT` | Pump API URL (also enables export when set) |
| `METRICS_EXPORT_ENABLED` | Set to `false` or `0` to disable export |
| `METRICS_EXPORT_TIMEOUT_SECONDS` | HTTP client timeout (default: 90) |
| `AUTH0_CLIENT_ID` | Auth0 client ID for JWT |
| `AUTH0_CLIENT_SECRET` | Auth0 client secret for JWT |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` (default: `info`) |
| `KUBECONFIG` | Path to kubeconfig (fallback when not in-cluster) |

## Code Conventions

- **Package naming:** lowercase, single-word (`collector`, `export`, `pump`, `auth`, `cloud`)
- **Exported types:** PascalCase (`ClusterMetricsPayload`, `NodeMetrics`, `PodSummary`)
- **Unexported helpers:** camelCase (`nodeCloudInfo`, `quantityToMilli`, `capPercent`)
- **JSON struct tags:** snake_case (`cluster_id`, `cpu_millicores`, `memory_bytes`)
- **Logging:** `log/slog` with `slog.NewJSONHandler` — structured key-value pairs, no `fmt.Printf`
- **Config pattern:** `ConfigFromEnv()` reads environment variables, returns a config struct
- **Cloud providers:** register via `cloud.Register()` in `init()` functions, implement the `cloud.Provider` interface
- **Context:** pass `context.Context` through collection and export call chains
- **Error handling:** return errors up the stack; log at the call site with `slog`. Use `fmt.Errorf("context: %w", err)` for wrapping.
- **Kubernetes quantities:** use `resource.Quantity` helpers (`MilliValue()`, `Value()`, `String()`); never hand-parse resource strings

## Architecture Notes

- The main loop in `cmd/k8s-agent/main.go` runs every 60 seconds: collect metrics, collect Karpenter events, export to Pump.
- Cloud provider detection is automatic via `node.Spec.ProviderID` prefix matching (`aws://`, `gcp://`, `azure://`).
- AWS account ID has a special fallback path through EC2 IMDS when providerID doesn't contain it.
- System namespaces (`kube-system`, `kube-public`, GKE/EKS/AKS internal namespaces) are excluded from pod collection.
- Karpenter events use `resourceVersion` tracking to avoid re-processing events across cycles.
- The `export` package resolves cluster and account IDs and skips export if either is empty.

## Testing

No test files exist yet. When adding tests, use standard Go conventions:

```bash
go test ./...
```

## Linting

No linter configuration exists. Use standard Go tooling:

```bash
go vet ./...
go fmt ./...
```
