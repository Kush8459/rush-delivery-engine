-- Store the OTel trace context alongside the event payload so the relay can
-- re-inject it when publishing. Without this, the trace breaks at the DB commit
-- and downstream Kafka consumers see a disconnected trace.
ALTER TABLE outbox ADD COLUMN trace_context JSONB;
