# otel-k8s-controller

A Kubernetes-native operator that manages **OpenTelemetry Collector** deployments via a custom `OTelCollector` CRD. Built with [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) and the [Operator SDK](https://sdk.operatorframework.io/).

Designed for cloud-native observability pipelines on GCP, GDC edge environments, and standard Kubernetes clusters.

---

## Features

- **CRD-driven lifecycle** — declare your collector config as Kubernetes-native YAML; the controller handles Deployment, Service, and ConfigMap reconciliation
- **OTLP pipeline configuration** — supports both gRPC (4317) and HTTP (4318) receivers, configurable per CR
- **Sampling strategies** — head-based, tail-based (with latency/error policies), always-on, or always-off
- **Store-and-forward (SAF)** — buffers OTLP spans to disk when the backend is unreachable; auto-replays on reconnect — critical for GDC edge deployments with intermittent connectivity
- **Prometheus metrics bridging** — built-in scrape config and `/metrics` endpoint
- **Finalizer-based cleanup** — safe deletion with owned resource garbage collection via OwnerReferences
- **Leader election** — safe for multi-replica controller deployments
- **Status subresource** — exposes `phase`, `readyReplicas`, `exporterReachable`, and `bufferedSpanCount`

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   Kubernetes Cluster                     │
│                                                          │
│   ┌──────────────────┐      ┌──────────────────────┐    │
│   │  OTelCollector   │      │  otel-k8s-controller  │    │
│   │  CRD (CR yaml)   │─────▶│  reconciliation loop  │    │
│   └──────────────────┘      └──────────┬─────────────┘   │
│                                        │                  │
│              creates/updates           │                  │
│         ┌──────────────────────────────┤                  │
│         ▼              ▼              ▼                   │
│   ┌──────────┐  ┌──────────┐  ┌──────────────┐           │
│   │Deployment│  │ Service  │  │  ConfigMap   │           │
│   │(collector│  │(OTLP port│  │(config.yaml) │           │
│   │  pods)   │  │ 4317/18) │  └──────────────┘           │
│   └──────────┘  └──────────┘                             │
│         │                                                 │
│         ▼  OTLP gRPC/HTTP                                 │
│   ┌──────────────────────┐                               │
│   │  Exporter Endpoint   │  (Jaeger / GCP Trace / etc.)  │
│   └──────────────────────┘                               │
│                                                          │
│   Store-and-Forward: spans buffered to disk              │
│   if exporter is unreachable, replayed on reconnect      │
└─────────────────────────────────────────────────────────┘
```

---

## Quick Start

### Prerequisites

- Go 1.21+
- Kubernetes 1.25+ cluster (or [kind](https://kind.sigs.k8s.io/))
- `kubectl` configured
- `controller-gen` for manifest generation

### 1. Install the CRD

```bash
make manifests
make install
```

### 2. Run the controller locally

```bash
make run
```

### 3. Deploy a collector

```yaml
# config/samples/otelcollector_v1alpha1.yaml
apiVersion: otel.chandradevgo.io/v1alpha1
kind: OTelCollector
metadata:
  name: production-collector
  namespace: observability
spec:
  replicas: 2
  exporterEndpoint: "jaeger-collector.observability.svc.cluster.local:4317"

  pipeline:
    mode: grpc
    port: 4317
    enableTraces: true
    enableMetrics: true

  sampling:
    strategy: tail
    tailPolicies:
      - errors-policy
      - slow-traces-policy

  storeAndForward:
    enabled: true
    bufferPath: "/var/otel/buffer"
    maxBufferSizeMB: 512
    retryIntervalSeconds: 30
```

```bash
kubectl apply -f config/samples/otelcollector_v1alpha1.yaml
kubectl get otelcollectors -n observability
```

### 4. Check status

```bash
kubectl describe otelcollector production-collector -n observability
```

```
Status:
  Phase:               Running
  Ready Replicas:      2
  Exporter Reachable:  true
  Last Reconcile Time: 2024-04-17T10:30:00Z
  Conditions:
    Type:    Ready
    Status:  True
    Reason:  ReconcileSuccess
```

---

## CRD Reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `spec.replicas` | int | `1` | Number of collector pods |
| `spec.image` | string | `otel/opentelemetry-collector-contrib:latest` | Collector container image |
| `spec.exporterEndpoint` | string | required | OTLP backend endpoint |
| `spec.pipeline.mode` | `grpc` \| `http` | `grpc` | Receiver protocol |
| `spec.pipeline.port` | int | `4317` | Receiver port |
| `spec.sampling.strategy` | `head` \| `tail` \| `always_on` \| `always_off` | `always_on` | Sampling strategy |
| `spec.sampling.samplingRate` | float | `1.0` | Rate for head sampling (0.0–1.0) |
| `spec.storeAndForward.enabled` | bool | `false` | Enable offline buffering |
| `spec.storeAndForward.maxBufferSizeMB` | int | `512` | Max disk buffer size |
| `spec.storeAndForward.retryIntervalSeconds` | int | `30` | Retry flush interval |

---

## Store-and-Forward (GDC Edge)

The SAF mechanism is designed for **Google Distributed Cloud (GDC) edge** and other environments with intermittent backend connectivity.

When `storeAndForward.enabled: true`:
- The collector uses the `file_storage` extension to persist spans to the configured `bufferPath`
- If the exporter endpoint becomes unreachable, spans are queued to disk rather than dropped
- On reconnection, buffered spans are automatically flushed in order
- Buffer size is capped at `maxBufferSizeMB` to prevent runaway disk usage

```
Span ingested
     │
     ▼
Backend reachable? ──Yes──▶ Export via OTLP
     │
     No
     │
     ▼
Buffer to disk (file_storage)
     │
     ▼ (on reconnect)
Replay buffered spans ──▶ Export via OTLP
```

---

## Project Structure

```
otel-k8s-controller/
├── api/
│   └── v1alpha1/
│       └── otelcollector_types.go   # CRD type definitions
├── cmd/
│   └── manager/
│       └── main.go                  # Controller entrypoint
├── config/
│   ├── crd/                         # Generated CRD manifests
│   ├── rbac/                        # ClusterRole + ClusterRoleBinding
│   ├── manager/                     # Deployment manifest for controller
│   └── samples/                     # Example OTelCollector CRs
├── internal/
│   └── controller/
│       └── otelcollector_controller.go  # Reconciliation logic
├── Dockerfile
├── Makefile
└── go.mod
```

---

## Development

```bash
# Run tests
make test

# Run linter
make lint

# Build Docker image
make docker-build IMG=your-registry/otel-k8s-controller:dev

# Regenerate CRD manifests after type changes
make manifests generate
```

---

## Roadmap

- [ ] Horizontal Pod Autoscaler support based on span ingestion rate
- [ ] Multi-pipeline support (separate trace / metrics / logs pipelines per CR)
- [ ] Automatic TLS certificate provisioning via cert-manager
- [ ] Prometheus `ServiceMonitor` auto-creation
- [ ] Webhook for CR validation
- [ ] GDC-specific connectivity health checks

---

## Related Work

This controller draws on patterns from:
- [opentelemetry-operator](https://github.com/open-telemetry/opentelemetry-operator) — the upstream OTel operator
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) — reconciler framework
- [operator-sdk](https://sdk.operatorframework.io/) — operator scaffolding

---

## Author

**Ravi Chandra Eluri** — Sr. Golang Engineer · Kubernetes · OpenTelemetry  
[GitHub](https://github.com/chandradevGo) · [LinkedIn](https://linkedin.com/in/ravi-chandra18)
