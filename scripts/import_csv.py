#!/usr/bin/env python3
"""
Bulk-import a CSV of contacts into MiniGun's D1 database, subscribing
each row to a single list.

Generates a sequence of SQL files (chunked to stay under D1's
~10k-statement-per-file limit) that you then apply via:

    cd worker
    for f in /tmp/minigun-import.*.sql; do
        wrangler d1 execute minigun --remote --file="$f"
    done

CSV requirements
----------------
- Must have an `email` column.
- Optional `created_at` column; if present and numeric it is treated as
  Unix epoch seconds and converted to ISO 8601 (matches the format the
  app writes natively). If absent, the current time is used.
- Any other columns are bundled into the contact's `params` JSON blob.

Idempotency
-----------
- `INSERT OR IGNORE` on contacts (UNIQUE on email).
- `INSERT OR IGNORE` on subscriptions (UNIQUE on list_id + contact_id).
- Re-running the importer with the same CSV is a no-op.
- Contacts that previously unsubscribed stay unsubscribed: their
  existing subscription row blocks the import from flipping the flag,
  which is the legally safe behavior.

Usage
-----
    python3 scripts/import_csv.py <csv> <list_id> [out_prefix] [--chunk N]

Defaults:
    out_prefix = /tmp/minigun-import
    chunk size = 5000 rows (= 10000 SQL statements per file)
"""
from __future__ import annotations

import argparse
import base64
import csv
import json
import secrets
import sys
from datetime import datetime, timezone
from pathlib import Path


def new_contact_id() -> str:
    """Generates a `c_XXXXXXXXXX` id matching the format produced by
    worker/src/lib/ids.ts and src/internal/ids — 10 random bytes, base32
    encoded with the RFC 4648 alphabet, truncated to 10 chars."""
    encoded = base64.b32encode(secrets.token_bytes(10)).decode("ascii").rstrip("=")
    return "c_" + encoded[:10]


def sql_quote(value: str) -> str:
    """Escapes single quotes for inline SQL literals. We avoid full
    parameterized statements because we emit SQL files for
    `wrangler d1 execute --file=...`, which doesn't take parameters."""
    return value.replace("'", "''")


def parse_created_at(raw: str | None, default_iso: str) -> str:
    """Accepts either a Unix epoch (seconds, as written by many older
    PHP/Postgres exports) or an ISO 8601 string. Returns ISO 8601 with
    millisecond precision + 'Z' suffix to match the format the app
    writes natively (e.g. 2026-05-24T22:09:39.200Z)."""
    if raw is None or not raw.strip():
        return default_iso
    raw = raw.strip()
    # Numeric epoch path — by far the common case for exports.
    try:
        epoch = int(raw)
        return (
            datetime.fromtimestamp(epoch, tz=timezone.utc)
            .isoformat(timespec="milliseconds")
            .replace("+00:00", "Z")
        )
    except ValueError:
        pass
    # ISO path — assume the caller already formatted correctly. We
    # normalize timezone to Z so the column has consistent ordering.
    try:
        dt = datetime.fromisoformat(raw.replace("Z", "+00:00"))
        return (
            dt.astimezone(timezone.utc)
            .isoformat(timespec="milliseconds")
            .replace("+00:00", "Z")
        )
    except ValueError:
        return default_iso


def iter_rows(csv_path: Path, list_id: str, default_iso: str):
    """Yields (sql_statement_1, sql_statement_2) pairs — one INSERT for
    the contact, one for the subscription, joined by email so the
    subscription always references the canonical contact_id (whether
    newly inserted by this import or already present from a previous
    one)."""
    list_id_esc = sql_quote(list_id)
    with csv_path.open(newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            email = (row.get("email") or "").strip().lower()
            if not email or "@" not in email:
                continue
            created_at = parse_created_at(row.get("created_at"), default_iso)
            params = {
                k: v
                for k, v in row.items()
                if k not in ("email", "created_at") and v not in (None, "")
            }
            params_json = sql_quote(json.dumps(params, ensure_ascii=False))
            cid = new_contact_id()
            email_esc = sql_quote(email)
            yield (
                f"INSERT OR IGNORE INTO contacts "
                f"(id, email, params, created_at, updated_at) VALUES "
                f"('{cid}', '{email_esc}', '{params_json}', "
                f"'{created_at}', '{created_at}');",
                f"INSERT OR IGNORE INTO subscriptions "
                f"(list_id, contact_id, subscribed, subscribed_at, updated_at) "
                f"SELECT '{list_id_esc}', id, 1, '{created_at}', '{created_at}' "
                f"FROM contacts WHERE email = '{email_esc}';",
            )


def write_chunk(path: Path, statements: list[str]) -> None:
    """D1's remote `--file` runner manages atomicity itself and rejects
    BEGIN/COMMIT/SAVEPOINT in user SQL (it pushes you to use the
    storage.transaction API). So we emit a plain stream of statements
    and rely on D1's own per-file batching for partial-failure recovery.
    A chunk that fails mid-way leaves any successfully-committed inner
    statements in place; INSERT OR IGNORE makes a retry of the same
    chunk safe."""
    with path.open("w", encoding="utf-8") as f:
        for s in statements:
            f.write(s)
            f.write("\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("csv", type=Path)
    parser.add_argument("list_id")
    parser.add_argument("out_prefix", type=Path, nargs="?", default=Path("/tmp/minigun-import"))
    parser.add_argument(
        "--chunk",
        type=int,
        default=5000,
        help="Rows per output file (each row = 2 SQL statements).",
    )
    args = parser.parse_args()

    default_iso = (
        datetime.now(timezone.utc)
        .isoformat(timespec="milliseconds")
        .replace("+00:00", "Z")
    )

    args.out_prefix.parent.mkdir(parents=True, exist_ok=True)
    chunk_idx = 0
    row_count = 0
    buffer: list[str] = []
    written_files: list[Path] = []

    def flush() -> None:
        nonlocal chunk_idx
        if not buffer:
            return
        chunk_idx += 1
        out_path = args.out_prefix.with_suffix(f".{chunk_idx:04d}.sql")
        write_chunk(out_path, buffer)
        written_files.append(out_path)
        buffer.clear()

    rows_in_chunk = 0
    for c_sql, s_sql in iter_rows(args.csv, args.list_id, default_iso):
        buffer.extend((c_sql, s_sql))
        rows_in_chunk += 1
        row_count += 1
        if rows_in_chunk >= args.chunk:
            flush()
            rows_in_chunk = 0
    flush()

    print(f"wrote {row_count} rows across {len(written_files)} file(s):", file=sys.stderr)
    for p in written_files:
        print(f"  {p}", file=sys.stderr)
    for p in written_files:
        print(p)


if __name__ == "__main__":
    main()
