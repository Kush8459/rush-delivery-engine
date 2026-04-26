# Real-Time Order Tracking & Delivery Engine

A production-grade backend system built in Go that simulates the core infrastructure
behind food delivery platforms like Swiggy, Zomato, and Zepto. Features real-time
order tracking, geo-based rider assignment, live location updates, and an
event-driven architecture using Kafka.

> Backend-only project. All interaction is via REST, WebSocket, or `curl`/`wscat`.

---

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [System Design](#system-design)
- [Architecture Diagram](#architecture-diagram)
- [Tech Stack](#tech-stack)
- [Project Structure](#project-structure)
- [Services Breakdown](#services-breakdown)
- [Database Schema](#database-schema)
- [Kafka Topics](#kafka-topics)
- [API Reference](#api-reference)
- [WebSocket Events](#websocket-events)
- [Getting Started](#getting-started)
- [Environment Variables](#environment-variables)
- [Design Decisions](#design-decisions)
- [Future Improvements](#future-improvements)

---

## Overview

This system handles the full lifecycle of a food delivery order — from placement
to delivery — in real time. It is designed to be horizontally scalable, fault-tolerant,
and observable, following patterns used by real-world delivery platforms.

The system is composed of multiple independent services that communicate
asynchronously via Kafka, with Redis handling ephemeral real-time state and
Postgres storing persistent data.

---

## Features

- **Order Lifecycle Management** — Full state machine from PLACED → DELIVERED
- **Real-Time Location Tracking** — Riders push GPS coordinates via WebSocket; customers subscribe to live updates
- **Geo-Based Rider Assignment** — Nearest available rider is auto-assigned using Redis geospatial queries
- **ETA Estimation** — Dynamic ETA recalculated on every rider location update
- **Event-Driven Architecture** — Every state transition publishes a Kafka event; services are fully decoupled
- **Transactional Outbox** — Order events committed atomically with DB writes; background relay drains to Kafka, so no event is lost during a Kafka outage
- **Distributed Tracing** — OpenTelemetry propagates trace context across HTTP → DB → Kafka → consumer in one trace, including across the outbox
- **Rate Limiting** — Per-user order placement throttling using a sliding window algorithm
- **Audit Log** — Immutable append-only `order_events` log of every state transition

---

## System Design

### High-Level Flow

```
Customer App
     │
     │  POST /orders
     ▼
[Order Service] ──── publishes ──▶ Kafka: order.placed
     │                                      │
     │                             [Rider Assignment Service]
     │                                      │
     │                             Redis GEORADIUS query
     │                                      │
     │                             assigns nearest rider
     │                                      │
     │                             publishes ──▶ Kafka: order.assigned
     │
[Rider App] ──── WebSocket ──▶ [Location Service]
                                      │
                              Redis GEOADD (live position)
                                      │
                              publishes ──▶ Kafka: rider.location.updated
                                      │
                              [ETA Service] recalculates ETA
                                      │
                              WebSocket push ──▶ Customer App
```

### Order State Machine

```
  [PLACED]
     │
     │ restaurant accepts
     ▼
  [ACCEPTED]
     │
     │ food is being prepared
     ▼
  [PREPARING]
     │
     │ rider picks up order
     ▼
  [PICKED_UP]
     │
     │ rider reaches customer
     ▼
  [DELIVERED]

  Any state ──▶ [CANCELLED]  (within allowed window)
```

Each transition publishes an event to Kafka. No service directly calls another
service to change state — everything flows through events.

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                        API Gateway                          │
│              (rate limiting, auth middleware)               │
└────────┬────────────┬──────────────┬───────────────────────┘
         │            │              │
         ▼            ▼              ▼
  ┌────────────┐ ┌──────────┐ ┌───────────────┐
  │   Order    │ │  Rider   │ │   Location    │
  │  Service   │ │  Service │ │   Service     │
  └─────┬──────┘ └────┬─────┘ └──────┬────────┘
        │             │               │
        └──────────────┴───────────────┘
                       │
                       ▼
              ┌─────────────────┐
              │      Kafka      │
              │  (Event Bus)    │
              └────────┬────────┘
                       │
        ┌──────────────┼──────────────┐
        ▼              ▼              ▼
 ┌────────────┐ ┌────────────┐ ┌──────────────┐
 │  Rider     │ │    ETA     │ │  Notification│
 │Assignment  │ │  Service   │ │   Service    │
 │  Service   │ └─────┬──────┘ └──────┬───────┘
 └────────────┘       │               │
                      ▼               ▼
               WebSocket Push    WebSocket Push
               (ETA updates)    (status updates)
                      │               │
                      └───────┬───────┘
                              ▼
                       Customer App

┌──────────────────────────────────────────┐
│              Data Stores                 │
│                                          │
│  Postgres  ──  orders, users, riders     │
│  Redis     ──  live locations, sessions  │
│  Redis     ──  rate limiting counters    │
└──────────────────────────────────────────┘
```

---

## Tech Stack

| Layer | Technology | Purpose |
|---|---|---|
| Language | Go 1.25+ | Core backend language |
| HTTP Framework | `net/http` + `chi` router | REST API |
| WebSockets | `gorilla/websocket` | Real-time communication |
| Message Broker | Apache Kafka 3.8 (KRaft) | Async event bus |
| Kafka Client | `segmentio/kafka-go` | Producer/consumer |
| Cache / Geo | Redis 7 | Live locations, rate limiting |
| Database | PostgreSQL 15 | Persistent storage + outbox |
| Migrations | `golang-migrate` | DB schema versioning |
| Tracing | OpenTelemetry + Jaeger | Distributed tracing across services |
| Metrics | Prometheus + Grafana | RED metrics + business KPIs |
| Containerization | Docker + Docker Compose | Local development |
| Config | `viper` | YAML + env-var overrides |
| Logging | `zerolog` | Structured JSON logging |
| Testing | Go test + testcontainers | Unit + integration tests |

---

## Project Structure

```
rush-delivery-engine/
│
├── cmd/
│   ├── order-service/         # Entry point for order service
│   ├── rider-service/         # Entry point for rider service
│   ├── location-service/      # Entry point for location service
│   ├── assignment-service/    # Entry point for rider assignment service
│   ├── eta-service/           # Entry point for ETA calculation service
│   └── notification-service/  # Entry point for WebSocket push service
│
├── internal/
│   ├── order/
│   │   ├── handler.go         # HTTP handlers
│   │   ├── service.go         # Business logic
│   │   ├── repository.go      # DB queries
│   │   ├── statemachine.go    # Order state transitions
│   │   └── model.go           # Structs and types
│   │
│   ├── rider/
│   │   ├── handler.go
│   │   ├── service.go
│   │   └── repository.go
│   │
│   ├── location/
│   │   ├── handler.go         # WebSocket upgrade + connection manager
│   │   ├── service.go         # Geo updates, Redis writes
│   │   └── hub.go             # WebSocket connection hub
│   │
│   ├── assignment/
│   │   ├── consumer.go        # Kafka consumer for order.placed
│   │   └── service.go         # Geo query + atomic rider claim + outbox emit
│   │
│   ├── eta/
│   │   ├── consumer.go        # Kafka consumer for rider.location.updated
│   │   └── calculator.go      # ETA formula
│   │
│   ├── notification/
│   │   ├── consumer.go        # Consumes all events
│   │   └── hub.go             # Pushes to subscribed WebSocket clients
│   │
│   └── outbox/
│       ├── outbox.go          # Write(tx, topic, key, payload) — used by services
│       └── relay.go           # Polls outbox, publishes to Kafka, marks sent
│
├── pkg/
│   ├── kafka/                 # Producer/consumer wrappers + admin (EnsureTopics) + OTel propagation
│   ├── telemetry/             # OTel SDK setup + OTLP HTTP exporter
│   ├── redisx/                # Redis client wrapper
│   ├── postgres/              # DB connection pool setup
│   ├── auth/                  # JWT signer + verifier + middleware
│   ├── middleware/            # Rate limiter, etc.
│   ├── metrics/               # Prometheus registry + HTTP middleware
│   └── geo/                   # Haversine distance utilities
│
├── migrations/                # SQL migration files (up + down per step)
│   ├── 001_create_riders.*.sql
│   ├── 002_create_orders.*.sql
│   ├── 003_create_order_events.*.sql
│   ├── 004_create_outbox.*.sql
│   └── 005_outbox_trace_context.*.sql
│
├── config/
│   ├── config.yaml            # Default configuration (local dev)
│   └── config.docker.yaml     # Overrides for the compose app profile
│
├── Dockerfile                 # Single multi-stage image; SERVICE arg picks entrypoint
├── docker-compose.yml         # Full local stack (infra + app profile + obs profile)
├── Makefile                   # Common dev commands
└── README.md
```

---

## Services Breakdown

### 1. Order Service
- Exposes REST endpoints for placing and managing orders
- Validates request, writes to Postgres, publishes `order.placed` to Kafka
- Handles state transitions via the state machine

### 2. Rider Service
- Manages rider profiles, availability status, and shift data
- Exposes endpoints for riders to go online/offline

### 3. Location Service
- Accepts WebSocket connections from rider apps
- On every GPS update: writes to Redis using `GEOADD`, publishes `rider.location.updated` to Kafka
- Also accepts customer WebSocket subscriptions for their order's rider location

### 4. Assignment Service
- Consumes `order.placed` events from Kafka
- Queries Redis with `GEORADIUS` to find nearest available rider
- Updates rider status to BUSY, publishes `order.assigned`

### 5. ETA Service
- Consumes `rider.location.updated` events
- Recalculates ETA using straight-line distance / average speed formula
- Publishes `eta.updated` event

### 6. Notification Service
- Consumes all events from Kafka
- Pushes real-time updates to subscribed WebSocket clients (customers)
- Manages a connection hub mapping `order_id → []WebSocket connections`

---

## Database Schema

### orders
```sql
CREATE TABLE orders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id UUID NOT NULL,
    rider_id    UUID,
    restaurant_id UUID NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'PLACED',
    total_amount NUMERIC(10, 2) NOT NULL,
    delivery_address TEXT NOT NULL,
    eta_minutes INT,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);
```

### riders
```sql
CREATE TABLE riders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) NOT NULL,
    phone       VARCHAR(15) UNIQUE NOT NULL,
    status      VARCHAR(20) DEFAULT 'OFFLINE', -- OFFLINE | AVAILABLE | BUSY
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
```

### order_events (audit log)
```sql
CREATE TABLE order_events (
    id          BIGSERIAL PRIMARY KEY,
    order_id    UUID NOT NULL,
    event_type  VARCHAR(50) NOT NULL,
    payload     JSONB,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
-- Append-only. No updates or deletes ever happen on this table.
```

### outbox (transactional outbox)
```sql
CREATE TABLE outbox (
    id            BIGSERIAL PRIMARY KEY,
    aggregate_id  UUID NOT NULL,
    topic         VARCHAR(100) NOT NULL,
    partition_key VARCHAR(100) NOT NULL,
    payload       JSONB NOT NULL,
    trace_context JSONB,                  -- captured OTel W3C propagation map
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at       TIMESTAMPTZ
);
CREATE INDEX idx_outbox_unsent ON outbox (id) WHERE sent_at IS NULL;
-- Partial index keeps the relay's poll query O(unsent) regardless of table size.
```

---

## Kafka Topics

| Topic | Producer | Consumer | Payload |
|---|---|---|---|
| `order.placed` | Order Service | Assignment Service | order_id, location, items |
| `order.assigned` | Assignment Service | Notification Service | order_id, rider_id |
| `order.status.updated` | Order Service | Notification Service | order_id, new_status |
| `rider.location.updated` | Location Service | ETA Service, Notification Service | rider_id, lat, lng |
| `eta.updated` | ETA Service | Notification Service | order_id, eta_minutes |

All topics use `order_id` as the partition key to guarantee ordering per order.

---

## API Reference

### Orders

```
POST   /api/v1/orders              Place a new order
GET    /api/v1/orders/:id          Get order details + current status
PATCH  /api/v1/orders/:id/cancel   Cancel an order
GET    /api/v1/orders/:id/track    Get live tracking info (ETA, rider location)
```

### Riders

```
POST   /api/v1/riders              Register a rider
PATCH  /api/v1/riders/:id/status   Toggle online/offline
GET    /api/v1/riders/:id/orders   Get active order for a rider
```

### WebSockets

```
WS  /ws/rider/:rider_id            Rider connects to push location updates
WS  /ws/order/:order_id            Customer connects to receive live updates
```

---

## WebSocket Events

### Rider → Server (location push)
```json
{
  "type": "location_update",
  "payload": {
    "lat": 28.6139,
    "lng": 77.2090,
    "timestamp": "2024-11-01T14:32:00Z"
  }
}
```

### Server → Customer (order update)
```json
{
  "type": "order_status_changed",
  "payload": {
    "order_id": "abc-123",
    "status": "PICKED_UP",
    "eta_minutes": 8
  }
}
```

```json
{
  "type": "rider_location",
  "payload": {
    "lat": 28.6145,
    "lng": 77.2095
  }
}
```

---

## Getting Started

### Prerequisites
- Go 1.25+
- Docker + Docker Compose
- `golang-migrate` CLI

### Run locally

```bash
git clone https://github.com/Kush8459/rush-delivery-engine.git
cd rush-delivery-engine

# Start infrastructure (Kafka in KRaft mode — no ZooKeeper, Redis, Postgres)
docker compose up -d

# Run database migrations (Postgres is on host port 5433)
make migrate-up

# Start all services (one per terminal, or use `make build` + ./bin/*)
make run-order
make run-rider
make run-location
make run-notification
make run-assignment
make run-eta
```

See `RUN.md` for the full runbook including sanity checks, introspection
queries, and troubleshooting.

### Test the flow

```bash
# 1. Get a JWT (order endpoints require Authorization: Bearer <token>)
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222"}' | jq -r .token)

# 2. Place an order
curl -X POST http://localhost:8080/api/v1/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'X-Customer-Id: 22222222-2222-2222-2222-222222222222' \
  -d '{
    "customer_id":"22222222-2222-2222-2222-222222222222",
    "restaurant_id":"33333333-3333-3333-3333-333333333333",
    "total_amount":249.50,
    "delivery_address":"Test",
    "pickup_lat":28.6139, "pickup_lng":77.2090,
    "dropoff_lat":28.6200, "dropoff_lng":77.2150
  }'

# 3. Connect as a customer to receive live updates
#    wscat -c ws://localhost:8085/ws/order/<order_id>

# 4. Simulate a rider sending GPS updates
#    wscat -c ws://localhost:8083/ws/rider/<rider_id>
#    Send: {"type":"location_update","payload":{"lat":28.6145,"lng":77.2095}}
#    Watch the customer WebSocket receive `rider_location` + `eta_updated`.
```

Full end-to-end walkthrough + debugging queries: see `RUN.md`.

---

## Configuration

Each service reads `config/config.yaml` (override path with `CONFIG_PATH` env var). Any YAML key can be overridden at runtime by a `DE_*` env var — nested keys use `_` instead of `.`.

Examples:

```env
DE_POSTGRES_HOST=db.internal
DE_POSTGRES_PORT=5432
DE_POSTGRES_NAME=delivery_engine
DE_POSTGRES_USER=postgres
DE_POSTGRES_PASSWORD=postgres

DE_REDIS_ADDR=localhost:6379

DE_KAFKA_BROKERS=localhost:9092
DE_KAFKA_GROUP_ID=delivery-engine

DE_SERVICES_ORDER_PORT=8080
DE_SERVICES_RIDER_PORT=8081
DE_SERVICES_LOCATION_PORT=8083
DE_SERVICES_NOTIFY_PORT=8085

DE_RATE_LIMIT_ORDERS_PER_MINUTE=5

DE_AUTH_JWT_SECRET=dev-only-secret-change-me
DE_AUTH_TTL_HOURS=24

DE_TELEMETRY_ENABLED=true
DE_TELEMETRY_OTLP_ENDPOINT=localhost:4318   # Jaeger all-in-one (obs profile)
```

See `pkg/config/config.go` for the full schema and defaults.

---

## Design Decisions

Highlights below. The full list — including the libraries picked (chi, pgx,
kafka-go, zerolog, viper), infrastructure (KRaft-mode Kafka, distroless
images), and what was deliberately **not** used (ORM, gRPC, service mesh) —
lives in [`DECISIONS.md`](./DECISIONS.md).

**Why Kafka over direct HTTP calls between services?**
Services are fully decoupled. If the Assignment Service is down, the event is
not lost — it waits in Kafka until the consumer comes back online. This gives
us at-least-once delivery and resilience for free.

**Why Redis for rider locations instead of Postgres?**
Rider locations update every few seconds per active rider. Writing thousands of
rows per minute to Postgres is wasteful. Redis `GEOADD` is O(log N) and keeps
only the latest position in memory. Postgres is used only for durable, slower-
changing data.

**Why partition Kafka topics by order_id?**
All events for a single order go to the same partition, guaranteeing that
consumers see events in the correct order (PLACED before ASSIGNED before
PICKED_UP). Without this, race conditions in the state machine are possible.

**Why an append-only order_events table?**
Regulatory and debugging requirement. You must be able to replay exactly what
happened to any order. The `orders` table stores current state; `order_events`
stores the full history. This is the Event Sourcing pattern.

**Why the transactional outbox pattern?**
Writing to Postgres and publishing to Kafka as two separate steps risks losing
events if Kafka is slow/down in between. The outbox commits the event row in
the same tx as the business write, and a relay goroutine forwards it with
`FOR UPDATE SKIP LOCKED`. A Kafka outage no longer drops events — they queue
and drain on recovery.

---

## Scope & next steps

This repo is intentionally scoped to **backend fundamentals**: event-driven
microservices, exactly-once publishing via the outbox, and distributed tracing
across async boundaries. Payments, restaurant/menu domain, ML-based ETA, and
full user management are out of scope — the commits for `outbox` and
`OpenTelemetry` are representative of the bar.

Natural next steps if this were to keep evolving:

- [ ] Idempotency keys on `POST /orders`
- [ ] Dead letter queue for poison Kafka messages
- [ ] Graceful consumer shutdown on SIGTERM
- [ ] Log↔trace correlation (inject `trace_id` into every zerolog line)
- [ ] Real road-distance ETA (self-hosted OSRM) to replace the haversine stand-in
- [ ] Tiered assignment retry (2 km → 5 km → 10 km + pending queue)

---

## License

MIT
