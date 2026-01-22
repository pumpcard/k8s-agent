# Kubernetes Cluster Metrics Agent

A lightweight Kubernetes agent that collects cluster-level metrics from any Kubernetes cluster.

## Prerequisites

- EKS cluster (or any Kubernetes cluster)
- `kubectl` configured to access your cluster
- `helm` installed (v3.x)

## Quick Start

### Install from Helm Repository (One-liner)

```bash
helm repo add k8s-agent https://pumpcard.github.io/k8s-agent && \
helm repo update && \
helm upgrade --install k8s-agent-test \
    --namespace kube-system --create-namespace \
    k8s-agent/k8s-agent-test
```

### Install from Local Chart

```bash
helm install k8s-agent-test ./charts/k8s-agent-test
```

### Verify Deployment

```bash
# Check pod status
kubectl get pods -n kube-system -l app=k8s-agent-test

# View logs
kubectl logs -f deployment/k8s-agent-test -n kube-system
```

## Configuration

You can customize the deployment by overriding values:

```bash
helm install k8s-agent-test ./charts/k8s-agent-test \
  --set image.tag=v1.0.1 \
  --set replicaCount=2
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
