CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS riders (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(100) NOT NULL,
    phone      VARCHAR(15)  UNIQUE NOT NULL,
    status     VARCHAR(20)  NOT NULL DEFAULT 'OFFLINE',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT riders_status_chk CHECK (status IN ('OFFLINE', 'AVAILABLE', 'BUSY'))
);

CREATE INDEX IF NOT EXISTS idx_riders_status ON riders (status);
