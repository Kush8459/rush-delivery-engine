# Run the project — personal runbook

Shell: **PowerShell** (Windows 11) or **bash**. Project root: `D:\dot net poc\Golang\rush-delivery-engine`.

Every command below is either runnable as-is from the project root, or uses a `docker compose exec` prefix so it doesn't care about your host OS.

---

## 1. Prereqs (one-time)

- **Docker Desktop** running
- **Go 1.25+** — `go version`
- **golang-migrate** CLI:
  ```powershell
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```
- **Postman** (desktop or the VS Code extension) — needed for WebSocket testing
- **wscat** (optional, for terminal WebSocket testing): `npm i -g wscat`
- **jq** (optional, used by some curl one-liners): `winget install jqlang.jq`

---

## 2. Start the infrastructure (every session)

```powershell
docker compose up -d
```

Wait ~15s. Verify the three containers are up:

```powershell
docker compose ps
```

Expect `postgres`, `redis`, `kafka` all `Up`. Health:
- Postgres on host port **5433** (not 5432 — avoids clashing with a native Windows Postgres).
- Redis on 6379.
- Kafka on 9092 (single-node KRaft mode, no ZooKeeper).

---

## 3. Apply DB migrations (first time only, or after `docker compose down -v`)

```powershell
migrate -path migrations -database "postgres://postgres:postgres@localhost:5433/delivery_engine?sslmode=disable" up
```

Verify the 4 tables exist:

```powershell
docker compose exec postgres psql -U postgres -d delivery_engine -c "\dt"
```

Expected output:
```
 public | order_events      | table | postgres
 public | orders            | table | postgres
 public | riders            | table | postgres
 public | schema_migrations | table | postgres
```

---

## 4. Run the six Go services

Open **six terminals** (VS Code terminal splits are fine). Each stays attached to its service — **don't close them** while testing.

| # | Command                                  | Port  | Role                             |
|---|------------------------------------------|-------|----------------------------------|
| 1 | `go run ./cmd/order-service`             | 8080  | REST: `/api/v1/orders`, `/auth`  |
| 2 | `go run ./cmd/rider-service`             | 8081  | REST: `/api/v1/riders`           |
| 3 | `go run ./cmd/location-service`          | 8083  | WS: `/ws/rider/{id}`             |
| 4 | `go run ./cmd/notification-service`      | 8085  | WS: `/ws/order/{id}`             |
| 5 | `go run ./cmd/assignment-service`        | —     | Kafka consumer (`order.placed`)  |
| 6 | `go run ./cmd/eta-service`               | —     | Kafka consumer (`rider.location.updated`) |

Each service logs 1–2 JSON lines and sits idle.

### Sanity check: all 6 services healthy

```bash
for port in 8080 8081 8083 8085; do echo -n "port $port: "; curl -s -o /dev/null -w "%{http_code}\n" http://localhost:$port/healthz; done
```

Expect `200` four times.

### Sanity check: all 6 Kafka consumer groups alive with partitions

```powershell
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --all-groups --members"
```

Expect 6 groups (`delivery-engine.assignment`, `.eta`, and 4 × `.notify.*`), each with `#PARTITIONS = 1`. If any shows `0`, a service was started before its topic existed — `EnsureTopics` at boot should prevent this, but restart the affected service if it happens.

### Alternative: run everything in Docker (no local Go needed)

```powershell
docker compose --profile app build        # one-time build
docker compose --profile app up -d        # run all 6 services as containers
docker compose logs -f                    # tail combined logs
```

Add `--profile obs` for Prometheus + Grafana.

---

## 5. Happy-path test (curl — fastest)

Paste each block in turn. Replace bash variables if you're on PowerShell, or run from a Git Bash / WSL terminal.

### 5a. Register a rider
```bash
RID=$(curl -s -X POST http://localhost:8081/api/v1/riders \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ada","phone":"+911234567890"}' | jq -r .id)
echo "rider_id: $RID"
```

### 5b. Put rider online at pickup coords
```bash
curl -s -X PATCH "http://localhost:8081/api/v1/riders/$RID/status" \
  -H 'Content-Type: application/json' \
  -d '{"status":"AVAILABLE","lat":28.6139,"lng":77.2090}'
```
Expect the rider object back with `"status":"AVAILABLE"`.

### 5c. Login → JWT
```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222"}' | jq -r .token)
echo "token: ${TOKEN:0:30}..."
```

