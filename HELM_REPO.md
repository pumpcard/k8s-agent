# Publishing Helm Chart to Repository

This guide explains how to publish the Helm chart to a repository so users can install it with a one-liner.

## Option 1: GitHub Pages (Recommended - Free)

### Setup

1. **Create a `gh-pages` branch:**
   ```bash
   git checkout --orphan gh-pages
   git rm -rf .
   git commit --allow-empty -m "Initial commit"
   git push origin gh-pages
   ```

2. **Enable GitHub Pages:**
   - Go to repository Settings → Pages
   - Source: Deploy from a branch
   - Branch: `gh-pages` / `/ (root)`
   - Save

3. **Package and publish the chart:**
   ```bash
   make package-chart
   make publish-chart
   ```

### Repository URL

Once published, your Helm repository will be available at:
```
https://pumpcard.github.io/k8s-agent
```

### Automated Publishing (Recommended)

This repository includes a GitHub Actions workflow (`.github/workflows/publish-chart.yml`) that automatically publishes the chart to GitHub Pages whenever you push changes to the `charts/` directory.

**Initial Setup (One-time):**
1. Push your code to the `main` or `master` branch
2. Enable GitHub Pages:
   - Go to repository Settings → Pages
   - Source: Deploy from a branch
   - Branch: `gh-pages` / `/ (root)`
   - Save
3. The workflow will automatically create the `gh-pages` branch on first run

**After setup, charts are automatically published when you:**
- Push changes to `charts/` directory
- Manually trigger the workflow from Actions tab

## Option 2: OCI Registry (Docker Hub, GHCR, etc.)

You can also publish Helm charts to OCI registries:

```bash
# Login to registry
helm registry login docker.io

# Package and push
helm package charts/k8s-agent-test
helm push k8s-agent-test-1.0.0.tgz oci://docker.io/yourusername/charts
```

Then install with:
```bash
helm install k8s-agent-test oci://docker.io/yourusername/charts/k8s-agent-test --version 1.0.0
```

## Option 3: ChartMuseum

For self-hosted solutions, you can use ChartMuseum or other Helm repository servers.

## After Publishing

Users can then install with:

```bash
helm repo add k8s-agent https://pumpcard.github.io/k8s-agent
helm repo update
helm install k8s-agent-test k8s-agent/k8s-agent-test
```

Or as a one-liner:
```bash
helm repo add k8s-agent https://pumpcard.github.io/k8s-agent && \
helm repo update && \
helm install k8s-agent-test k8s-agent/k8s-agent-test
```
