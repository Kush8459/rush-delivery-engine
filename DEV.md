# Dev notes

All six services from `README.md` are implemented:

| Service              | Type     | Port  | Produces                     | Consumes                           |
|----------------------|----------|-------|------------------------------|------------------------------------|
| order-service        | REST     | 8080  | `order.placed`, `order.status.updated` | —                        |
| rider-service        | REST     | 8081  | —                            | —                                  |
| location-service     | WS       | 8083  | `rider.location.updated`     | —                                  |
| assignment-service   | consumer | —     | `order.assigned`             | `order.placed`                     |
| eta-service          | consumer | —     | `eta.updated`                | `rider.location.updated`           |
| notification-service | WS       | 8085  | —                            | all of the above                   |

## Data flow

```
POST /orders ─► order-service ─► Kafka:order.placed
                                      │
                                      ▼
                       assignment-service (Redis GEOSEARCH)
                                      │
                                      ▼
                           Kafka:order.assigned ─┐
                                                 │
   WS /ws/rider/:id ─► location-service          │
                              │                  │
                              ▼                  │
            Redis GEOADD, Kafka:rider.location.updated
                              │                  │
                              ▼                  ▼
                         eta-service       notification-service
                              │                  │
                              ▼                  ▼
                     Kafka:eta.updated    WS /ws/order/:id
                              │                  ▲
                              └──────────────────┘
```

## First-time setup

```bash
go mod tidy
docker compose up -d            # kafka + redis + postgres
make migrate-up                 # apply SQL migrations
```

Install migrate CLI once:
`go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`

## Run the stack (six terminals, or `make build` and run binaries)

```bash
make run-order
make run-rider
make run-location
make run-assignment
make run-eta
make run-notification
```

## End-to-end smoke test

```bash
# 1. Register a rider
curl -X POST http://localhost:8081/api/v1/riders \
  -H "Content-Type: application/json" \
  -d '{"name":"Ada","phone":"+911234567890"}'
# → copy the returned id; call it RIDER_ID

# 2. Put the rider online with a starting position
curl -X PATCH http://localhost:8081/api/v1/riders/$RIDER_ID/status \
  -H "Content-Type: application/json" \
  -d '{"status":"AVAILABLE","lat":28.6139,"lng":77.2090}'

# 3. Connect as a customer to receive live updates (use websocat, wscat, or a tab)
#    Leave this open — events will stream in:
wscat -c ws://localhost:8085/ws/order/$ORDER_ID     # (ORDER_ID from step 4)

# 4. Login to get a JWT (all POST/PATCH /orders require Bearer auth)
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222"}' | jq -r .token)

# 5. Place an order
curl -X POST http://localhost:8080/api/v1/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Customer-Id: 22222222-2222-2222-2222-222222222222" \
  -d '{
    "customer_id":"22222222-2222-2222-2222-222222222222",
    "restaurant_id":"33333333-3333-3333-3333-333333333333",
    "total_amount":249.50,
    "delivery_address":"Test address",
    "pickup_lat":28.6139, "pickup_lng":77.2090,
    "dropoff_lat":28.6200, "dropoff_lng":77.2150
  }'
# Within ~1s the customer WS receives order_assigned.

# 6. Simulate rider GPS updates
wscat -c ws://localhost:8083/ws/rider/$RIDER_ID
> {"type":"location_update","payload":{"lat":28.6145,"lng":77.2095}}
# The customer WS receives rider_location + eta_updated events.
```

## Rate limiting

Order placement is sliding-window throttled at **5/min per `X-Customer-Id`**
(configurable via `rate_limit.orders_per_minute`). Over limit → `429` with
`Retry-After`. Missing header → unthrottled (useful for internal probes).

## Verify with SQL

```bash
docker compose exec postgres psql -U postgres -d delivery_engine -c \
  "SELECT id, status, rider_id, eta_minutes FROM orders ORDER BY created_at DESC LIMIT 3;"

docker compose exec postgres psql -U postgres -d delivery_engine -c \
  "SELECT event_type, created_at FROM order_events ORDER BY id DESC LIMIT 10;"
```

## What's deliberately not here yet

- **Idempotency keys** — duplicate `POST /orders` creates duplicate orders.
- **Dead letter queues** — poison messages are logged and committed with no
  replay mechanism.
- **Graceful consumer shutdown** — SIGTERM cancels in-flight handlers; can cause
  partial writes.
- **Log↔trace correlation** — spans have trace_ids but `zerolog` output doesn't
  yet pull them into log fields. A zerolog hook that reads
  `trace.SpanFromContext` closes this gap.
- **pgx + Redis auto-instrumentation** — service-level spans are wired, but
  driver-level SQL/Redis spans aren't. Easy add when SQL latency needs debugging.
- **Per-service Dockerfiles** — one multi-stage image builds all six via a
  `SERVICE` build arg. That's intentional and probably stays.

**Already shipped:** JWT auth on orders, transactional outbox with Kafka-outage
resilience, OpenTelemetry distributed tracing end-to-end (HTTP → DB → outbox →
Kafka → consumer), Prometheus metrics on every HTTP service + Grafana + Jaeger
dashboards, sliding-window rate limiter, unit tests for state machine / ETA /
geo / JWT.
