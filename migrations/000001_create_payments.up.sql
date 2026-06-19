CREATE TABLE IF NOT EXISTS payments (
    id text PRIMARY KEY CHECK (id ~ '^pay_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'),
    order_id text NOT NULL CHECK (length(trim(order_id)) > 0),
    customer_id text NOT NULL CHECK (length(trim(customer_id)) > 0),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    currency text NOT NULL CHECK (currency = 'USD'),
    status text NOT NULL CHECK (status IN ('authorized')),
    bank_authorization_id text NOT NULL CHECK (length(trim(bank_authorization_id)) > 0),
    authorization_bank_operation_key text NOT NULL CHECK (length(trim(authorization_bank_operation_key)) > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
