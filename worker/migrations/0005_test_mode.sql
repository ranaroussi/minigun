-- Adds an explicit test_mode flag to sends so cron-resumed chains preserve
-- the operator's intent. When test_mode = 1, the worker passes
-- o:testmode=yes to Mailgun, which accepts and logs the message but does
-- not actually deliver it.

ALTER TABLE sends ADD COLUMN test_mode INTEGER NOT NULL DEFAULT 0;
