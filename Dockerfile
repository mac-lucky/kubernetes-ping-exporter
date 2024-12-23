# Syntax version for better caching and features
# syntax=docker/dockerfile:1.4

# Add platform arguments
ARG TARGETARCH
ARG BUILDPLATFORM

# Build stage
FROM --platform=$BUILDPLATFORM golang:alpine AS builder

# Set working directory
WORKDIR /src

# Copy only go.mod and go.sum first for better layer caching
COPY go.* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy source code
COPY . .

# Build the application with optimizations
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=$TARGETARCH \
    go build -o /app/kubernetes_ping_exporter

# Final stage
FROM --platform=$TARGETPLATFORM alpine

WORKDIR /app

# Copy binary from builder (without user ownership)
COPY --from=builder /app/kubernetes_ping_exporter .

# Set environment variables
ENV METRICS_PORT=9107 \
    CHECK_INTERVAL_SECONDS=15

# Expose prometheus metrics port
EXPOSE ${METRICS_PORT}

# Add healthcheck
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:${METRICS_PORT}/metrics || exit 1

# Run the application
ENTRYPOINT ["/app/kubernetes_ping_exporter"]