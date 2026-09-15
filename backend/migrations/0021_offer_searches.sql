-- Persisted offer-search pipeline state, mirroring bill_scans: a row exists
-- from creation until it is deleted manually (or TTL-swept when failed).
-- request_json snapshots the cart lines sent to the model so the result stays
-- renderable after products are edited or merged; result_json holds the
-- normalized offers.
CREATE TABLE offer_searches (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    token        TEXT    NOT NULL UNIQUE,
    status       TEXT    NOT NULL DEFAULT 'searching'
                 CHECK (status IN ('searching', 'done', 'failed')),
    provider_id  TEXT    NOT NULL DEFAULT '',
    request_json TEXT    NOT NULL DEFAULT '',
    result_json  TEXT    NOT NULL DEFAULT '',
    error        TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX idx_offer_searches_status  ON offer_searches (status);
CREATE INDEX idx_offer_searches_updated ON offer_searches (updated_at);