-- Auvyn Apies v2 schema. Timestamps are RFC3339Nano UTC TEXT for sortability.

CREATE TABLE IF NOT EXISTS clients (
    client_id                 TEXT PRIMARY KEY,
    client_name               TEXT NOT NULL,
    status                    TEXT NOT NULL DEFAULT 'ACTIVE',
    registered_at             TEXT NOT NULL,
    secret_hash               TEXT NOT NULL,
    pin_hash                  TEXT NOT NULL,
    keys_hash                 TEXT NOT NULL,
    secret_callback           TEXT NOT NULL,
    secret_callback_rotated_at TEXT NOT NULL,
    deleted_at                TEXT
);

CREATE TABLE IF NOT EXISTS webhooks (
    webhook_id  TEXT PRIMARY KEY,
    client_id   TEXT NOT NULL REFERENCES clients(client_id),
    url         TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    deleted_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_webhooks_client ON webhooks(client_id);

CREATE TABLE IF NOT EXISTS webhook_events (
    webhook_id TEXT NOT NULL REFERENCES webhooks(webhook_id) ON DELETE CASCADE,
    event      TEXT NOT NULL,
    PRIMARY KEY (webhook_id, event)
);

CREATE TABLE IF NOT EXISTS transactions (
    ref_id         TEXT PRIMARY KEY,
    client_id      TEXT,
    product        TEXT NOT NULL,
    dest           TEXT NOT NULL,
    qty            INTEGER,
    nominal        INTEGER,
    category       TEXT NOT NULL DEFAULT 'pending',
    provider_state TEXT,
    provider_code  TEXT,
    provider_trxid TEXT,
    sn             TEXT,
    request_raw    TEXT,
    report_raw     TEXT,
    meta           TEXT,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    final_at       TEXT
);
CREATE INDEX IF NOT EXISTS idx_transactions_created ON transactions(created_at);
CREATE INDEX IF NOT EXISTS idx_transactions_client ON transactions(client_id);

CREATE TABLE IF NOT EXISTS provider_reports (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ref_id       TEXT,
    accepted     INTEGER NOT NULL,
    reason       TEXT NOT NULL,
    category     TEXT,
    remote_ip    TEXT,
    method       TEXT,
    query        TEXT,
    body         TEXT,
    raw_message  TEXT,
    received_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_provider_reports_ref ON provider_reports(ref_id);

-- events is the outbox source of truth. dedupe_key makes delivery idempotent.
CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ref_id     TEXT,
    kind       TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    payload    TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS deliveries (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        INTEGER NOT NULL REFERENCES events(id),
    target_kind     TEXT NOT NULL,
    target_name     TEXT NOT NULL,
    client_id       TEXT,
    webhook_id      TEXT,
    url             TEXT NOT NULL,
    event_name      TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error      TEXT,
    response_status INTEGER,
    created_at      TEXT NOT NULL,
    delivered_at    TEXT
);
CREATE INDEX IF NOT EXISTS idx_deliveries_due ON deliveries(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_event ON deliveries(event_id);
