# Tech choices & why

Every line below is a decision that had real alternatives. Each entry answers:
**what we chose**, **what we rejected**, and **the trade-off we accepted**.
Boring defaults (e.g. "we use JSON") are not listed.

---

## Architecture

### Microservices over a modular monolith
**Chose:** 6 services talking over Kafka + REST/WS.
**Rejected:** single binary with internal packages.
**Why:** the domain has natural async boundaries (assignment, ETA, notification
are background workloads, not HTTP paths). Splitting them makes
independent-scaling and failure-isolation concrete rather than theoretical.
**Trade-off:** harder debugging without distributed tracing — which is exactly
why OTel is a Phase-1 concern here, not a Phase-3 one.

### Event-driven (Kafka) over sync HTTP between services
**Chose:** every state change is a Kafka event.
**Rejected:** REST calls between order-service → assignment-service etc.
**Why:** if assignment-service is down, sync HTTP fails the whole order flow.
With Kafka, the event sits durably and is processed when the consumer comes
back. Also unlocks fan-out (notification-service listens to everything without
order-service knowing it exists).
**Trade-off:** eventual consistency. The customer's `POST /orders` returns
`201` before a rider has been assigned; assignment happens asynchronously.
Customer gets the rider via WebSocket, not HTTP response.

### Transactional outbox over "write, then publish"
**Chose:** orders + outbox row commit in one DB transaction; a relay polls
and forwards to Kafka.
**Rejected:** direct `producer.Publish()` after `tx.Commit()`.
**Why:** a crash/timeout between commit and publish silently loses events —
the order exists but assignment never fires. Proven in testing: killed Kafka
mid-flight, `POST /orders` still returned 201 in 86 ms, event queued in
Postgres, drained on Kafka recovery.
**Trade-off:** 500 ms added latency between DB commit and Kafka publish
(the relay poll interval). Visible as a gap in the distributed trace.

### Partition Kafka by `order_id`
**Chose:** every order-scoped topic uses `order_id` as the partition key.
**Rejected:** round-robin or hash(customer_id).
**Why:** all events for one order land on the same partition, so consumers
see them in causal order (`placed` → `assigned` → `picked_up` → `delivered`).
Without this, the state machine can race.
**Trade-off:** hot-key risk if one customer places many orders. For this
domain's cardinality it's irrelevant; for real production you'd monitor it.

### Race-safe rider claim via `UPDATE ... WHERE status='AVAILABLE'`
**Chose:** atomic claim — the UPDATE only succeeds once per rider.
**Rejected:** `SELECT ... FOR UPDATE` + in-process logic; optimistic version
columns.
**Why:** simpler and faster. Two orders racing for the same rider → one UPDATE
wins, the other sees 0 rows affected and falls through to the next candidate.
Zero DB locks held across the app logic.
**Trade-off:** consumer must be retried on the "all candidates lost" path —
which is exactly the `markCancelled` fallback.

---

## Infrastructure

### PostgreSQL for durable state
**Chose:** Postgres 15.
**Rejected:** MySQL, DynamoDB, MongoDB.
**Why:** JSONB for the audit log, partial indexes for the outbox
(`WHERE sent_at IS NULL`), transactional guarantees for the outbox pattern,
and CTEs for future read-model queries. Nothing else matches all four.
**Trade-off:** no built-in horizontal partitioning. If we outgrow one primary
we'd go read-replicas first, Citus later.

### Redis for rider geo + rate limit
**Chose:** Redis 7 with `GEOADD`/`GEOSEARCH`.
**Rejected:** PostGIS, Elasticsearch geo, in-memory with a kd-tree.
**Why:** geo sorted-sets are O(log N) for nearest-neighbor; writes are
~10x cheaper than an indexed Postgres UPDATE; position data is
ephemeral so durability doesn't matter. Same instance handles the sliding-window
rate limiter — no new dependency.
**Trade-off:** single-instance Redis is a bottleneck at massive scale.
Redis Cluster covers it when needed.

