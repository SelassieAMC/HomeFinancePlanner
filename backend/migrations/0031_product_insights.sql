-- Product insights: AI-written "nudges" the deferred-intelligence job saves
-- after every accepted purchase whose price-per-unit (unit_price_cents /
-- unit_value) shows a shrinkflation shrink, a cheaper bulk size, or a steady
-- price creep. Rows are the kept record (like done offer searches) with an
-- acknowledged flag for the dashboard's unread list.
CREATE TABLE product_insights (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    kind           TEXT    NOT NULL CHECK (kind IN ('shrinkflation', 'bulk_buy', 'price_creep')),
    product_id     INTEGER REFERENCES products (id) ON DELETE SET NULL,
    product_name   TEXT    NOT NULL,
    generic_name   TEXT    NOT NULL DEFAULT '',
    currency       TEXT    NOT NULL,
    message        TEXT    NOT NULL,
    source         TEXT    NOT NULL CHECK (source IN ('ai', 'auto')),
    data_json      TEXT    NOT NULL DEFAULT '',
    acknowledged   INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE INDEX idx_product_insights_unread ON product_insights (acknowledged, created_at DESC);
CREATE INDEX idx_product_insights_product ON product_insights (product_id);

-- Seeds the instruction text the analysis job sends to the default
-- bill-reading connector. The seed stays byte-identical with the built-in
-- default in internal/service/prompt_defaults (guarded by the seed-sync test).
-- An empty or deleted row is safe — resolve falls back to the built-in default.
INSERT INTO ai_prompts (key, name, description, content, created_at, updated_at) VALUES
('product_insights', 'Product insights', 'Background job: turns the price-per-unit findings of every accepted purchase (shrinkflation, cheaper bulk sizes, price creep) into one friendly nudge sentence each. The findings are appended automatically as JSON.', 'You are a friendly shopping advisor for a personal finance planner. You receive ONE purchase insight as JSON: a price-per-unit finding about a product the user just bought again, already detected by deterministic math. Write the nudge the user reads. Return the nudge text and nothing else — no explanations, no markdown, no quotes around it.

Rules:
- ONE short sentence (at most two), warm and direct, speaking to the reader as "you". Never a question, never a lecture.
- State what changed and what it means, in everyday words — never show a formula, a percentage of prices, or raw numbers like cents per unit; round prices to the currency scale, sizes to the printed unit (e.g. "500g", "1.5l").
- shrinkflation: the pack got smaller while the price stayed — say the size before and now, and that the user now pays more for less.
- bulk_buy: a bigger pack of the same product costs notably less per unit than the size the user keeps buying — say both sizes and which one the user should try.
- price_creep: the product''s price per unit has kept rising purchase after purchase — say it rose again, name where the user last bought it, and suggest trying another brand or store.
- The numbers given are the facts; use them only to say the right thing. Never claim anything the finding does not state.
- Respond with ONLY the nudge text.
', strftime('%s', 'now'), strftime('%s', 'now'));