### 5d. Place an order
```bash
OID=$(curl -s -X POST http://localhost:8080/api/v1/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'X-Customer-Id: 22222222-2222-2222-2222-222222222222' \
  -d '{
    "customer_id":"22222222-2222-2222-2222-222222222222",
    "restaurant_id":"33333333-3333-3333-3333-333333333333",
    "total_amount":249.50,
    "delivery_address":"Test address",
    "pickup_lat":28.6139, "pickup_lng":77.2090,
    "dropoff_lat":28.6200, "dropoff_lng":77.2150
  }' | jq -r .id)
echo "order_id: $OID"
```

### 5e. Verify the order was assigned
```bash
docker compose exec postgres psql -U postgres -d delivery_engine \
  -c "SELECT id, status, rider_id FROM orders WHERE id = '$OID';"
```

Expected: `rider_id` matches the `$RID` from step 5a. `status` stays `PLACED` until the rider is moved through the status machine (that's correct — `PLACED` here means "assigned, awaiting pickup").

---

## 6. Full flow test in Postman (with live WebSocket)

Use this flow when you want to **see the real-time events flow** end-to-end. Needs Postman desktop (for WebSocket support).

### 6a. Register rider + go online
Same as 5a–5b, but via Postman HTTP requests. Save `rider_id` from the response.

### 6b. Login → save JWT
`POST http://localhost:8080/api/v1/auth/login` with body `{"customer_id":"22222222-2222-2222-2222-222222222222"}`. Save the `token`.

### 6c. Open **Customer WebSocket first** — timing matters
Postman → **New → WebSocket Request**:
```
ws://localhost:8085/ws/order/PLACEHOLDER
```
Don't click Connect yet — you need the real order_id first.

### 6d. Place order
`POST http://localhost:8080/api/v1/orders` with the JWT from 6b.

Headers:
```
Content-Type: application/json
Authorization: Bearer <token>
X-Customer-Id: 22222222-2222-2222-2222-222222222222
```
Body:
```json
{
  "customer_id":"22222222-2222-2222-2222-222222222222",
  "restaurant_id":"33333333-3333-3333-3333-333333333333",
  "total_amount":249.50,
  "delivery_address":"Test address",
  "pickup_lat":28.6139, "pickup_lng":77.2090,
  "dropoff_lat":28.6200, "dropoff_lng":77.2150
}
```

**Immediately** paste the new `id` into the WebSocket URL from 6c and click **Connect**. Within ~1s you should see:
```json
{"type":"order_assigned","payload":{"order_id":"...","rider_id":"...","assigned_at":"..."}}
```

> Note: if you miss this because the Assign happened before you connected, that's fine — the WS has no replay. Just verify with the SQL in 5e.

### 6e. Rider WebSocket — push GPS
**New → WebSocket Request**:
```
ws://localhost:8083/ws/rider/<rider_id>
```
Connect. Paste **one at a time** and hit Send:
```json
{"type":"location_update","payload":{"lat":28.6145,"lng":77.2095}}
```
```json
{"type":"location_update","payload":{"lat":28.6165,"lng":77.2115}}
```
```json
{"type":"location_update","payload":{"lat":28.6190,"lng":77.2145}}
```

### 6f. Watch the customer WebSocket
Per GPS update you should see two messages on the order WS:
```json
{"type":"rider_location","payload":{"lat":...,"lng":...,"recorded_at":"..."}}
{"type":"eta_updated","payload":{"order_id":"...","eta_minutes":2,"computed_at":"..."}}
```

---

## 7. CANCELLED-path test (no rider available)

Useful for verifying the assignment-failure path (fixed in this session).

```bash
# Login
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222"}' | jq -r .token)

# Place order with NO AVAILABLE rider (after docker compose down -v, or after all riders are BUSY)
OID=$(curl -s -X POST http://localhost:8080/api/v1/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'X-Customer-Id: 22222222-2222-2222-2222-222222222222' \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222","restaurant_id":"33333333-3333-3333-3333-333333333333","total_amount":199,"delivery_address":"x","pickup_lat":28.6139,"pickup_lng":77.2090,"dropoff_lat":28.6200,"dropoff_lng":77.2150}' | jq -r .id)

sleep 2

# Verify: status should be CANCELLED
docker compose exec postgres psql -U postgres -d delivery_engine \
  -c "SELECT id, status, rider_id FROM orders WHERE id = '$OID';"
```

Expected: `status = CANCELLED`, `rider_id = null`. A `order.status.updated` event with `reason = "no rider available"` is published too.

---

## 8. Introspection & debugging queries

All the commands I reach for when something looks off. Paste-ready.

### Postgres

```powershell
# List tables
docker compose exec postgres psql -U postgres -d delivery_engine -c "\dt"

# Recent orders — status, rider, ETA
docker compose exec postgres psql -U postgres -d delivery_engine \
  -c "SELECT id, status, rider_id, eta_minutes, created_at FROM orders ORDER BY created_at DESC LIMIT 5;"

# All riders and their status
docker compose exec postgres psql -U postgres -d delivery_engine \
  -c "SELECT id, name, phone, status FROM riders;"

# Full event log for an order (chronological)
docker compose exec postgres psql -U postgres -d delivery_engine \
  -c "SELECT id, event_type, payload, created_at FROM order_events WHERE order_id = '<order_id>' ORDER BY id;"

# Most recent 10 events system-wide
docker compose exec postgres psql -U postgres -d delivery_engine \
  -c "SELECT id, order_id, event_type, created_at FROM order_events ORDER BY id DESC LIMIT 10;"

# Interactive psql session
docker compose exec postgres psql -U postgres -d delivery_engine
```

### Redis

```powershell
# All rider positions (sorted-set of GEOADD members + geohash scores)
docker compose exec redis redis-cli ZRANGE riders:geo 0 -1 WITHSCORES

# One rider's lat/lng
docker compose exec redis redis-cli GEOPOS riders:geo <rider_id>

# How many riders currently online
docker compose exec redis redis-cli ZCARD riders:geo

# Interactive Redis session
docker compose exec redis redis-cli
```

### Kafka

All Kafka commands need `sh -c "..."` because PowerShell mangles paths.

```powershell
# List all topics
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list"

# List consumer groups
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --list"

# Consumer group state (are they connected? stable?)
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --all-groups --state"

# Consumer group members + partitions (diagnoses 0-partition bug)
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --all-groups --members"

# Consumer group offsets + lag (are they falling behind?)
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --all-groups --offsets"

# Total message count per topic (offset:count)
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server localhost:9092 --topic order.placed"

# Read messages from a topic (for inspection)
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server localhost:9092 --topic order.placed --from-beginning --timeout-ms 5000 --max-messages 10"

# Same but for eta.updated / order.assigned / order.status.updated / rider.location.updated
```

### Jaeger (traces, `obs` profile)

```bash
# Is Jaeger up?
curl -s -o /dev/null -w "ui:%{http_code}\n" http://localhost:16686
curl -s -o /dev/null -w "otlp:%{http_code}\n" -X POST http://localhost:4318/v1/traces -d '{}'

# Which services have reported spans recently?
curl -s http://localhost:16686/api/services

# Pull the most recent 5 traces from order-service (operations + service count)
curl -s "http://localhost:16686/api/traces?service=order-service&limit=5&lookback=10m"

# Inspect the outbox trace-context column for a row (proves trace_id persisted)
docker compose exec postgres psql -U postgres -d delivery_engine \
  -c "SELECT id, topic, trace_context FROM outbox ORDER BY id DESC LIMIT 3;"
```

### Service logs

If running via `go run ./cmd/...` each service has its own terminal — scroll up in the relevant window.

If running via Docker compose:
```powershell
docker compose logs -f order-service
docker compose logs -f --tail 50 assignment-service
docker compose logs -f   # all services combined
```

---

## 9. Shutdown

```powershell
# Stop Go services: Ctrl+C in each of the six terminals
# Then stop infra:
docker compose down          # keeps data in Docker volumes
docker compose down -v       # wipes Postgres + Redis + Kafka data (re-run migrations next time)
```

---

## 10. Troubleshooting

### "password authentication failed for user postgres" during `migrate`
Native Windows Postgres is on 5432. Docker Postgres is on **5433**. Use `:5433` in the connection string.

### `kafka/opt/kafka/bin/...: not found` or PowerShell path weirdness
Always wrap Kafka CLI calls in `sh -c "..."`:
```powershell
docker compose exec kafka sh -c "/opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list"
```

### Order stays `PLACED` with `rider_id = null` (not CANCELLED, not assigned)
- **Most common cause**: assignment-service consumer joined Kafka before the topic existed, so it has 0 partitions. Check:
  ```powershell
  docker compose exec kafka sh -c "/opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group delivery-engine.assignment --members"
  ```
  If `#PARTITIONS` is `0`, restart `assignment-service` (Ctrl+C + `go run ./cmd/assignment-service`). The `EnsureTopics` call at boot should prevent this on fresh starts — this only tends to happen if something odd (e.g., manually deleted topics) happened.
- Is assignment-service actually running? Check its terminal.
- Is any rider `AVAILABLE`? `SELECT id, name, status FROM riders;`
- Is rider in Redis geo? `redis-cli ZRANGE riders:geo 0 -1 WITHSCORES`

### WebSocket connects but receives no `order_assigned`
Timing — `notification-service` only pushes to **currently-connected** clients (no replay). Connect the WS first (with a placeholder URL), then POST the order and swap the URL. Or just verify in DB.

### Rider is `BUSY` from a previous test → next order gets CANCELLED
Flip back to available:
```bash
curl -X PATCH http://localhost:8081/api/v1/riders/<rider_id>/status \
  -H 'Content-Type: application/json' \
  -d '{"status":"AVAILABLE","lat":28.6139,"lng":77.2090}'
```
Or register a fresh rider with a new phone number. Note: setting a BUSY rider back to AVAILABLE also re-adds them to Redis geo.

### `429 Too Many Requests` on `POST /orders`
Rate limiter: 5 orders/minute per `X-Customer-Id`. Wait 60s or use a different customer header.

### After `docker compose down -v`, things don't work
Volumes were wiped. Re-run migrations (step 3). Topics will be re-created at service startup automatically.

---

## 11. Auth — JWT details

All `POST/PATCH` on `/api/v1/orders*` require `Authorization: Bearer <token>`.

```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222"}'
```

Response: `{"token":"eyJhbGciOi..."}`

Attach: `Authorization: Bearer eyJhbGciOi...`

Secret + TTL configurable via `config/config.yaml` under `auth:` or env var `DE_AUTH_JWT_SECRET`.

---

## 12. Metrics & observability

Each HTTP service exposes `/metrics` for Prometheus:
- http://localhost:8080/metrics
- http://localhost:8081/metrics
- http://localhost:8083/metrics
- http://localhost:8085/metrics

Counters include `http_requests_total`, `http_request_duration_seconds`, and `business_events_total{event="order.placed"|"order.assigned"|...}`.

With the `obs` profile running (`docker compose --profile obs up -d`), Prometheus is at http://localhost:9090:
```
rate(http_requests_total[1m])
business_events_total
histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, service))
```

Grafana at http://localhost:3000 (admin/admin or anonymous) — pre-provisioned `Rush Delivery — Overview` dashboard.

### Distributed tracing (Jaeger)

The `obs` profile also brings up Jaeger all-in-one. Services export OTLP HTTP to `:4318`; the UI serves at http://localhost:16686.

```powershell
docker compose --profile obs up -d jaeger
```

Verify services are registered (after at least one request has landed):
```bash
curl -s http://localhost:16686/api/services
# {"data":["order-service","rider-service","location-service","notification-service","assignment-service","eta-service",...]}
```

**The trace to look for:** place an order, then in the Jaeger UI pick `order-service` from the dropdown and click **Find Traces**. The `POST /orders` trace shows **5+ spans across 3 services** (order → assignment → notification), with a gap at the outbox hop that corresponds to the relay's 500 ms polling interval — an honest visualization of the async boundary.

Turn tracing off for a given run via env var:
```bash
DE_TELEMETRY_ENABLED=false go run ./cmd/order-service
```
Services still install the W3C propagator (so they forward trace headers uniformly) but stop exporting spans. Configure the OTLP target via `DE_TELEMETRY_OTLP_ENDPOINT` (default `localhost:4318`; in the compose `app` profile it's `jaeger:4318`).

---

## 13. Tests

Unit tests (fast, no infra):
```powershell
go test ./...
```

Integration test (spins up Postgres + Redis + Kafka via testcontainers — needs Docker):
```powershell
go test -tags=integration -v ./test/integration/...
```

---

## 14. CI / Kubernetes

**CI** (`.github/workflows/ci.yml`) runs on every push/PR:
- `go test -race`, `go vet`, build all 6 binaries
- Docker smoke-build one service image via buildx + GHA cache

**K8s**: raw YAML in `k8s/` for the app layer (not data plane):
```bash
kubectl apply -f k8s/
```

See `DEPLOY.md` for the full Oracle Cloud + Caddy HTTPS setup.

---

## 15. Quick reference — endpoints

| Service              | Endpoint                                      |
|----------------------|-----------------------------------------------|
| Auth login           | `POST http://localhost:8080/api/v1/auth/login`|
| Orders REST          | `http://localhost:8080/api/v1/orders`         |
| Riders REST          | `http://localhost:8081/api/v1/riders`         |
| Rider GPS WebSocket  | `ws://localhost:8083/ws/rider/{rider_id}`     |
| Customer WebSocket   | `ws://localhost:8085/ws/order/{order_id}`     |
| Postgres             | `localhost:5433` (postgres/postgres/delivery_engine) |
| Redis                | `localhost:6379`                              |
| Kafka                | `localhost:9092`                              |
| Prometheus (obs)     | `http://localhost:9090`                       |
| Grafana (obs)        | `http://localhost:3000`                       |
| Jaeger UI (obs)      | `http://localhost:16686`                      |
| OTLP HTTP receiver   | `http://localhost:4318` (services export here)|
