CREATE TABLE IF NOT EXISTS orders (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id       UUID          NOT NULL,
    rider_id          UUID          REFERENCES riders(id),
    restaurant_id     UUID          NOT NULL,
    status            VARCHAR(20)   NOT NULL DEFAULT 'PLACED',
    total_amount      NUMERIC(10,2) NOT NULL,
    delivery_address  TEXT          NOT NULL,
    pickup_lat        DOUBLE PRECISION NOT NULL,
    pickup_lng        DOUBLE PRECISION NOT NULL,
    dropoff_lat       DOUBLE PRECISION NOT NULL,
    dropoff_lng       DOUBLE PRECISION NOT NULL,
    eta_minutes       INT,
    created_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    CONSTRAINT orders_status_chk CHECK (
        status IN ('PLACED','ACCEPTED','PREPARING','PICKED_UP','DELIVERED','CANCELLED')
    )
);

CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders (customer_id);
CREATE INDEX IF NOT EXISTS idx_orders_rider    ON orders (rider_id);
CREATE INDEX IF NOT EXISTS idx_orders_status   ON orders (status);
