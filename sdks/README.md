# MiniGun SDKs

Single-file, zero-dependency client libraries for [MiniGun](https://github.com/ranaroussi/minigun).

Pick the one that matches your server language, drop the file into your project, and you have a typed, idiomatic client for the full MiniGun HTTP API — no `requests`, no `axios`, no `guzzle`, no module graph to think about.

| Language | Min. version | File | Install path | Quickstart |
|---|---|---|---|---|
| [PHP](./php/)        | 7.4+        | [`php/minigun.php`](./php/minigun.php)             | drop in + `require_once`                                                            | [php/README.md](./php/README.md) |
| [Python](./python/)  | 3.9+        | [`python/minigun.py`](./python/minigun.py)         | drop in + `from minigun import Minigun`                                             | [python/README.md](./python/README.md) |
| [TypeScript](./typescript/) | ES2020+ | [`typescript/minigun.ts`](./typescript/minigun.ts) | drop in + `import { Minigun } from './minigun'`                                     | [typescript/README.md](./typescript/README.md) |
| [Go](./go/)          | 1.21+       | [`go/minigun.go`](./go/minigun.go)                 | `go get github.com/ranaroussi/minigun/sdks/go`                                      | [go/README.md](./go/README.md) |

## Design

The PHP SDK was the first one written and the others mirror its shape deliberately. Reading any one of them teaches you all four.

**Single file, no dependencies.** Each SDK is one file you can copy into a project and have working immediately. The Python SDK uses `urllib`, the Go SDK uses `net/http`, the TypeScript SDK uses the standard `fetch` API, and the PHP SDK uses ext-curl. Nothing else.

**Same surface across all four.** Method names follow each language's idiom (`snake_case`, `camelCase`, `PascalCase`) but the semantics, arguments, defaults, and validation rules are identical. A complete reference:

| Operation             | PHP                          | Python                  | TypeScript               | Go                       |
|-----------------------|------------------------------|-------------------------|--------------------------|--------------------------|
| Upsert contact        | `addContact()`               | `add_contact()`         | `addContact()`           | `AddContact()`           |
| Admin unsubscribe     | `unsubscribeContact()`       | `unsubscribe_contact()` | `unsubscribeContact()`   | `UnsubscribeContact()`   |
| Hard-purge contact    | `deleteContact()`            | `delete_contact()`      | `deleteContact()`        | `DeleteContact()`        |
| Paginate contacts     | `listContacts()`             | `list_contacts()`       | `listContacts()`         | `ListContacts()`         |
| Single transactional  | `sendSingle()`               | `send_single()`         | `sendSingle()`           | `SendSingle()`           |
| Bulk send             | `sendBulk()`                 | `send_bulk()`           | `sendBulk()`             | `SendBulk()`             |
| Send status snapshot  | `getSend()`                  | `get_send()`            | `getSend()`              | `GetSend()`              |
| Send aggregate stats  | `getSendStats()`             | `get_send_stats()`      | `getSendStats()`         | `GetSendStats()`         |
| Resume paused/failed  | `resumeSend()`               | `resume_send()`         | `resumeSend()`           | `ResumeSend()`           |
| Send recipient rollup | `listSendRecipients()`       | `list_send_recipients()`| `listSendRecipients()`   | `ListSendRecipients()`   |
| Send per-URL clicks   | `listSendClicks()`           | `list_send_clicks()`    | `listSendClicks()`       | `ListSendClicks()`       |
| Contact engagement    | `getContactEngagement()`     | `get_contact_engagement()` | `getContactEngagement()` | `GetContactEngagement()` |
| Prune dormant list    | `pruneList()`                | `prune_list()`          | `pruneList()`            | `PruneList()`            |

The unsubscribe-mode constants are the same too: `UNSUB_LOCAL` / `UNSUB_REDIRECT` / `UNSUB_EXTERNAL` (PHP class consts, Python module constants, TypeScript exported constants, Go package constants).

**Three-tier error model.** Every SDK surfaces failures in one of three buckets so retry logic, alerting, and user-facing messages can branch cleanly:

1. **Local validation** — your code passed bad arguments (e.g. both `md` and `mdFile`, an unknown `unsub_mode`). Raised before any HTTP call. Each language uses its native shape: PHP `\InvalidArgumentException`, Python `ValueError`, TypeScript `Error`, Go a plain `errors.New` value.
2. **Transport error** — the request never completed: DNS failure, TLS handshake, timeout, connection refused, context cancellation. Raised as `MinigunTransportException` / `MinigunTransportError` / `*minigun.TransportError`.
3. **API error** — the server returned a 4xx or 5xx. Raised as `MinigunApiException` / `MinigunApiError` / `*minigun.APIError`, with the status code and decoded body attached as first-class fields.

The split matters for retry policy. Transport errors are often worth retrying with backoff (transient network blip); API errors usually aren't (a 404 won't suddenly become 200).

**Body-or-file pairs for sends.** The send methods accept `md` *or* `md_file` (and similarly for `html`, `text`, `template`). Pass at most one of each pair — passing both raises a validation error. Files are read synchronously at call time.

## When to use the HTTP API directly instead

These SDKs handle the request shapes, the bearer auth, the error decoding, and the body-or-file ergonomics — i.e. the boilerplate. There's nothing you can do via these SDKs that you can't do with a hand-rolled `curl` or `fetch` call.

Reach for the raw HTTP API ([documented in the top-level README](../README.md#api)) when:

- Your language isn't in the table above and adding a fifth SDK to maintain isn't justified.
- You're writing a one-off shell script and a single `curl` is shorter than `composer require` / `pip install` / `npm install`.
- You need to inspect headers (e.g. `Retry-After` on a 429), which the SDKs don't expose. (Open an issue if this is blocking you — it's easy to add.)

## Stability

All four SDKs are at parity and were live-smoke-tested against the deployed worker before commit. The wire format (request shape, response shape, status codes) is stable — these SDKs only have to evolve when the underlying API gains new endpoints or fields.

If you find a method missing or an argument mismatch between SDKs, that's a bug — please open an issue.
