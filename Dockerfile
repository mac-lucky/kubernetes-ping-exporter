# syntax=docker/dockerfile:1.4

# Platform arguments with default values
ARG TARGETARCH=amd64
ARG TARGETPLATFORM=linux/amd64

# Build stage - will automatically use the correct platform
FROM --platform=$BUILDPLATFORM golang:alpine AS builder

WORKDIR /src

# Copy only go.mod and go.sum first for better layer caching
COPY go.* ./
RUN go mod download

# Copy source code
COPY . .

# Make the build process more explicit for multi-arch
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags="-w -s" -o /app/kubernetes_ping_exporter

# Final stage - will use the target platform
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