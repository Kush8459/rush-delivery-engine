# Kubernetes manifests

Plain YAML for deploying the 6 Go services. **Data plane (Postgres, Redis,
Kafka) is not included** — use managed services, a Helm chart (e.g. bitnami/*),
or an operator (Strimzi for Kafka) for those. Hook them up by updating the
`host`/`addr`/`brokers` values in `configmap.yaml`.

## Files

| File                        | What it ships                                        |
|-----------------------------|------------------------------------------------------|
| `namespace.yaml`            | `rush` namespace                                     |
| `configmap.yaml`            | Non-secret config mounted at `/config/config.yaml`   |
| `secret.yaml`               | JWT secret + DB password (dev placeholders)          |
| `order-service.yaml`        | Deployment + Service (:8080)                         |
| `rider-service.yaml`        | Deployment + Service (:8081)                         |
| `location-service.yaml`     | Deployment + Service (:8083, sticky WS)              |
| `notification-service.yaml` | Deployment + Service (:8085, sticky WS)              |
| `consumers.yaml`            | assignment + eta (headless — Kafka-only)             |
| `ingress.yaml`              | NGINX Ingress routing REST + WS                      |

## Apply to a cluster

```bash
# Build and push images to a registry your cluster can pull from, then:
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/
```

Images expected: `rush/order-service:latest`, etc. Replace with your registry
path (`registry.example.com/rush/order-service:v1.0.0`) by editing the
`image:` lines or patching via kustomize/Helm overlay.

## Notes

- **WebSocket services** (`location-service`, `notification-service`) use
  `sessionAffinity: ClientIP` so long-lived connections stay on one pod.
- **Prometheus scraping** is annotation-based — a Prometheus Operator
  ServiceMonitor is the cleaner path for production.
- **Horizontal scaling**: HTTP services scale freely; Kafka consumers scale
  up to the partition count on their subscribed topic (extras sit idle).
