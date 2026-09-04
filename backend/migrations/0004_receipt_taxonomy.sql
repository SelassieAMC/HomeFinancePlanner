-- Storage taxonomy for scanned bill items: fixed set of grocery storage
-- categories, seeded once. is_system marks the seeded rows; users can still
-- add their own categories for transactions/budgets.
-- The taxonomy is intentionally fine-grained so spending can be analyzed at
-- product level (e.g. cola vs beer vs spirits, vegetables vs fruits) while
-- the section column keeps the pickers grouped by storage area.
ALTER TABLE categories ADD COLUMN section      TEXT    NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN icon         TEXT    NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN description  TEXT    NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN is_system    INTEGER NOT NULL DEFAULT 0;

-- The total printed on the receipt, kept for the "calculated total does not
-- match the receipt" warning (the stored total is always the computed one).
ALTER TABLE bills ADD COLUMN printed_total_cents INTEGER NOT NULL DEFAULT 0;

INSERT INTO categories (name, section, icon, description, is_system, created_at) VALUES
    -- Fridge (chilled)
    ('Vegetables',           'Fridge',              '🥬', 'Fresh vegetables: lettuce, tomatoes, cucumbers, peppers, carrots, broccoli, fresh herbs', 1, strftime ('%s', 'now')),
    ('Fruits',               'Fridge',              '🍎', 'Fresh fruit: apples, bananas, oranges, berries, grapes, melons', 1, strftime ('%s', 'now')),
    ('Dairy & Eggs',         'Fridge',              '🥚', 'Milk, yogurt, butter, cream, fresh eggs',                    1, strftime ('%s', 'now')),
    ('Cheese',               'Fridge',              '🧀', 'Hard and soft cheeses, sliced cheese, feta, mozzarella',     1, strftime ('%s', 'now')),
    ('Meats',                'Fridge',              '🥩', 'Raw chicken, beef, pork, ground meat, fresh sausages',       1, strftime ('%s', 'now')),
    ('Seafood',              'Fridge',              '🐟', 'Fresh fish, shrimp, mussels, seafood',                       1, strftime ('%s', 'now')),
    ('Deli & Ready-to-Eat',  'Fridge',              '🥪', 'Sliced lunch meats, pre-made salads, dips, chilled ready meals', 1, strftime ('%s', 'now')),
    ('Chilled Condiments',   'Fridge',              '🍯', 'Opened jars of mayonnaise, mustard, dressings, jams',        1, strftime ('%s', 'now')),
    -- Pantry & cupboards (dry storage)
    ('Pasta, Rice & Grains', 'Pantry & Cupboards',  '🍚', 'Rice, dry pasta, noodles, oats, quinoa, flour',              1, strftime ('%s', 'now')),
    ('Canned & Jarred',      'Pantry & Cupboards',  '🥫', 'Canned tomatoes, beans, corn, tuna, soups, jarred sauces',   1, strftime ('%s', 'now')),
    ('Oils & Vinegars',      'Pantry & Cupboards',  '🫙', 'Olive oil, vegetable oil, soy sauce, vinegar, honey',        1, strftime ('%s', 'now')),
    ('Spices & Baking',      'Pantry & Cupboards',  '🧂', 'Salt, pepper, spices, baking powder, vanilla, sugar',        1, strftime ('%s', 'now')),
    ('Breakfast & Spreads',  'Pantry & Cupboards',  '🥣', 'Boxed cereals, granola, peanut butter, maple syrup',         1, strftime ('%s', 'now')),
    ('Sweets & Chocolate',   'Pantry & Cupboards',  '🍫', 'Chocolate bars, candy, gummies, cookies',                    1, strftime ('%s', 'now')),
    ('Salty Snacks',         'Pantry & Cupboards',  '🍿', 'Chips, crackers, pretzels, popcorn',                         1, strftime ('%s', 'now')),
    ('Nuts & Dried Fruits',  'Pantry & Cupboards',  '🥜', 'Nuts, seeds, trail mixes, dried fruit',                      1, strftime ('%s', 'now')),
    ('Root Vegetables',      'Pantry & Cupboards',  '🥔', 'Potatoes, sweet potatoes, onions, garlic',                   1, strftime ('%s', 'now')),
    -- Beverages (drinks get their own product families)
    ('Water & Iced Tea',     'Beverages',           '💧', 'Still and sparkling water, iced tea',                        1, strftime ('%s', 'now')),
    ('Cola & Soda',          'Beverages',           '🥤', 'Cola, lemon-lime soda, orange soda, energy drinks',          1, strftime ('%s', 'now')),
    ('Juice',                'Beverages',           '🧃', 'Orange juice, apple juice, nectars, smoothies',              1, strftime ('%s', 'now')),
    ('Coffee & Tea',         'Beverages',           '☕', 'Coffee beans, ground coffee, tea bags, hot cocoa',           1, strftime ('%s', 'now')),
    ('Milk Drinks & Alternatives', 'Beverages',      '🥛', 'Plant milks (oat, soy, almond), drinking yogurt',            1, strftime ('%s', 'now')),
    ('Beer',                 'Beverages',           '🍺', 'Beer, radler, shandy, non-alcoholic beer',                   1, strftime ('%s', 'now')),
    ('Wine',                 'Beverages',           '🍷', 'Red, white, rosé and sparkling wine',                        1, strftime ('%s', 'now')),
    ('Spirits & Liqueurs',   'Beverages',           '🥃', 'Vodka, whisky, rum, gin, liqueurs, cocktails',               1, strftime ('%s', 'now')),
    -- Freezer (long-term cold)
    ('Frozen Vegetables',    'Freezer',             '🥦', 'Frozen peas, corn, broccoli, spinach',                       1, strftime ('%s', 'now')),
    ('Frozen Fruits',        'Freezer',             '🫐', 'Frozen berries, smoothie fruit mixes',                       1, strftime ('%s', 'now')),
    ('Frozen Meat & Fish',   'Freezer',             '🍤', 'Frozen chicken breasts, ground beef, fish fillets, shrimp',  1, strftime ('%s', 'now')),
    ('Frozen Ready Meals',   'Freezer',             '🍕', 'Frozen pizzas, lasagna, ice cream, popsicles',               1, strftime ('%s', 'now')),
    ('Frozen Breads',        'Freezer',             '🧇', 'Sliced bread loaves, bagels, waffles kept long-term',        1, strftime ('%s', 'now')),
    -- Non-food essentials
    ('Paper Goods',          'Non-Food Essentials', '🧻', 'Paper towels, napkins, tissues, toilet paper',               1, strftime ('%s', 'now')),
    ('Food Wrap & Storage',  'Non-Food Essentials', '🥡', 'Foil, plastic wrap, parchment paper, ziplock bags',          1, strftime ('%s', 'now')),
    ('Cleaning & Dish',      'Non-Food Essentials', '🧽', 'Dishwashing liquid, dishwasher tablets, sponges, trash bags', 1, strftime ('%s', 'now'));