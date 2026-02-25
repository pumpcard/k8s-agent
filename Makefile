.PHONY: build push deploy upgrade uninstall clean help package-chart publish-chart

# Configuration
IMAGE_NAME ?= k8s-agent
IMAGE_TAG ?= latest
REGISTRY ?= docker.io
FULL_IMAGE ?= $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
HELM_RELEASE_NAME ?= k8s-agent
HELM_CHART_PATH ?= ./charts/k8s-agent
HELM_REPO_URL ?= https://pumpcard.github.io/k8s-agent
CHART_VERSION ?= $(shell grep '^version:' $(HELM_CHART_PATH)/Chart.yaml | awk '{print $$2}')

# Build the Docker image
build:
	@echo "Building Docker image $(FULL_IMAGE)..."
	docker build -t $(FULL_IMAGE) .
	@echo "Build complete: $(FULL_IMAGE)"

# Push the Docker image to registry
push: build
	@echo "Pushing Docker image $(FULL_IMAGE)..."
	docker push $(FULL_IMAGE)
	@echo "Push complete: $(FULL_IMAGE)"

# Deploy to Kubernetes cluster using Helm
deploy:
	@echo "Deploying $(HELM_RELEASE_NAME) to cluster..."
	helm install $(HELM_RELEASE_NAME) $(HELM_CHART_PATH)
	@echo "Deployment complete!"

# Upgrade deployment
upgrade:
	@echo "Upgrading $(HELM_RELEASE_NAME)..."
	helm upgrade $(HELM_RELEASE_NAME) $(HELM_CHART_PATH)
	@echo "Upgrade complete!"

# Uninstall deployment
uninstall:
	@echo "Uninstalling $(HELM_RELEASE_NAME)..."
	helm uninstall $(HELM_RELEASE_NAME)
	@echo "Uninstall complete!"

# View logs
logs:
	kubectl logs -f deployment/$(HELM_RELEASE_NAME) -n kube-system

# Check status
status:
	kubectl get pods -n kube-system -l app=$(HELM_RELEASE_NAME)
	kubectl get deployment $(HELM_RELEASE_NAME) -n kube-system

# Clean (alias for uninstall)
clean: uninstall

# Package Helm chart
package-chart:
	@echo "Packaging Helm chart..."
	helm package $(HELM_CHART_PATH)
	@echo "Chart packaged: k8s-agent-$(CHART_VERSION).tgz"

# Publish chart to GitHub Pages
# Requires: gh-pages branch and GitHub Pages enabled
publish-chart: package-chart
	@echo "Publishing chart to GitHub Pages..."
	@if [ ! -d "gh-pages" ]; then \
		git clone -b gh-pages $(shell git config --get remote.origin.url) gh-pages || \
		(mkdir -p gh-pages && cd gh-pages && git init && git checkout -b gh-pages); \
	fi
	helm repo index gh-pages --url $(HELM_REPO_URL) --merge gh-pages/index.yaml || \
	helm repo index gh-pages --url $(HELM_REPO_URL)
	cp k8s-agent-$(CHART_VERSION).tgz gh-pages/
	cd gh-pages && \
		git add . && \
		git commit -m "Add chart version $(CHART_VERSION)" && \
		git push origin gh-pages
	@echo "Chart published to $(HELM_REPO_URL)"
	@echo "Users can now install with:"
	@echo "  helm repo add your-repo $(HELM_REPO_URL)"
	@echo "  helm install k8s-agent your-repo/k8s-agent"

# Help
help:
	@echo "Available targets:"
	@echo "  build        - Build the Docker image"
	@echo "  push         - Build and push the Docker image to registry"
	@echo "  deploy       - Deploy to Kubernetes cluster using Helm"
	@echo "  upgrade      - Upgrade existing Helm deployment"
	@echo "  uninstall    - Remove Helm deployment from cluster"
	@echo "  clean        - Alias for uninstall"
	@echo "  logs         - View agent logs"
	@echo "  status       - Check deployment status"
	@echo ""
	@echo "Configuration:"
	@echo "  IMAGE_NAME        - Docker image name (default: k8s-agent)"
	@echo "  IMAGE_TAG         - Docker image tag (default: latest)"
	@echo "  REGISTRY          - Container registry (default: docker.io)"
	@echo "  HELM_RELEASE_NAME - Helm release name (default: k8s-agent)"
	@echo ""
	@echo "Examples:"
	@echo "  make build IMAGE_NAME=pump/k8s-agent IMAGE_TAG=v1.0.0"
	@echo "  make deploy"
	@echo "  make upgrade"
