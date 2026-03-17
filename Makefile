## kube-janitor Makefile
## Targets follow standard OSS conventions.
##
## Usage:
##   make build          - Build the controller binary
##   make test           - Run all unit tests with race detector
##   make lint           - Run golangci-lint
##   make generate       - Run controller-gen (DeepCopy + RBAC manifests)
##   make manifests      - Generate CRD manifests
##   make docker-build   - Build container image for current platform
##   make docker-buildx  - Build multi-arch image (linux/amd64, linux/arm64)
##   make install        - Install CRDs into current cluster
##   make run            - Run manager locally against current cluster

# Project metadata
PROJECT_NAME    := kube-janitor
MODULE          := github.com/kaiohenricunha/kube-janitor
IMAGE_REGISTRY  ?= ghcr.io/kaiohenricunha
IMAGE_TAG       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
IMAGE           := $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)

# Tool versions — pin for reproducibility
CONTROLLER_GEN_VERSION := v0.16.3
GOLANGCI_LINT_VERSION  := v1.61.0
ENVTEST_VERSION        := release-0.19

# Directories
BIN_DIR         := bin
TOOLS_DIR       := $(BIN_DIR)/tools

# Go flags
GOFLAGS         ?=
LDFLAGS         := -ldflags "-X main.version=$(IMAGE_TAG) -w -s"
RACE            := -race

# OS/Arch detection
OS              := $(shell go env GOOS)
ARCH            := $(shell go env GOARCH)

.PHONY: all build test test-unit test-integration lint fmt vet generate manifests \
        install uninstall run docker-build docker-buildx tools clean help

all: lint test build

## Build

build: ## Build the controller binary for the current platform
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/manager ./cmd/manager/...

build-linux-amd64: ## Cross-compile for linux/amd64
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) $(LDFLAGS) \
		-o $(BIN_DIR)/manager-linux-amd64 ./cmd/manager/...

build-linux-arm64: ## Cross-compile for linux/arm64
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(GOFLAGS) $(LDFLAGS) \
		-o $(BIN_DIR)/manager-linux-arm64 ./cmd/manager/...

## Test

test: test-unit ## Run all tests

test-unit: ## Run unit tests with race detector
	go test $(RACE) -count=1 -timeout=120s ./...

test-integration: ## Run integration tests using envtest
	KUBEBUILDER_ASSETS="$(shell $(TOOLS_DIR)/setup-envtest use $(ENVTEST_VERSION) -p path)" \
		go test $(RACE) -count=1 -timeout=300s -tags=integration ./...

test-single: ## Run a single test. Usage: make test-single PKG=./internal/classifier/... TEST=TestClassifier_ProtectedByAnnotation
	go test $(RACE) -count=1 -timeout=60s -run $(TEST) $(PKG)

coverage: ## Run tests with coverage report
	go test $(RACE) -count=1 -timeout=120s -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## Code quality

lint: $(TOOLS_DIR)/golangci-lint ## Run golangci-lint
	$(TOOLS_DIR)/golangci-lint run ./...

fmt: ## Format all Go source files
	go fmt ./...
	goimports -w .

vet: ## Run go vet
	go vet ./...

vulncheck: ## Run govulncheck for vulnerability scanning
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## Code generation

generate: $(TOOLS_DIR)/controller-gen ## Run controller-gen for DeepCopy and object methods
	$(TOOLS_DIR)/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

manifests: $(TOOLS_DIR)/controller-gen ## Generate CRD YAML manifests
	$(TOOLS_DIR)/controller-gen rbac:roleName=kube-janitor-manager-role \
		crd \
		webhook \
		paths="./..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac

## Cluster operations

install: manifests ## Install CRDs into the current cluster
	kubectl apply -f config/crd/bases/

uninstall: ## Remove CRDs from the current cluster
	kubectl delete -f config/crd/bases/ --ignore-not-found

run: manifests ## Run manager locally against the current cluster (--dry-run by default)
	go run ./cmd/manager/... \
		--dry-run=true \
		--development=true \
		--scan-interval=30s

## Container images

docker-build: ## Build container image for current platform
	docker build \
		--build-arg VERSION=$(IMAGE_TAG) \
		-t $(IMAGE) \
		-f Dockerfile .

docker-buildx: ## Build and push multi-arch container image
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(IMAGE_TAG) \
		-t $(IMAGE) \
		-f Dockerfile \
		--push .

## Helm

helm-lint: ## Lint the Helm chart
	helm lint charts/$(PROJECT_NAME)

helm-package: ## Package the Helm chart
	helm package charts/$(PROJECT_NAME) -d dist/

helm-template: ## Render Helm chart templates for review
	helm template kube-janitor charts/$(PROJECT_NAME) --debug

## Tools installation

tools: $(TOOLS_DIR)/controller-gen $(TOOLS_DIR)/golangci-lint $(TOOLS_DIR)/setup-envtest ## Install all tools

$(TOOLS_DIR)/controller-gen:
	@mkdir -p $(TOOLS_DIR)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

$(TOOLS_DIR)/golangci-lint:
	@mkdir -p $(TOOLS_DIR)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
		| sh -s -- -b $(PWD)/$(TOOLS_DIR) $(GOLANGCI_LINT_VERSION)

$(TOOLS_DIR)/setup-envtest:
	@mkdir -p $(TOOLS_DIR)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

## Utility

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out coverage.html dist/

tidy: ## Run go mod tidy
	go mod tidy

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-25s\033[0m %s\n", $$1, $$2}'
