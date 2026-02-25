# Install from Helm Repository

Add the Helm repo and install the k8s-agent chart. Auth0 credentials are required for metrics export.

```bash
helm repo add k8s-agent https://pumpcard.github.io/k8s-agent
helm repo update
helm install k8s-agent k8s-agent/k8s-agent --namespace kube-system --create-namespace \
  --set auth0.clientId="YOUR_CLIENT_ID" \
  --set auth0.clientSecret="YOUR_CLIENT_SECRET"
```

One-liner:

```bash
helm repo add k8s-agent https://pumpcard.github.io/k8s-agent && \
helm repo update && \
helm install k8s-agent k8s-agent/k8s-agent --namespace kube-system --create-namespace \
  --set auth0.clientId="YOUR_CLIENT_ID" \
  --set auth0.clientSecret="YOUR_CLIENT_SECRET"
```

See the [main README](README.md) for more options and upgrade/uninstall.
