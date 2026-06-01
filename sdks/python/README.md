# MiniGun — Python SDK

Single-file Python client for [MiniGun](https://github.com/ranaroussi/minigun). Stdlib-only — no `requests`, no `httpx`, no `aiohttp` — so you can drop one file into any project, Lambda, or CI script without touching its dependency tree.

**Requires:** Python 3.9+.

## Install

Drop the file in:

```bash
curl -O https://raw.githubusercontent.com/ranaroussi/minigun/main/sdks/python/minigun.py
```

Then:

```python
from minigun import Minigun, MinigunApiError, MinigunTransportError
```

No `pip install`, no `setup.py`. If you'd prefer a packaged install, vendoring the file into your repo is the supported path — it's a single ~320-line file.

## Quickstart

```python
import os
import sys
from minigun import Minigun, MinigunApiError, MinigunTransportError

mg = Minigun(
    os.environ["MINIGUN_API_URL"],
    os.environ["MINIGUN_API_TOKEN"],
)

try:
    # Upsert a contact and subscribe them.
    mg.add_contact("newsletter", "alice@example.com", {"first_name": "Alice"})

    # Send a bulk campaign.
    with open("week-12.md") as fh:
        body = fh.read()
    res = mg.send_bulk(
        list="newsletter",
        subject="Big news this week",
        from_="Ran <ran@example.com>",
        md=body,
    )
    print(f"queued send {res['send_id']} — {res['total_recipients']} recipients")

except MinigunApiError as e:
    print(f"API {e.status}: {e}", file=sys.stderr)
    sys.exit(1)
except MinigunTransportError as e:
    print(f"Network error: {e}", file=sys.stderr)
    sys.exit(2)
```

## Reference

### Construction

```python
Minigun(
    base_url: str,
    token: str = "",
    *,
    connect_timeout: float = 10.0,
    timeout: float = 120.0,
    user_agent: str = "minigun-python/0.1",
)
```

- `base_url` — API origin (e.g. `https://mailer.example.com`). Trailing slash optional.
- `token` — Bearer token. Required when the server has `MINIGUN_API_TOKEN` set.
- `connect_timeout` / `timeout` — Seconds. `urllib` enforces a single overall timeout, so internally the SDK uses `max(connect_timeout, timeout)`. The split is kept in the signature for parity with the other SDKs.

### Contacts

```python
mg.add_contact(list: str, email: str, params: dict | None = None) -> dict
mg.unsubscribe_contact(list: str, email: str) -> dict
mg.delete_contact(id_or_email: str) -> dict
mg.list_contacts(list: str, cursor: str | None = None, limit: int = 50) -> dict
```

- **`add_contact()`** — Upsert. Safe to call repeatedly: existing `params` are merged and any prior unsubscribe is cleared.
- **`unsubscribe_contact()`** — Admin-side opt-out. Preserves the row with `subscribed=0` so future re-imports don't silently re-subscribe. Use this for user-initiated unsubscribes.
- **`delete_contact()`** — Hard purge: removes the contact + every subscription + every audit row. Use this for hard-bounce cleanup. The Mailgun webhook (`/webhooks/mailgun`) does this automatically; this method is for scripted/one-off purges. Accepts either `c_XXXXXXXXXX` ids or email addresses.
- **`list_contacts()`** — Paginated. Returns `{"contacts": [...], "next_cursor": str | None}`.

### Sends

```python
mg.send_single(
    *,
    to: str, company: str,
    from_: str = "", subject: str = "",   # optional — may come from md frontmatter
    md: str | None = None,       md_file: str | None = None,
    html: str | None = None,     html_file: str | None = None,
    text: str | None = None,     text_file: str | None = None,
    template: str | None = None, template_file: str | None = None,
    preheader: str | None = None,
    reply_to: str | None = None,
    domain: str | None = None,
    list: str | None = None,
    test_mode: bool = False,
) -> dict

mg.send_bulk(
    *,
    list: str,
    subject: str = "", from_: str = "",   # optional — may come from md frontmatter
    # ...same body-or-file pairs as send_single...
    batch_size: int = 500,
    throttle_ms: int = 1000,
    notify_to: str | None = None,
    unsub_mode: str = UNSUB_LOCAL,
    unsub_redir: str | None = None,
    unsub_url: str | None = None,
    test_mode: bool = False,
) -> dict

mg.get_send(send_id: str) -> dict
mg.get_send_stats(send_id: str) -> dict
mg.resume_send(send_id: str, force: bool = False) -> dict
```

The send methods are **keyword-only** (note the bare `*` in the signature). That keeps callsites self-documenting given the long argument list:

```python
mg.send_bulk(
    list="newsletter",
    subject="Weekly update",
    from_="Ran <ran@example.com>",
    md_file="./week-12.md",
    test_mode=True,
)
```

> `from_` has a trailing underscore because `from` is a Python keyword. This is the PEP 8 convention.
> `list` shadows the `list` builtin but only as a parameter name inside the call — it has no effect on your surrounding code.

For each body field there's a value-or-file pair (e.g. `md` / `md_file`). Pass at most one; passing both raises `ValueError`.

`subject` and `from_` default to `""` because they can be supplied via the Markdown frontmatter (a leading `---`/`-----` fenced block with `subject:` / `from:` / `preheader:` / `reply_to:`). An explicit argument wins; the block is stripped from the body. If neither the argument nor frontmatter supplies `subject` and `from_`, a `ValueError` is raised.

Unsubscribe-mode constants are exported from the module:

```python
from minigun import UNSUB_LOCAL, UNSUB_REDIRECT, UNSUB_EXTERNAL
```

| Mode | Constant | When to use | Required extra arg |
|---|---|---|---|
| Local unsubscribe page | `UNSUB_LOCAL` (default) | Standard. Renders the MiniGun unsub / preferences page. | — |
| Redirect after unsub | `UNSUB_REDIRECT` | Send the user to your own thank-you page after they opt out. | `unsub_redir` |
| External (your own) | `UNSUB_EXTERNAL` | You host the entire unsub flow on your own domain. | `unsub_url` |

### Errors

```python
try:
    mg.send_bulk(...)
except MinigunApiError as e:
    # 4xx/5xx from the server.
    e.status  # int
    e.body    # parsed JSON (usually a dict with an 'error' key)
except MinigunTransportError as e:
    # Network failure: DNS, TLS, timeout, connection refused, etc.
    pass
except MinigunError:
    # Common base. Catch this if you don't need to branch.
    pass
except ValueError:
    # Local validation — mutually-exclusive args, unknown unsub mode,
    # missing/unreadable file.
    pass
```

The split matters for retry policy: transport errors are often worth retrying with backoff; API errors usually aren't.

## Type hints

Method signatures use standard `typing` annotations. The return type is `dict` (Python doesn't have anonymous typed records without a TypedDict per method, and the shape is API-controlled, so we keep this loose by design).

If you want stricter types in your codebase, the JSON shapes are documented in the [top-level README's API section](../../README.md#api) — you can wrap each method in your own typed helper.

## See also

- [Top-level README](../../README.md) — server install, deployment, full HTTP API reference.
- [Cross-SDK overview](../README.md) — method-name table across all four languages.
- [Auto list hygiene](../../README.md#automatic-list-hygiene) — the Mailgun webhook is server-side and needs no SDK code; the `delete_contact()` method here is the manual / scripted equivalent.
