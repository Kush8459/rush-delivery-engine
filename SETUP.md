# Environment setup (Windows 11)

Everything you need to install on a fresh machine to run this project end-to-end.

---

## 1. Prerequisites

### Go 1.25+
Already installed on this box (`go1.25.4`). If starting fresh:
- Download from <https://go.dev/dl/> (pick the Windows MSI)
- Verify: `go version`

### Docker Desktop
All infrastructure (Kafka, Redis, Postgres) runs in containers — you do **not**
need to install them on the host.
- Download <https://www.docker.com/products/docker-desktop/>
- Make sure WSL2 backend is enabled
- Verify: `docker version` and `docker compose version`

### Git (optional but recommended)
`winget install Git.Git` or <https://git-scm.com/download/win>

### golang-migrate CLI (for applying SQL migrations)
One-time install:
```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```
Make sure `%USERPROFILE%\go\bin` is in your `PATH`. Verify: `migrate -version`

### wscat (for poking WebSockets)
Needs Node.js. If you don't have it: `winget install OpenJS.NodeJS`
Then:
```bash
npm install -g wscat
```
Verify: `wscat --version`

### make (optional — the Makefile targets are for convenience)
Windows doesn't ship `make`. Options:
- `choco install make` (with Chocolatey), or
- `winget install ezwinports.make`, or
- Just run the equivalent `go run`/`docker` commands directly — every Makefile
  target is a one-liner you can copy.

---

## 2. Start the infrastructure

```bash
cd "D:/dot net poc/Golang/rush-delivery-engine"
docker compose up -d
```

This starts three containers:
| Container  | Image                 | Host port | Purpose                    |
|------------|-----------------------|-----------|----------------------------|
| `postgres` | `postgres:15-alpine`  | 5433 *    | Orders, riders, audit log  |
| `redis`    | `redis:7-alpine`      | 6379      | Geo index + rate limiter   |
| `kafka`    | `apache/kafka:3.8.0`  | 9092      | Event bus (KRaft, no ZK)   |

Confirm they're healthy:
```bash
docker compose ps
```
All three should show `(healthy)` or `Up` after ~15s (Kafka takes the longest).

**Stop everything:** `docker compose down`
**Wipe data and start clean:** `docker compose down -v`

### Observability stack (optional)

Add `--profile obs` to bring up three more containers for metrics + traces:

```bash
docker compose --profile obs up -d
```

| Container    | Image                              | Host port | Purpose                              |
|--------------|------------------------------------|-----------|--------------------------------------|
| `prometheus` | `prom/prometheus:v2.55.0`          | 9090      | Scrapes `/metrics` on each service   |
| `grafana`    | `grafana/grafana:11.2.2`           | 3000      | Pre-provisioned dashboard            |
| `jaeger`     | `jaegertracing/all-in-one:1.76.0`  | 16686, 4318 | Trace UI + OTLP HTTP receiver       |

Services export traces to `localhost:4318` by default (configurable via `DE_TELEMETRY_OTLP_ENDPOINT`). When `--profile obs` is off, tracing still works internally but has nowhere to export — spans batch up and are dropped on service shutdown.

---

## 3. Apply database migrations

```bash
migrate -path migrations -database "postgres://postgres:postgres@localhost:5433/delivery_engine?sslmode=disable" up
```

Or with make: `make migrate-up`

You should see:
```
1/u create_riders (12ms)
2/u create_orders (18ms)
3/u create_order_events (7ms)
```

Revert one step: `make migrate-down`

---

## 4. Fetch Go dependencies

```bash
go mod tidy
```

---

## 5. Run the services

Six terminals (or `make build` and run the binaries from `bin/`):

```bash
make run-order            # terminal 1 — REST :8080
make run-rider            # terminal 2 — REST :8081
make run-location         # terminal 3 — WS   :8083
make run-notification     # terminal 4 — WS   :8085
make run-assignment       # terminal 5 — Kafka consumer
make run-eta              # terminal 6 — Kafka consumer
```

Each logs as structured JSON with a `service` field so you can follow one service's logs in isolation.

---

## 6. Poking the infrastructure directly

### Postgres
```bash
# Interactive psql
docker compose exec postgres psql -U postgres -d delivery_engine

# One-off queries
docker compose exec postgres psql -U postgres -d delivery_engine -c "\dt"
docker compose exec postgres psql -U postgres -d delivery_engine -c \
  "SELECT id, status, rider_id FROM orders ORDER BY created_at DESC LIMIT 5;"
```

