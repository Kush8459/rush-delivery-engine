-- Transactional outbox. Writers INSERT rows in the same tx as their business
-- write (e.g. orders row + outbox row commit atomically). A background relay
-- polls unsent rows, publishes to Kafka, and marks sent_at.
--
-- Partial index on unsent rows keeps the relay query O(unsent) regardless of
-- total table size.
CREATE TABLE outbox (
    id            BIGSERIAL    PRIMARY KEY,
    aggregate_id  UUID         NOT NULL,
    topic         VARCHAR(100) NOT NULL,
    partition_key VARCHAR(100) NOT NULL,
    payload       JSONB        NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    sent_at       TIMESTAMPTZ
);

CREATE INDEX idx_outbox_unsent ON outbox (id) WHERE sent_at IS NULL;
