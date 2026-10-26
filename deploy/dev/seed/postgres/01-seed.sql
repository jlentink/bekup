-- Sample data for the dev Postgres targets (pg15, pg15-tunneled).
-- Loaded once by /docker-entrypoint-initdb.d into the database `shop`.
-- Development only: gives backups and restores something to verify.

CREATE TABLE customers (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email       text        NOT NULL UNIQUE,
    full_name   text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE products (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku         text           NOT NULL UNIQUE,
    name        text           NOT NULL,
    price_cents integer        NOT NULL CHECK (price_cents >= 0),
    attributes  jsonb          NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE orders (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint      NOT NULL REFERENCES customers(id),
    product_id  bigint      NOT NULL REFERENCES products(id),
    quantity    integer     NOT NULL CHECK (quantity > 0),
    ordered_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX orders_customer_idx ON orders (customer_id);

CREATE VIEW order_totals AS
SELECT c.email, sum(o.quantity * p.price_cents) AS total_cents
FROM orders o
JOIN customers c ON c.id = o.customer_id
JOIN products p ON p.id = o.product_id
GROUP BY c.email;

INSERT INTO customers (email, full_name) VALUES
    ('ada@example.com', 'Ada Lovelace'),
    ('grace@example.com', 'Grace Hopper'),
    ('edsger@example.com', 'Edsger Dijkstra'),
    ('zoë@example.com', 'Zoë Ünicode-Tëst');

INSERT INTO products (sku, name, price_cents, attributes) VALUES
    ('KB-001', 'Mechanical keyboard', 12900, '{"layout": "ANSI", "switch": "brown"}'),
    ('MS-002', 'Trackball', 7900, '{"buttons": 5}'),
    ('MN-003', '27" monitor', 32900, '{"resolution": "2560x1440"}');

INSERT INTO orders (customer_id, product_id, quantity) VALUES
    (1, 1, 1), (1, 2, 2), (2, 3, 1), (3, 1, 3), (4, 2, 1);

-- A second database so "dump all" produces more than one file.
CREATE DATABASE analytics;
