FROM --platform=$BUILDPLATFORM golang:alpine AS builder

# Add platform arguments
ARG TARGETARCH
ARG BUILDPLATFORM

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.* ./
RUN go mod download

# Copy source code
COPY . .

# Build the application with architecture-specific flags
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o kubernetes_ping_exporter

# Final stage
FROM --platform=$TARGETPLATFORM alpine

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/kubernetes_ping_exporter .

# Set default metrics port and check interval
ENV METRICS_PORT=2112
ENV CHECK_INTERVAL_SECONDS=30

# Expose prometheus metrics port
EXPOSE ${METRICS_PORT}

# Run the application with correct config path
CMD ["/app/kubernetes_ping_exporter"]
