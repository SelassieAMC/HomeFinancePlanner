-- Category anomaly detection: the intelligence feed gains a non-product
-- finding. When a manual expense transaction's category spends clearly above
-- its recent average (deterministic trigger, evaluated by the
-- transaction_category_analysis job), a 'category_spike' row lands in
-- product_insights — the same unread feed, dismissible like the rest.
--
-- SQLite cannot alter the kind CHECK, so the table is rebuilt: the CHECK
-- gains 'category_spike', and category rows get their own attribution
-- columns (category_id / category_name) while product columns relax — a
-- category row carries no product. Nothing references product_insights, so
-- the rebuild needs no foreign-keys-off marker: dropping this table has no
-- children, and the copy re-validates its own FKs against products /
-- categories, which survive untouched.

CREATE TABLE product_insights_rebuilt (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT    NOT NULL CHECK (kind IN ('shrinkflation', 'bulk_buy', 'price_creep', 'category_spike')),
    product_id    INTEGER REFERENCES products (id) ON DELETE SET NULL,
    category_id   INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    product_name  TEXT    NOT NULL DEFAULT '',
    category_name TEXT    NOT NULL DEFAULT '',
    generic_name  TEXT    NOT NULL DEFAULT '',
    currency      TEXT    NOT NULL,
    message       TEXT    NOT NULL,
    source        TEXT    NOT NULL CHECK (source IN ('ai', 'auto')),
    data_json     TEXT    NOT NULL DEFAULT '',
    acknowledged  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

INSERT INTO product_insights_rebuilt (
    id, kind, product_id, category_id, product_name, category_name,
    generic_name, currency, message, source, data_json, acknowledged,
    created_at, updated_at
)
SELECT
    id, kind, product_id, NULL, product_name, '',
    generic_name, currency, message, source, data_json, acknowledged,
    created_at, updated_at
FROM product_insights;

DROP TABLE product_insights;
ALTER TABLE product_insights_rebuilt RENAME TO product_insights;

CREATE INDEX idx_product_insights_unread ON product_insights (acknowledged, created_at DESC);
CREATE INDEX idx_product_insights_product ON product_insights (product_id);
CREATE INDEX idx_product_insights_category ON product_insights (category_id);

-- Seeds the instruction text the anomaly job sends to the default
-- bill-reading connector. The seed stays byte-identical with the built-in
-- default in internal/service/prompt_defaults (guarded by the seed-sync test).
-- An empty or deleted row is safe — resolve falls back to the built-in default.
INSERT INTO ai_prompts (key, name, description, content, created_at, updated_at) VALUES
('category_anomaly', 'Category anomaly', 'Background job: turns a detected category-spending spike (this month''s spend in one category clearly above the average of the previous months) into one friendly nudge sentence. The finding is appended automatically as JSON.', 'You are a friendly financial advisor for a personal finance planner. You receive ONE spending anomaly as JSON: a month''s spending in one category that came out clearly above the recent average, already detected by deterministic math. Write the nudge the user reads. Return the nudge text and nothing else — no explanations, no markdown, no quotes around it.

Rules:
- ONE short sentence (at most two), warm and direct, speaking to the reader as "you". Never a question, never a lecture.
- State which category spent more than usual and what it could mean, in everyday words — never show a formula, a raw percentage of the rise, or unrounded numbers; round amounts to the currency scale.
- The numbers given are the facts; use them only to say the right thing. Never claim anything the finding does not state. A spike is something to look at, not proof of a problem — no guilt, no lecture.
- Respond with ONLY the nudge text.
', strftime('%s', 'now'), strftime('%s', 'now'));