# Kubernetes Ping Exporter

A Prometheus exporter for ICMP ping metrics in Kubernetes environments. This exporter allows you to monitor network latency to specified IP addresses from within your Kubernetes cluster.

## Features

- Exports ICMP ping metrics in Prometheus format
- Runs as a Kubernetes deployment
- Configurable target IPs via ConfigMap
- Integrated with Prometheus Operator via ServiceMonitor
- RBAC-ready with necessary permissions

## Prerequisites

- Kubernetes cluster
- Helm v3
- Prometheus Operator installed in the cluster

## Installation

```bash
helm install ping-exporter ./infra-ping-exporter -n monitoring
```

## Configuration

The exporter is configured via a ConfigMap. By default, it pings the following IP addresses:
- 1.1.1.1 (Cloudflare DNS)
- 8.8.8.8 (Google DNS)

To modify target IPs, update the `additional_ips` field in the ConfigMap.

## Components

- **Service**: Headless service exposing port 9107
- **ServiceMonitor**: Prometheus Operator integration
- **RBAC**: ServiceAccount, Role, and RoleBinding for necessary permissions
- **ConfigMap**: Configuration for target IP addresses

## Metrics

The exporter provides metrics on port 9107 and is automatically discovered by Prometheus through the ServiceMonitor configuration.

## Docker Image

The exporter uses a multi-stage build process with Python 3.12 and includes:
- Python dependencies
- fping utility
- Exposed port 9107