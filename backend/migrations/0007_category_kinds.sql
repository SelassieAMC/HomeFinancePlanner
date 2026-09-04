-- Budgets use GENERAL expense categories (groceries, home, leisure, …),
-- distinct from the product storage taxonomy used to classify bill items.
-- kind distinguishes the two families: 'product' (bill item classification)
-- vs 'expense' (budget/transaction groupings).
ALTER TABLE categories ADD COLUMN kind TEXT NOT NULL DEFAULT 'expense';

UPDATE categories SET kind = 'product' WHERE is_system = 1;

INSERT INTO categories (name, section, icon, description, is_system, kind, created_at) VALUES
    ('Groceries',      'General', '🛒', 'Supermarket, food and household shopping',    1, 'expense', strftime ('%s', 'now')),
    ('Home Expenses',  'General', '🏠', 'Rent, repairs, furniture, utilities',         1, 'expense', strftime ('%s', 'now')),
    ('Leisure',        'General', '🎮', 'Entertainment, hobbies, going out',           1, 'expense', strftime ('%s', 'now')),
    ('Vacations',      'General', '✈️', 'Trips, hotels, travel',                       1, 'expense', strftime ('%s', 'now')),
    ('Health',         'General', '🩺', 'Pharmacy, doctor visits, wellness',           1, 'expense', strftime ('%s', 'now')),
    ('Transport',      'General', '🚗', 'Fuel, public transit, car upkeep',            1, 'expense', strftime ('%s', 'now')),
    ('Dining Out',     'General', '🍽️', 'Restaurants, cafés, take-away',               1, 'expense', strftime ('%s', 'now')),
    ('Other Expenses', 'General', '📦', 'Everything that fits nowhere else',           1, 'expense', strftime ('%s', 'now'));