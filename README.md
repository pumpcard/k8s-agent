# Kubernetes Cluster Metrics Agent

A lightweight Kubernetes agent that collects cluster-level metrics from any Kubernetes cluster.

## Prerequisites

- Kubernetes cluster (EKS, GKE, AKS, or any compatible cluster)
- `kubectl` configured to access your cluster
- `helm` installed (v3.x)

## Quick Start

### Install from Helm Repository (One-liner)

When your backend requires a valid JWT, pass your `client_id` and `client_secret` from Auth0. The agent fetches the Bearer token automatically (domain and audience are built-in):

```bash
helm upgrade --install k8s-agent ./charts/k8s-agent \
  --namespace k8s-agent --create-namespace \
  --set defaultComponents.enabled=true \
  --set auth0.clientId="YOUR_CLIENT_ID" \
  --set auth0.clientSecret="YOUR_CLIENT_SECRET"
```

This installs both:
- **Cluster-level agent** (Deployment) - collects cluster-wide metrics from the Kubernetes API
- **Node-level agent** (DaemonSet) - one pod per node for node-specific metrics and host-level data


### Verify Deployment

Use the same namespace you passed to `helm install --namespace` (e.g. `k8s-agent`):

```bash
# Check pod status (both cluster and node components)
kubectl get pods -n k8s-agent -l app=k8s-agent

# View cluster-level agent logs
kubectl logs -f deployment/k8s-agent-cluster -n k8s-agent

# View node-level agent logs (from any node)
kubectl logs -f daemonset/k8s-agent-node -n k8s-agent
```

## Configuration

You can customize the deployment by overriding values:

```bash
helm install k8s-agent ./charts/k8s-agent --namespace k8s-agent --create-namespace \
  --set image.tag=v1.0.1 \
  --set components.cluster.replicaCount=2 \
  --set defaultComponents.enabled=true
```

To disable specific components:
```bash
# Only cluster-level collection
helm install k8s-agent ./charts/k8s-agent --namespace k8s-agent --create-namespace \
  --set defaultComponents.enabled=true \
  --set components.node.enabled=false

# Only node-level collection
helm install k8s-agent ./charts/k8s-agent --namespace k8s-agent --create-namespace \
  --set defaultComponents.enabled=true \
  --set components.cluster.enabled=false
```

Or create a custom `values.yaml` file:

```bash
helm install k8s-agent ./charts/k8s-agent --namespace k8s-agent --create-namespace -f my-values.yaml
```

## Upgrading

Use the same namespace you used for install:

```bash
helm upgrade k8s-agent ./charts/k8s-agent --namespace k8s-agent
```

Or with new values:

```bash
helm upgrade k8s-agent ./charts/k8s-agent --namespace k8s-agent \
  --set image.tag=v1.0.1
```

## Uninstalling

```bash
helm uninstall k8s-agent --namespace k8s-agent
```

## Deploying on GKE

The same chart works on GKE. Set `image.repository` to your GCR or Artifact Registry URL (e.g. `gcr.io/my-project/k8s-agent`). Use Auth0 for ingestion API auth as in Quick Start (`auth0.clientId`, `auth0.clientSecret`), and set `metricsExport.customerId` as needed (cluster ID is derived from the API).

## Troubleshooting

### Pod not starting

Use the namespace where you installed the agent (e.g. `k8s-agent`):

```bash
# Check pod events
kubectl describe pod -n k8s-agent -l app=k8s-agent

# Check RBAC permissions
kubectl auth can-i list nodes --as=system:serviceaccount:k8s-agent:k8s-agent
```

### Image pull errors

Ensure:
- Image exists in the registry and is publicly accessible
- Image reference in `values.yaml` is correct (default: `public.ecr.aws/b6q7g2c1/pump`)
- Cluster has network access to pull from the registry
