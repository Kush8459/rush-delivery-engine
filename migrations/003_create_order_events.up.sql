-- Append-only audit log. No UPDATE or DELETE should ever run on this table.
CREATE TABLE IF NOT EXISTS order_events (
    id         BIGSERIAL    PRIMARY KEY,
    order_id   UUID         NOT NULL,
    event_type VARCHAR(50)  NOT NULL,
    payload    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_events_order ON order_events (order_id, id);
