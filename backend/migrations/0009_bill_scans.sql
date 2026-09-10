-- Persisted scan pipeline state. A bill_scans row exists from upload until
-- confirm (row deleted, receipt kept as the bill's image) or discard (row and
-- receipt deleted). draft_json holds the extraction result while the user
-- reviews it; status drives the async worker and the "analysis in progress" UI.
CREATE TABLE bill_scans (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token       TEXT    NOT NULL UNIQUE,
    status      TEXT    NOT NULL DEFAULT 'analyzing'
                CHECK (status IN ('analyzing','done','failed')),
    image_path  TEXT    NOT NULL,
    mime_type   TEXT    NOT NULL DEFAULT '',
    provider_id TEXT    NOT NULL DEFAULT '',
    draft_json  TEXT    NOT NULL DEFAULT '',
    error       TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE INDEX idx_bill_scans_status  ON bill_scans (status);
CREATE INDEX idx_bill_scans_updated ON bill_scans (updated_at);