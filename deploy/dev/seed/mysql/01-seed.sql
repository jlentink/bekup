-- Sample data for the dev MySQL 8.4 and MariaDB 11.4 targets.
-- Loaded once by /docker-entrypoint-initdb.d into the database named by
-- MYSQL_DATABASE / MARIADB_DATABASE (`shop`). Development only.

CREATE TABLE customers (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    email      VARCHAR(255)    NOT NULL UNIQUE,
    full_name  VARCHAR(255)    NOT NULL,
    created_at TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE products (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    sku         VARCHAR(32)     NOT NULL UNIQUE,
    name        VARCHAR(255)    NOT NULL,
    price_cents INT UNSIGNED    NOT NULL,
    image       BLOB            NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE orders (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    customer_id BIGINT UNSIGNED NOT NULL,
    product_id  BIGINT UNSIGNED NOT NULL,
    quantity    INT UNSIGNED    NOT NULL,
    ordered_at  TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY orders_customer_idx (customer_id),
    CONSTRAINT orders_customer_fk FOREIGN KEY (customer_id) REFERENCES customers (id),
    CONSTRAINT orders_product_fk FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE VIEW order_totals AS
SELECT c.email, SUM(o.quantity * p.price_cents) AS total_cents
FROM orders o
JOIN customers c ON c.id = o.customer_id
JOIN products p ON p.id = o.product_id
GROUP BY c.email;

INSERT INTO customers (email, full_name) VALUES
    ('ada@example.com', 'Ada Lovelace'),
    ('grace@example.com', 'Grace Hopper'),
    ('edsger@example.com', 'Edsger Dijkstra'),
    ('zoë@example.com', 'Zoë Ünicode-Tëst');

-- The BLOB exercises --hex-blob.
INSERT INTO products (sku, name, price_cents, image) VALUES
    ('KB-001', 'Mechanical keyboard', 12900, UNHEX('89504E470D0A1A0A')),
    ('MS-002', 'Trackball', 7900, NULL),
    ('MN-003', '27" monitor', 32900, UNHEX('FFD8FFE0'));

INSERT INTO orders (customer_id, product_id, quantity) VALUES
    (1, 1, 1), (1, 2, 2), (2, 3, 1), (3, 1, 3), (4, 2, 1);

-- A second database so "dump all" produces more than one file.
CREATE DATABASE IF NOT EXISTS analytics DEFAULT CHARACTER SET utf8mb4;
