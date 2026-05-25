-- Adds an explicit sending_domain to companies, lists, and sends.
-- Companies own the identity (required). Lists inherit at creation time unless
-- the operator supplies their own. Sends record the resolved value used for that
-- specific batch chain (so multi-batch /next calls stay on the same domain).
ALTER TABLE companies ADD COLUMN sending_domain TEXT NOT NULL DEFAULT '';
ALTER TABLE lists ADD COLUMN sending_domain TEXT NOT NULL DEFAULT '';
ALTER TABLE sends ADD COLUMN sending_domain TEXT NOT NULL DEFAULT '';

UPDATE companies SET sending_domain = 'mail.varops.com' WHERE sending_domain = '';
UPDATE lists SET sending_domain = 'mail.varops.com' WHERE sending_domain = '';
UPDATE sends SET sending_domain = 'mail.varops.com' WHERE sending_domain = '';
