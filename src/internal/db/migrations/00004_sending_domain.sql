-- +goose Up
-- +goose StatementBegin
ALTER TABLE companies ADD COLUMN sending_domain TEXT NOT NULL DEFAULT '';
ALTER TABLE lists ADD COLUMN sending_domain TEXT NOT NULL DEFAULT '';
ALTER TABLE sends ADD COLUMN sending_domain TEXT NOT NULL DEFAULT '';

UPDATE companies SET sending_domain = 'mail.varops.com' WHERE sending_domain = '';
UPDATE lists SET sending_domain = 'mail.varops.com' WHERE sending_domain = '';
UPDATE sends SET sending_domain = 'mail.varops.com' WHERE sending_domain = '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sends DROP COLUMN sending_domain;
ALTER TABLE lists DROP COLUMN sending_domain;
ALTER TABLE companies DROP COLUMN sending_domain;
-- +goose StatementEnd
