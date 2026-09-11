-- Taxonomy expansion: the app is not groceries-only anymore.
--
-- Two families are seeded, following the 0004/0007/0011 pattern:
--   kind='product'  — bill-item classification for non-grocery receipts
--                     (gas stations, pharmacies, hardware, …). New sections
--                     group them; the AI prompt and categoryAliases must
--                     list them too.
--   kind='expense'  — fine-grained budget/transaction groupings (utilities,
--                     vehicle, gym, subscriptions, …) in their own sections.
-- The 8 coarse expense categories from 0007 keep section 'General' as
-- catch-alls; nothing existing is renamed or removed (budgets reference IDs).
INSERT INTO categories (name, section, icon, description, is_system, kind, created_at) VALUES
    -- Vehicle & Fuel (gas-station and car-shop receipts)
    ('Fuel & Gasoline',           'Vehicle & Fuel',        '⛽', 'Petrol, diesel, E5/E10/Super fuel lines, AdBlue at the pump',                     1, 'product', strftime ('%s', 'now')),
    ('Car Oils & Fluids',         'Vehicle & Fuel',        '🛢️', 'Engine oil, transmission fluid, coolant, brake fluid, screenwash',              1, 'product', strftime ('%s', 'now')),
    ('Car Parts & Care',          'Vehicle & Fuel',        '🔧', 'Wiper blades, bulbs, filters, car batteries, wax, interior care',               1, 'product', strftime ('%s', 'now')),
    -- Pharmacy & Health (drugstore/pharmacy receipts)
    ('Medicines',                'Pharmacy & Health',      '💊', 'OTC and pharmacy medicines: painkillers, cold remedies, prescriptions',          1, 'product', strftime ('%s', 'now')),
    ('Vitamins & Supplements',   'Pharmacy & Health',      '🍊', 'Vitamins, minerals, protein powder, dietary supplements',                       1, 'product', strftime ('%s', 'now')),
    ('First Aid',                'Pharmacy & Health',      '🩹', 'Bandages, gauze, disinfectant, thermometers',                                    1, 'product', strftime ('%s', 'now')),
    -- Beauty & Personal Care (drugstore non-food aisle)
    ('Cosmetics',                'Beauty & Personal Care','💄', 'Makeup, skincare, perfume, nail care',                                         1, 'product', strftime ('%s', 'now')),
    ('Hair & Body Care',         'Beauty & Personal Care','🧴', 'Shampoo, soap, shower gel, deodorant, toothpaste, shaving',                     1, 'product', strftime ('%s', 'now')),
    -- Pets
    ('Pet Supplies',             'Pets',                   '🐾', 'Pet food, litter, pet accessories and treats',                                  1, 'product', strftime ('%s', 'now')),
    -- Baby & Kids
    ('Baby Care',                'Baby & Kids',            '🍼', 'Diapers, wipes, baby food, formula, baby hygiene',                              1, 'product', strftime ('%s', 'now')),
    ('Toys & Games',             'Baby & Kids',            '🧸', 'Toys, board games, video games, puzzles',                                        1, 'product', strftime ('%s', 'now')),
    -- Home & Hardware (non-food store sections beyond groceries)
    ('Hardware & Tools',         'Home & Hardware',        '🛠️', 'Screws, tools, light bulbs, small electrical, glue, paint',                      1, 'product', strftime ('%s', 'now')),
    ('Garden & Outdoor',         'Home & Hardware',        '🌱', 'Plants, seeds, soil, garden tools, BBQ supplies',                                1, 'product', strftime ('%s', 'now')),
    -- Electronics & Office
    ('Electronics & Accessories','Electronics & Office',   '🔌', 'Chargers, cables, headphones, household batteries, small electronics',           1, 'product', strftime ('%s', 'now')),
    ('Stationery & Office',      'Electronics & Office',   '📎', 'Pens, paper, notebooks, printer ink',                                           1, 'product', strftime ('%s', 'now')),
    ('Books & Media',            'Electronics & Office',   '📚', 'Books, magazines, DVDs',                                                         1, 'product', strftime ('%s', 'now')),
    -- Clothing
    ('Clothing & Footwear',      'Clothing',               '👕', 'Clothes, shoes, belts, accessories',                                            1, 'product', strftime ('%s', 'now')),
    -- Expense categories: Utilities
    ('Internet & Telecom',       'Utilities',              '📶', 'Internet, mobile phone, landline, TV packages',                                 1, 'expense', strftime ('%s', 'now')),
    ('Electricity',              'Utilities',              '💡', 'Electricity bills and grid fees',                                              1, 'expense', strftime ('%s', 'now')),
    ('Gas',                      'Utilities',              '🔥', 'Gas/heating bills',                                                             1, 'expense', strftime ('%s', 'now')),
    ('Water',                    'Utilities',              '🚰', 'Water and sewage bills',                                                         1, 'expense', strftime ('%s', 'now')),
    ('Waste & Recycling',        'Utilities',              '🗑️', 'Garbage collection, recycling fees',                                            1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Vehicle & Transport
    ('Fuel',                     'Vehicle & Transport',    '⛽', 'Fuel, petrol, diesel',                                                          1, 'expense', strftime ('%s', 'now')),
    ('Car Maintenance',          'Vehicle & Transport',    '🧰', 'Car oil, repairs, tires, inspections, spare parts',                             1, 'expense', strftime ('%s', 'now')),
    ('Car Insurance',            'Vehicle & Transport',    '🛡️', 'Car and vehicle insurance',                                                      1, 'expense', strftime ('%s', 'now')),
    ('Public Transit',           'Vehicle & Transport',    '🚆', 'Trains, buses, metro tickets and passes',                                       1, 'expense', strftime ('%s', 'now')),
    ('Parking & Tolls',          'Vehicle & Transport',    '🅿️', 'Parking, road tolls, congestion charges',                                        1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Housing & Home
    ('Rent & Mortgage',          'Housing & Home',         '🏠', 'Rent, mortgage payments, association fees',                                     1, 'expense', strftime ('%s', 'now')),
    ('Home Maintenance',         'Housing & Home',         '🔨', 'Repairs, plumber, electrician, furniture, appliances',                          1, 'expense', strftime ('%s', 'now')),
    ('Cleaning Service',         'Housing & Home',         '🧹', 'House cleaning, window cleaning, housekeeping',                                1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Health & Wellness
    ('Gym & Sports',             'Health & Wellness',      '🏋️', 'Gym membership, sports clubs, courses, gear',                                    1, 'expense', strftime ('%s', 'now')),
    ('Pharmacy & Health',        'Health & Wellness',      '💊', 'Pharmacy, medicines, doctor visits, therapy',                                   1, 'expense', strftime ('%s', 'now')),
    ('Personal Care',            'Health & Wellness',      '🧖', 'Hairdresser, barber, spa, cosmetics',                                             1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Leisure & Subscriptions
    ('Streaming & Subscriptions','Leisure & Subscriptions', '📺', 'Netflix/Spotify and other recurring subscriptions',                             1, 'expense', strftime ('%s', 'now')),
    ('Hobbies & Games',          'Leisure & Subscriptions', '🎲', 'Hobby supplies, games, crafts',                                                 1, 'expense', strftime ('%s', 'now')),
    ('Events & Tickets',         'Leisure & Subscriptions', '🎫', 'Cinema, concerts, sports events, exhibitions',                                 1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Family & Pets
    ('Pets',                     'Family & Pets',          '🐾', 'Pet food, vet, grooming, pet insurance',                                        1, 'expense', strftime ('%s', 'now')),
    ('Kids & Education',         'Family & Pets',          '🎒', 'School, kindergarten, childcare, courses',                                      1, 'expense', strftime ('%s', 'now')),
    ('Gifts & Donations',        'Family & Pets',          '🎁', 'Presents, gift cards, charity donations',                                        1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Finance
    ('Insurance',                'Finance',                '🛡️', 'Home, liability, life and other non-car insurance',                             1, 'expense', strftime ('%s', 'now')),
    ('Taxes & Fees',             'Finance',                '🏛️', 'Income tax, property tax, government fees',                                     1, 'expense', strftime ('%s', 'now')),
    ('Bank Fees',                'Finance',                '🏦', 'Account fees, transfer fees, interest charges',                                 1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Clothing (the product twin is 'Clothing & Footwear';
    -- category names are UNIQUE and draft resolution matches by name)
    ('Clothing',                  'Clothing',               '👕', 'Clothes, shoes, accessories',                                                    1, 'expense', strftime ('%s', 'now')),
    -- Expense categories: Income (kind='expense' family: kind distinguishes the
    -- product/expense families, not transaction direction; income transactions
    -- never hit spendByCategory, which sums expense-kind TRANSACTIONS)
    ('Salary',                   'Income',                 '💰', 'Wages, salary payments',                                                        1, 'expense', strftime ('%s', 'now')),
    ('Freelance & Side Income',  'Income',                 '💼', 'Freelance work, side jobs, royalties',                                          1, 'expense', strftime ('%s', 'now')),
    ('Refunds & Interest',        'Income',                 '↩️', 'Refunds, reimbursements, bank interest, dividends',                             1, 'expense', strftime ('%s', 'now'));