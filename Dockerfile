# syntax=docker/dockerfile:1

# Build stage. The tag follows the newest Go 1.26 patch (release.yml and the Makefile build with
# --pull), so standard-library fixes reach the image without a version edit. Official golang
# images set GOTOOLCHAIN=local, which ignores go.mod's toolchain line; auto makes that line a
# real floor, so a stale cached tag fetches the minimum patched toolchain instead of using it.
FROM golang:1.26-alpine AS builder
ENV GOTOOLCHAIN=auto
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /workspace

# Download dependencies first (layer cache friendly).
COPY go.mod go.sum ./
RUN go mod download

# Build the manager binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
    -ldflags "-X main.version=${VERSION} -w -s" \
    -o manager \
    ./cmd/manager/...

# Final stage: distroless/static for minimal attack surface.
# No shell, no package manager, non-root.
FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.source="https://github.com/kaiohenricunha/kube-janitor"
LABEL org.opencontainers.image.description="kube-janitor: Kubernetes resource cleanup controller"
LABEL org.opencontainers.image.licenses="Apache-2.0"

WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
