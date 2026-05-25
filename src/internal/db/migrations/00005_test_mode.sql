-- +goose Up
-- +goose StatementBegin
ALTER TABLE sends ADD COLUMN test_mode INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sends DROP COLUMN test_mode;
-- +goose StatementEnd
