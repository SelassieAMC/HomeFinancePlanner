-- Storage taxonomy for scanned bill items: fixed set of grocery storage
-- categories, seeded once. is_system marks the seeded rows; users can still
-- add their own categories for transactions/budgets.
ALTER TABLE categories ADD COLUMN section      TEXT    NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN icon         TEXT    NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN description  TEXT    NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN is_system    INTEGER NOT NULL DEFAULT 0;

-- The total printed on the receipt, kept for the "calculated total does not
-- match the receipt" warning (the stored total is always the computed one).
ALTER TABLE bills ADD COLUMN printed_total_cents INTEGER NOT NULL DEFAULT 0;

INSERT INTO categories (name, section, icon, description, is_system, created_at) VALUES
    -- Fridge (chilled)
    ('Produce',              'Fridge',              '🥬', 'Fresh vegetables, leafy greens, berries, apples, fresh herbs', 1, strftime ('%s', 'now')),
    ('Dairy & Eggs',         'Fridge',              '🥚', 'Milk, cheeses, yogurt, butter, sour cream, fresh eggs',        1, strftime ('%s', 'now')),
    ('Meats & Seafood',      'Fridge',              '🥩', 'Raw chicken, beef, pork, fresh fish, bacon',                   1, strftime ('%s', 'now')),
    ('Deli & Ready-to-Eat',  'Fridge',              '🥪', 'Sliced lunch meats, leftover meals, pre-made salads, dips',    1, strftime ('%s', 'now')),
    ('Chilled Condiments',   'Fridge',              '🍯', 'Opened jars of mayonnaise, mustard, dressings, jams',          1, strftime ('%s', 'now')),
    -- Pantry & cupboards (dry storage)
    ('Grains & Carbs',       'Pantry & Cupboards',  '🍚', 'Rice, dry pasta, oats, quinoa, flour, baking sugar',           1, strftime ('%s', 'now')),
    ('Canned Goods',         'Pantry & Cupboards',  '🥫', 'Canned tomatoes, beans, corn, tuna, soups, jarred sauces',     1, strftime ('%s', 'now')),
    ('Oils & Vinegars',      'Pantry & Cupboards',  '🫙', 'Olive oil, vegetable oil, soy sauce, vinegar, honey',          1, strftime ('%s', 'now')),
    ('Spices & Baking',      'Pantry & Cupboards',  '🧂', 'Salt, pepper, garlic powder, baking powder, vanilla',          1, strftime ('%s', 'now')),
    ('Breakfast & Spreads',  'Pantry & Cupboards',  '🥣', 'Boxed cereals, granola, peanut butter, maple syrup',           1, strftime ('%s', 'now')),
    ('Snacks & Treats',      'Pantry & Cupboards',  '🍿', 'Chips, crackers, nuts, popcorn, cookies, chocolate',           1, strftime ('%s', 'now')),
    ('Beverages',            'Pantry & Cupboards',  '☕', 'Coffee beans, tea bags, juice cartons, soda, bottled water',   1, strftime ('%s', 'now')),
    ('Root Vegetables',      'Pantry & Cupboards',  '🥔', 'Potatoes, sweet potatoes, onions, garlic',                     1, strftime ('%s', 'now')),
    -- Freezer (long-term cold)
    ('Frozen Proteins',      'Freezer',             '🍤', 'Frozen chicken breasts, ground beef, fish fillets, shrimp',    1, strftime ('%s', 'now')),
    ('Frozen Fruits & Veggies', 'Freezer',          '🥦', 'Frozen peas, corn, broccoli florets, berry mixes',             1, strftime ('%s', 'now')),
    ('Frozen Ready Meals',   'Freezer',             '🍕', 'Frozen pizzas, ice cream, popsicles, batch-cooked leftovers',  1, strftime ('%s', 'now')),
    ('Frozen Breads',        'Freezer',             '🧇', 'Sliced bread loaves, bagels, waffles kept long-term',          1, strftime ('%s', 'now')),
    -- Non-food essentials
    ('Paper Goods',          'Non-Food Essentials', '🧻', 'Paper towels, napkins, tissues',                               1, strftime ('%s', 'now')),
    ('Food Wrap & Storage',  'Non-Food Essentials', '🥡', 'Foil, plastic wrap, parchment paper, ziplock bags',            1, strftime ('%s', 'now')),
    ('Cleaning & Dish',      'Non-Food Essentials', '🧽', 'Dishwashing liquid, dishwasher tablets, sponges, trash bags',  1, strftime ('%s', 'now'));