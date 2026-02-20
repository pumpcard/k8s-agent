# Kubernetes Cluster Metrics Agent

A lightweight Kubernetes agent that collects cluster-level metrics from any Kubernetes cluster.

## Prerequisites

- EKS cluster (or any Kubernetes cluster)
- `kubectl` configured to access your cluster
- `helm` installed (v3.x)

## Quick Start

### Install from Helm Repository (One-liner)

When your backend requires a valid JWT, pass your `client_id` and `client_secret` from Auth0. The agent fetches the Bearer token automatically (domain and audience are built-in):

```bash
helm upgrade --install k8s-agent-test ./charts/k8s-agent-test \
  --namespace kube-system --create-namespace \
  --set defaultComponents.enabled=true \
  --set auth0.clientId="YOUR_CLIENT_ID" \
  --set auth0.clientSecret="YOUR_CLIENT_SECRET"
```

This installs both:
- **Cluster-level agent** (Deployment) - collects cluster-wide metrics from the Kubernetes API
- **Node-level agent** (DaemonSet) - one pod per node for node-specific metrics and host-level data

### Install from Local Chart

```bash
helm install k8s-agent-test ./charts/k8s-agent-test
```

### Verify Deployment

```bash
# Check pod status (both cluster and node components)
kubectl get pods -n kube-system -l app=k8s-agent-test

# View cluster-level agent logs
kubectl logs -f deployment/k8s-agent-test-cluster -n kube-system

# View node-level agent logs (from any node)
kubectl logs -f daemonset/k8s-agent-test-node -n kube-system
```

## Configuration

You can customize the deployment by overriding values:

```bash
helm install k8s-agent-test ./charts/k8s-agent-test \
  --set image.tag=v1.0.1 \
  --set components.cluster.replicaCount=2 \
  --set defaultComponents.enabled=true
```

To disable specific components:
```bash
# Only cluster-level collection
helm install k8s-agent-test ./charts/k8s-agent-test \
  --set defaultComponents.enabled=true \
  --set components.node.enabled=false

# Only node-level collection
helm install k8s-agent-test ./charts/k8s-agent-test \
  --set defaultComponents.enabled=true \
  --set components.cluster.enabled=false
```

Or create a custom `values.yaml` file:

```bash
helm install k8s-agent-test ./charts/k8s-agent-test -f my-values.yaml
```

## Upgrading

```bash
helm upgrade k8s-agent-test ./charts/k8s-agent-test
```

Or with new values:

```bash
helm upgrade k8s-agent-test ./charts/k8s-agent-test \
  --set image.tag=v1.0.1
```

## Uninstalling

```bash
helm uninstall k8s-agent-test
```

## Troubleshooting

### Pod not starting

```bash
# Check pod events
kubectl describe pod -n kube-system -l app=k8s-agent-test

# Check RBAC permissions
kubectl auth can-i list nodes --as=system:serviceaccount:kube-system:k8s-agent-test
```

### Image pull errors

Ensure:
- Image exists in the registry and is publicly accessible
- Image reference in `values.yaml` is correct
- Cluster has network access to pull from the registry