### Kafka in KRaft mode over ZooKeeper-backed Kafka
**Chose:** Apache Kafka 3.8 KRaft.
**Rejected:** 3.x with ZooKeeper, NATS, RabbitMQ.
**Why:** KRaft removed ZooKeeper as a dependency — one less moving part in
compose and k8s. NATS is simpler but weaker on replay/partitioning
guarantees. RabbitMQ is push-based; we want consumer-driven pull +
offset management for the outbox-replay semantics.
**Trade-off:** KRaft is newer — bigger/older operational knowledge base around
ZK. For a new project the simpler topology wins.

### Jaeger for trace UI over Tempo or Zipkin
**Chose:** `jaegertracing/all-in-one`.
**Rejected:** Grafana Tempo, Zipkin, cloud SaaS.
**Why:** single-container dev setup, native OTLP HTTP receiver, intuitive
waterfall UI. Tempo is better at scale but needs Grafana + object storage to
be useful.
**Trade-off:** all-in-one is in-memory; traces vanish on restart. Fine for
dev/demo; production would use Tempo/Jaeger-cassandra/cloud.

### Prometheus + Grafana over hosted APM
**Chose:** self-hosted on the `obs` compose profile.
**Rejected:** Datadog, New Relic, Honeycomb.
**Why:** zero sign-up friction, pre-provisioned dashboard in the repo, every
reviewer can spin up the full observability stack with one `--profile obs`.
**Trade-off:** no alerting, no retention, no anomaly detection — we rely on
the reviewer running it locally.

