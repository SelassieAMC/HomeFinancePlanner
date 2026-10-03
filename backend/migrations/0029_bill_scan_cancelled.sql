-- "cancelled" scan status: the user aborted an in-progress analysis (a
-- backend cancel stops the extraction; the kept photos are re-uploadable and
-- the request stays deletable or re-readable).
--
-- SQLite cannot alter a CHECK constraint, so the table is rebuilt in place,
-- keeping every row and id. pragma: foreign_keys=off — bill_scans is the
-- parent of bill_scan_files, and with foreign keys enforced the rebuild's DROP
-- TABLE of the old parent would be rejected by the child rows it still holds
-- (the runner honors the marker above; see internal/repository/db.go).
CREATE TABLE bill_scans_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token       TEXT    NOT NULL UNIQUE,
    status      TEXT    NOT NULL DEFAULT 'analyzing'
                CHECK (status IN ('analyzing','done','failed','cancelled')),
    image_path  TEXT    NOT NULL,
    mime_type   TEXT    NOT NULL DEFAULT '',
    provider_id TEXT    NOT NULL DEFAULT '',
    draft_json  TEXT    NOT NULL DEFAULT '',
    error       TEXT    NOT NULL DEFAULT '',
    -- added by 0011 (content-hash dedup); the columns are rebuilt verbatim
    file_hash   TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

INSERT INTO bill_scans_new (id, token, status, image_path, mime_type, provider_id, draft_json, error, file_hash, created_at, updated_at)
SELECT id, token, status, image_path, mime_type, provider_id, draft_json, error, file_hash, created_at, updated_at
FROM bill_scans;

DROP TABLE bill_scans;
ALTER TABLE bill_scans_new RENAME TO bill_scans;

CREATE INDEX idx_bill_scans_status  ON bill_scans (status);
CREATE INDEX idx_bill_scans_updated ON bill_scans (updated_at);
CREATE INDEX idx_bill_scans_hash    ON bill_scans (file_hash);