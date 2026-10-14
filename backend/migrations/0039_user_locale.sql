-- UI language preference per user (i18n): one of 'en' (default), 'de', 'es'.
-- Every existing user stays english until they pick a language in settings.
ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT 'en';