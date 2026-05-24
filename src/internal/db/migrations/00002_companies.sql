-- +goose Up
-- +goose StatementBegin
CREATE TABLE companies (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

ALTER TABLE lists ADD COLUMN company_id TEXT REFERENCES companies(id);
ALTER TABLE lists ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE lists ADD COLUMN weight INTEGER NOT NULL DEFAULT 10;

CREATE INDEX idx_lists_company ON lists(company_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_lists_company;
ALTER TABLE lists DROP COLUMN weight;
ALTER TABLE lists DROP COLUMN description;
ALTER TABLE lists DROP COLUMN company_id;
DROP TABLE IF EXISTS companies;
-- +goose StatementEnd
