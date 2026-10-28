-- Shippers who use the dashboard. The password is an argon2id PHC string, never the password itself.
CREATE TABLE customers (
    id            text        PRIMARY KEY,
    name          text        NOT NULL,
    email         text        NOT NULL,
    phone         text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL
);
CREATE UNIQUE INDEX customers_email_key ON customers (lower(email));
