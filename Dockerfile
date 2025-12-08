# Optimized multi-stage Dockerfile for Kubernetes Ping Exporter
# Uses distroless base images for maximum security and minimal image size

# Declare ARGs before any FROM statements
ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG GO_VERSION
ARG VERSION="dev"
ARG COMMIT_SHA="unknown"
ARG BUILD_DATE="unknown"

# Build stage - Use the official Go image for building
FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:${GO_VERSION:-1.24.0}-alpine AS builder

WORKDIR /src

# Install dependencies, copy files, and show version info in one layer
RUN apk add --no-cache ca-certificates git gcc musl-dev
COPY go.mod go.sum ./
RUN CURRENT_GO=$(go version | cut -d' ' -f3 | sed 's/go//') && \
  DETECTED_GO=$(grep "^go " go.mod | cut -d' ' -f2) && \
  echo "Building with Go $CURRENT_GO" && \
  echo "Project requires Go $DETECTED_GO" && \
  if [ -n "$GO_VERSION" ]; then \
  echo "Using CI-provided Go version: $GO_VERSION"; \
  else \
  echo "For exact version match: docker build --build-arg GO_VERSION=$DETECTED_GO ."; \
  fi && \
  go mod download && go mod verify

# Copy source and build in one layer
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  go build \
  -ldflags="-w -s -X main.version=${VERSION} -X main.commitSHA=${COMMIT_SHA} -X main.buildDate=${BUILD_DATE}" \
  -a -installsuffix cgo \
  -o /app/kubernetes_ping_exporter && \
  echo "Binary built successfully"

# Runtime stage - Use Google's distroless image for maximum security
FROM gcr.io/distroless/static-debian12:nonroot AS runner

# Copy ca-certificates and application binary in one layer
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/kubernetes_ping_exporter /app/kubernetes_ping_exporter

USER 65532:65532
ENV METRICS_PORT=9107 CHECK_INTERVAL_SECONDS=15
EXPOSE ${METRICS_PORT}

# Add labels for better maintainability
LABEL org.opencontainers.image.title="Kubernetes Ping Exporter" \
  org.opencontainers.image.description="Prometheus exporter for Kubernetes endpoint ping monitoring" \
  org.opencontainers.image.version="${VERSION}" \
  org.opencontainers.image.created="${BUILD_DATE}" \
  org.opencontainers.image.revision="${COMMIT_SHA}" \
  org.opencontainers.image.vendor="maclucky" \
  org.opencontainers.image.licenses="MIT" \
  org.opencontainers.image.source="https://github.com/mac-lucky/kubernetes-ping-exporter"

# Use ENTRYPOINT for better signal handling
ENTRYPOINT ["/app/kubernetes_ping_exporter"]

# Development stage - includes shell and additional tools for debugging
FROM alpine:3.23 AS development

# Install tools, copy binary, create user in one layer
RUN apk add --no-cache ca-certificates curl wget iputils && \
  addgroup -g 65532 -S nonroot && \
  adduser -u 65532 -S nonroot -G nonroot
COPY --from=builder /app/kubernetes_ping_exporter /app/kubernetes_ping_exporter

USER 65532:65532
ENV METRICS_PORT=9107 CHECK_INTERVAL_SECONDS=15
EXPOSE ${METRICS_PORT}
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost:${METRICS_PORT}/metrics || exit 1
ENTRYPOINT ["/app/kubernetes_ping_exporter"]