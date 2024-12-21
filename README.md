# Kubernetes Ping Exporter

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

To build the Docker image:
```bash
docker build -t kubernetes-ping-exporter .
```

## License

MIT License
