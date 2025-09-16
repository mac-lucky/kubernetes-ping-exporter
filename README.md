# Kubernetes Ping Exporter

[![Docker Pulls](https://img.shields.io/docker/pulls/maclucky/kubernetes-ping-exporter)](https://hub.docker.com/r/maclucky/kubernetes-ping-exporter)
[![Docker Image Version](https://img.shields.io/docker/v/maclucky/kubernetes-ping-exporter/latest)](https://hub.docker.com/r/maclucky/kubernetes-ping-exporter/tags)
[![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/mac-lucky/kubernetes-ping-exporter/ci-cd.yml)](https://github.com/mac-lucky/kubernetes-ping-exporter/actions)

A Prometheus exporter that measures network latency between Kubernetes pods and external targets.

## Features

- Measures network latency between pods with `app=ping-exporter` label
- Supports additional external IP targets via ConfigMap
- Provides comprehensive ping metrics including RTT, packet loss, and reachability
- Auto-discovery of ping-exporter pods within the same namespace

## Metrics

- `ping_rtt_best_seconds`: Best round trip time
- `ping_rtt_worst_seconds`: Worst round trip time
- `ping_rtt_mean_seconds`: Mean round trip time
- `ping_rtt_std_deviation_seconds`: Standard deviation of RTT
- `ping_loss_ratio`: Packet loss ratio (0-1)
- `ping_up`: Target reachability status (1=up, 0=down)

## Requirements

- Kubernetes cluster with RBAC enabled
- Helm 3.x (for installation)

## Installation

Using Helm:
```bash
helm install ping-exporter ./infra-ping-exporter -n monitoring
```

## Configuration

### Environment Variables

- `POD_IP`: IP address of the current pod (required)
- `NODE_NAME`: Name of the node running the pod (required)
- `MY_POD_NAME`: Name of the current pod (required)
- `MY_POD_NAMESPACE`: Namespace where the pod is running (required)
- `CONFIG_MAP_NAME`: Name of the ConfigMap containing additional IPs (default: ping-exporter-config)

### ConfigMap

Create a ConfigMap to specify additional IP targets:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: ping-exporter-config
data:
  additional_ips: "8.8.8.8,1.1.1.1"
```

## Metrics Collection

The exporter runs on port 9107 and metrics are available at `/metrics`. Ping measurements are performed every 15 seconds.

## Labels

All metrics include the following labels:
- `source`: Source pod IP
- `destination`: Target IP
- `source_nodename`: Source node name
- `dest_nodename`: Target node name
- `source_podname`: Source pod name

## Building

### Local Build

To build the Docker image:
```bash
# Quick build (uses default Go version)
docker build -t kubernetes-ping-exporter .

# Exact build (uses Go version from go.mod - recommended)
docker build --build-arg GO_VERSION=$(grep "^go " go.mod | cut -d' ' -f2) -t kubernetes-ping-exporter .

# Development build (includes debugging tools)
docker build --target development -t kubernetes-ping-exporter:dev .
```

### Pre-built Images

Pre-built images are available on Docker Hub and GitHub Container Registry:
```bash
# Docker Hub
docker pull maclucky/kubernetes-ping-exporter:latest

# GitHub Container Registry
docker pull ghcr.io/mac-lucky/kubernetes-ping-exporter:latest
```

### Multi-platform Build
```bash
# Build for multiple architectures
docker buildx build --platform linux/amd64,linux/arm64 -t kubernetes-ping-exporter .
```

## Development

### Quick Development Workflow
```bash
# 1. Build locally
docker build --build-arg GO_VERSION=$(grep "^go " go.mod | cut -d' ' -f2) -t kubernetes-ping-exporter .

# 2. Test run (without Kubernetes)
docker run -d -p 9107:9107 --name ping-test kubernetes-ping-exporter

# 3. Check metrics
curl http://localhost:9107/metrics

# 4. View logs
docker logs ping-test

# 5. Clean up
docker stop ping-test && docker rm ping-test
```

### Development Container
```bash
# Build development image with debugging tools
docker build --target development -t kubernetes-ping-exporter:dev .

# Run with shell access
docker run -it --rm -p 9107:9107 -v $(pwd):/workspace kubernetes-ping-exporter:dev /bin/sh
```

### Building from Source
```bash
go mod download
go build -o kubernetes_ping_exporter
./kubernetes_ping_exporter
```

## License

MIT License