Default credentials (from `docker-compose.yml`):
- DB: `delivery_engine`, user: `postgres`, password: `postgres`
- Connect from a GUI (DBeaver, pgAdmin) on `localhost:5433`

> *Host port is **5433** (not the default 5432) so it doesn't clash with a
> native Windows Postgres install that may already bind 5432. Inside the
> container it's still 5432.

### Redis
```bash
# Interactive CLI
docker compose exec redis redis-cli

# One-offs:
docker compose exec redis redis-cli PING
docker compose exec redis redis-cli ZRANGE riders:geo 0 -1 WITHSCORES
docker compose exec redis redis-cli GEOPOS riders:geo <rider-uuid>
docker compose exec redis redis-cli KEYS 'rl:orders:*'     # rate limit buckets
```

### Kafka

> **Git Bash users (Windows):** prefix each command with `MSYS_NO_PATHCONV=1` so
> Git Bash doesn't try to translate `/opt/kafka/...` into a Windows path.
> PowerShell and cmd.exe don't need this.
>
> In the apache/kafka image the scripts aren't on `PATH`, so use the full
> `/opt/kafka/bin/` prefix shown below.

```bash
# List topics
docker compose exec kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server localhost:9092 --list

# Describe a topic (partitions, leaders, offsets)
docker compose exec kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server localhost:9092 --describe --topic order.placed

# Tail messages from the start of a topic (handy for debugging)
docker compose exec kafka /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 --topic order.placed --from-beginning

# Tail with key printed
docker compose exec kafka /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 --topic order.assigned \
  --property print.key=true --property key.separator=" | "

# Manually publish a test message
docker compose exec -T kafka /opt/kafka/bin/kafka-console-producer.sh \
  --bootstrap-server localhost:9092 --topic order.placed \
  --property "parse.key=true" --property "key.separator=:"
> test-key:{"order_id":"..."}

# See consumer group lag (are my services caught up?)
docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh \
  --bootstrap-server localhost:9092 --describe --all-groups
```

Topics are pre-created idempotently by each service at startup (see
`pkg/kafka/admin.go::EnsureTopics`). The compose file also has
`KAFKA_AUTO_CREATE_TOPICS_ENABLE=true` as a safety net, but the `EnsureTopics`
call is what guarantees consumer groups get partitions on first join — without
it, consumers that happened to start before their topic existed would have
silently received zero partitions. In production you'd pre-create with
explicit partition counts and replication, and disable auto-create entirely.

---

## 7. End-to-end smoke test

See `DEV.md` — same curl + wscat flow, already laid out there.

Quick version:

```bash
# Register a rider
RIDER_ID=$(curl -s -X POST http://localhost:8081/api/v1/riders \
  -H "Content-Type: application/json" \
  -d '{"name":"Ada","phone":"+911234567890"}' | jq -r .id)

# Put them online
curl -X PATCH http://localhost:8081/api/v1/riders/$RIDER_ID/status \
  -H "Content-Type: application/json" \
  -d '{"status":"AVAILABLE","lat":28.6139,"lng":77.2090}'

# Login → JWT (POST/PATCH /orders require Bearer auth)
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222"}' | jq -r .token)

# Place an order
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
```

Within ~1 second:
- Order row has `rider_id` populated
- Rider row flips `AVAILABLE → BUSY`
- `order_events` gains `order.placed` and `order.assigned` rows
- Kafka `order.assigned` topic has a new message

---

## 8. Common issues

**`dial tcp [::1]:9092: connectex: No connection could be made`**
Kafka isn't up yet. Wait ~15s after `docker compose up -d`, then retry.
Check with `docker compose logs kafka --tail 30`.

**`ERROR: database "delivery_engine" does not exist`**
First-run DB wasn't created. Stop the stack, `docker compose down -v`, then
`docker compose up -d` again. The `POSTGRES_DB` env var only runs on first init.

**`migrate: no change`**
Migrations already applied. Harmless.

**429 Too Many Requests on `POST /orders`**
You hit the 5/min rate limit for that `X-Customer-Id`. Wait 60s or use a
different customer header.

**WebSocket connects but no events arrive**
Most likely: no active order for the rider you're sending locations for.
Check with:
```bash
docker compose exec postgres psql -U postgres -d delivery_engine -c \
  "SELECT id, status, rider_id FROM orders WHERE rider_id = '<rider-uuid>';"
```