### Docker multi-stage distroless over Alpine-based images
**Chose:** `gcr.io/distroless/static-debian12` as the runtime base.
**Rejected:** alpine, scratch, full Debian.
**Why:** ~24 MB images, no shell/package manager = much smaller CVE surface,
glibc-compatible (unlike Alpine's musl which breaks some Go cgo libs).
Scratch is smaller still but provides no CA certs.
**Trade-off:** can't `docker exec` for debugging — mitigated by good logging.

### One Dockerfile, `SERVICE` build arg picks the binary
**Chose:** single recipe, 6 tags.
**Rejected:** per-service Dockerfiles.
**Why:** they'd be 99% identical and drift over time. The build arg is a
5-minute learning curve; divergence is a perpetual maintenance cost.
**Trade-off:** slightly less CI parallelism on image layer caching.

---

## Go ecosystem choices

### `chi` over gin / echo / fiber / stdlib
**Chose:** `go-chi/chi/v5`.
**Rejected:** gin (DSL-heavy, weak `context` support), echo (similar), fiber
(not `net/http` compatible; can't use `otelhttp` directly), stdlib-only
(no route parameters without muxing boilerplate).
**Why:** `chi` is stdlib-compatible, so every `net/http`-ecosystem middleware
(otelhttp, Prometheus, etc.) works unmodified.
**Trade-off:** none worth mentioning.

### `jackc/pgx/v5` over `database/sql` + `lib/pq`
**Chose:** pgx native driver.
**Rejected:** `database/sql` + `lib/pq` or pgx via the database/sql wrapper.
**Why:** native pgx is ~2x faster on common paths, supports COPY and LISTEN/NOTIFY,
has proper JSONB scanning without manual `[]byte` dances, and the pgx-specific
`pgxpool` is a better connection manager than `sql.DB` for microservices.
**Trade-off:** can't trivially swap to another SQL driver. Not a real concern.

### `segmentio/kafka-go` over Confluent `confluent-kafka-go`
**Chose:** `segmentio/kafka-go`.
**Rejected:** `confluent-kafka-go` (the official Go binding).
**Why:** pure Go, no cgo, no librdkafka C dependency — cross-compiles cleanly
to any arch, distroless-compatible, no image-size bloat.
**Trade-off:** a bit slower and fewer advanced Kafka features (no transactions
API, simpler consumer-group semantics). None of them mattered for this project.

### `gorilla/websocket` over `nhooyr/websocket`
**Chose:** gorilla.
**Rejected:** nhooyr (newer, lighter).
**Why:** gorilla is battle-tested, everyone knows the `Upgrader` API, and the
performance delta is negligible for our message volume.
**Trade-off:** gorilla is in maintenance mode. Swap is straightforward if
needed.

### `zerolog` over `slog` or `zap`
**Chose:** `zerolog`.
**Rejected:** `log/slog` (stdlib, 1.21+), `uber-go/zap`.
**Why:** `zerolog` is allocation-free on the hot path and has cleaner JSON
output than `zap` out of the box. Written before `slog` stabilized.
**Trade-off:** a more recent project would probably use `slog` for stdlib
alignment. Not worth rewriting.

### `viper` over environment-only config
**Chose:** viper — YAML defaults with `DE_*` env overrides.
**Rejected:** raw `os.Getenv` everywhere, `envconfig`, koanf.
**Why:** sane defaults live in a file (not a README), env vars override at
deploy time. 12-factor-compliant without the ceremony.
**Trade-off:** viper is large. For a single-config-file project we pay the
dependency size for the ergonomics.

### `testcontainers-go` for integration tests
**Chose:** real Postgres + Redis + Kafka spun up per test run.
**Rejected:** mocks, `dockertest`, a shared always-running test stack.
**Why:** mocks drift from real behavior (we got burned specifically on
`FOR UPDATE SKIP LOCKED` which mocks don't emulate). testcontainers gives
full fidelity with ~30 s startup cost.
**Trade-off:** integration tests are build-tagged (`-tags=integration`) and
not run by default — too slow for the inner loop, run them in CI instead.

---

## Observability choices

### OpenTelemetry over a vendor SDK
**Chose:** OTel SDK + OTLP HTTP exporter.
**Rejected:** Jaeger client directly, DataDog's Go agent, Honeycomb's beelines.
**Why:** vendor-neutral — swap Jaeger for Tempo or a SaaS by changing one
config line (`DE_TELEMETRY_OTLP_ENDPOINT`). Also the de-facto standard for
2024+; vendor SDKs are in maintenance mode.
**Trade-off:** extra layer of abstraction; slightly more verbose than vendor
SDKs.

### Trace context in the outbox column (not just in-memory)
**Chose:** `outbox.trace_context JSONB` persisted with each row.
**Rejected:** only span the HTTP path; let the relay start fresh traces.
**Why:** without this, the trace breaks at the DB commit — reviewer sees two
disconnected traces for one logical request. Persisting the W3C
`traceparent` map into the row and re-extracting it in the relay keeps the
entire HTTP → Kafka → consumer flow as one trace.
**Trade-off:** a few extra bytes per outbox row, and small extract/inject
overhead. Tiny relative to the payload itself.

### Prometheus pull over push
**Chose:** scrape-based with `/metrics` endpoints.
**Rejected:** push-gateway, statsd-style agents.
**Why:** stateless services are scrape-friendly; Prometheus handles discovery
and retention; zero extra infrastructure in the critical path.
**Trade-off:** short-lived batch jobs can't meaningfully be scraped (they'd
exit before being polled). Not applicable here — our "jobs" are long-running
consumers.

---

## What we deliberately didn't use

- **ORM** — `sqlc` or raw pgx is more honest about the SQL being written, and
  migrations already own the schema.
- **gRPC** — between services would add a compile-time dep chain that
  undermines the "independently deployable" point. Kafka + JSON is enough.
- **Service mesh** — Istio/Linkerd would bury the interesting parts
  (outbox/tracing/rate-limiting) behind infrastructure. For a portfolio
  project the application-layer code _is_ the point.
- **Protobuf** — would be the right answer at 1000+ msg/sec; at our volume,
  JSON is debuggable and `jq`-able.
