"""
Python SDK for MiniGun (https://github.com/ranaroussi/minigun).

Stdlib-only — no `requests`, no `httpx` — so you can drop this single
file into any project without touching its dependency tree. Targets
Python 3.9+.

    from minigun import Minigun, MinigunApiError, MinigunTransportError

    mg = Minigun(os.environ["MINIGUN_API_URL"], os.environ["MINIGUN_API_TOKEN"])
    mg.add_contact("newsletter", "alice@example.com", {"first_name": "Alice"})
    res = mg.send_bulk(
        list="newsletter",
        subject="Hi",
        from_="Ran <r@x.com>",
        md="Hello {{first_name | 'there'}}!",
    )
    print(res["send_id"])

All methods raise:
  - MinigunTransportError on a network / SSL / timeout failure
  - MinigunApiError on any 4xx/5xx (status + decoded body are attributes)
  - ValueError on local argument validation (mutually-exclusive flags, etc.)
MinigunError is the common base, so `except MinigunError` catches both.
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Mapping, Optional

__all__ = [
    "Minigun",
    "MinigunError",
    "MinigunTransportError",
    "MinigunApiError",
    "UNSUB_LOCAL",
    "UNSUB_REDIRECT",
    "UNSUB_EXTERNAL",
]

UNSUB_LOCAL = "local"
UNSUB_REDIRECT = "redirect"
UNSUB_EXTERNAL = "external"


class MinigunError(Exception):
    """Base for every error this SDK raises after construction."""


class MinigunTransportError(MinigunError):
    """The HTTP request itself failed (DNS, TLS, timeout, refused, etc.)."""


class MinigunApiError(MinigunError):
    """The server replied with a non-2xx status."""

    def __init__(self, status: int, body: Any, message: str) -> None:
        super().__init__(message)
        self.status = status
        self.body = body


class Minigun:
    """One client per (base_url, token) pair. Thread-safe — internal
    state is read-only after __init__, and each request opens its own
    urllib opener."""

    def __init__(
        self,
        base_url: str,
        token: str = "",
        *,
        connect_timeout: float = 10.0,
        timeout: float = 120.0,
        user_agent: str = "minigun-python/0.1",
    ) -> None:
        if not base_url:
            raise ValueError("base_url is required")
        self._base_url = base_url.rstrip("/")
        self._token = token
        # urllib's `timeout=` is a single overall timeout, not separate
        # connect/read like requests. We keep both args for API parity
        # with the other SDKs but only the larger value is enforced.
        self._timeout = max(float(connect_timeout), float(timeout))
        self._user_agent = user_agent

    # -----------------------------------------------------------------
    # Contacts
    # -----------------------------------------------------------------

    def add_contact(
        self,
        list: str,
        email: str,
        params: Optional[Mapping[str, Any]] = None,
    ) -> dict:
        """Upsert a contact + (re-)subscribe them to a list.

        Safe to call repeatedly: existing rows get `params` merged and
        any prior unsubscribe is cleared.
        """
        path = f"/lists/{_q(list)}/contacts"
        return self._post(path, {"email": email, "params": dict(params) if params is not None else None})

    def unsubscribe_contact(self, list: str, email: str) -> dict:
        """Admin-side unsubscribe by email (no HMAC token required).

        Preserves the row with `subscribed=0` so future re-imports
        don't silently re-subscribe. For hard-bounce / spam-complaint
        cleanup prefer delete_contact().
        """
        path = f"/lists/{_q(list)}/unsubscribe"
        return self._post(path, {"email": email})

    def delete_contact(self, id_or_email: str) -> dict:
        """Permanently purge a contact + every row that references them
        (subscriptions on every list + unsubscribe-event audit log).

        Use for hard-bounce cleanup so the address can't be picked up
        by a future bulk send. The Mailgun webhook does this
        automatically; this method is for scripted / one-off purges.
        Accepts either the contact id (c_XXXX...) or the email address.
        """
        return self._delete(f"/contacts/{_q(id_or_email)}")

    def list_contacts(
        self,
        list: str,
        cursor: Optional[str] = None,
        limit: int = 50,
    ) -> dict:
        """Paginated subscribers on a list. Pass back `cursor` from the
        previous response to walk forward."""
        qs: dict[str, str] = {"limit": str(limit)}
        if cursor:
            qs["cursor"] = cursor
        path = f"/lists/{_q(list)}/contacts?{urllib.parse.urlencode(qs)}"
        return self._get(path)

    # -----------------------------------------------------------------
    # Sends
    # -----------------------------------------------------------------

    def send_single(
        self,
        *,
        to: str,
        from_: str,
        subject: str,
        company: str,
        md: Optional[str] = None,
        md_file: Optional[str] = None,
        html: Optional[str] = None,
        html_file: Optional[str] = None,
        text: Optional[str] = None,
        text_file: Optional[str] = None,
        template: Optional[str] = None,
        template_file: Optional[str] = None,
        preheader: Optional[str] = None,
        reply_to: Optional[str] = None,
        domain: Optional[str] = None,
        list: Optional[str] = None,
        test_mode: bool = False,
    ) -> dict:
        """Send a single transactional email.

        Required: `to`, `from_`, `subject`, `company`, and one of
        md/md_file or html/html_file. `company` is the company id or
        slug — MiniGun resolves the sending domain from it. Pass
        `domain=` to override for this one send.

        Pass at most one of each `*` / `*_file` pair. Files are read
        synchronously at call time.

        Returns immediately (202). The worker performs the Mailgun
        POST in the background — poll get_send() if you need the
        terminal status.
        """
        md = _resolve_body("md", md, md_file)
        html = _resolve_body("html", html, html_file)
        text = _resolve_body("text", text, text_file)
        template = _resolve_body("template", template, template_file)
        if md is None and html is None:
            raise ValueError("either md/md_file or html/html_file is required")

        return self._post(
            "/send/single",
            {
                "to": to,
                "from": from_,
                "subject": subject,
                "preheader": preheader or "",
                "company": company,
                "list": list or "",
                "reply_to": reply_to or "",
                "domain": domain or "",
                "md": md or "",
                "html": html or "",
                "text": text or "",
                "template": template or "",
                "test_mode": test_mode,
            },
        )

    def send_bulk(
        self,
        *,
        list: str,
        subject: str,
        from_: str,
        md: Optional[str] = None,
        md_file: Optional[str] = None,
        html: Optional[str] = None,
        html_file: Optional[str] = None,
        text: Optional[str] = None,
        text_file: Optional[str] = None,
        template: Optional[str] = None,
        template_file: Optional[str] = None,
        reply_to: Optional[str] = None,
        preheader: Optional[str] = None,
        domain: Optional[str] = None,
        batch_size: int = 500,
        throttle_ms: int = 1000,
        notify_to: Optional[str] = None,
        unsub_mode: str = UNSUB_LOCAL,
        unsub_redir: Optional[str] = None,
        unsub_url: Optional[str] = None,
        test_mode: bool = False,
    ) -> dict:
        """Trigger a bulk send to a list. Returns 202 immediately with
        a send_id while the worker drives batches in the background.

        The first batch runs inline before the 202 so the response
        time scales with batch_size + Mailgun's latency, then
        subsequent batches self-chain on the server.
        """
        md = _resolve_body("md", md, md_file)
        html = _resolve_body("html", html, html_file)
        text = _resolve_body("text", text, text_file)
        template = _resolve_body("template", template, template_file)
        if md is None and html is None:
            raise ValueError("either md/md_file or html/html_file is required")
        if unsub_mode not in (UNSUB_LOCAL, UNSUB_REDIRECT, UNSUB_EXTERNAL):
            raise ValueError("unsub_mode must be 'local', 'redirect', or 'external'")
        if unsub_mode == UNSUB_REDIRECT and not unsub_redir:
            raise ValueError("unsub_redir is required when unsub_mode='redirect'")
        if unsub_mode == UNSUB_EXTERNAL and not unsub_url:
            raise ValueError("unsub_url is required when unsub_mode='external'")

        return self._post(
            "/send/bulk",
            {
                "list": list,
                "subject": subject,
                "from": from_,
                "reply_to": reply_to or "",
                "preheader": preheader or "",
                "domain": domain or "",
                "md": md or "",
                "html": html or "",
                "text": text or "",
                "template": template or "",
                "batch_size": batch_size,
                "throttle_ms": throttle_ms,
                "notify_email": notify_to or "",
                "unsub_mode": unsub_mode,
                "unsub_redir": unsub_redir or "",
                "unsub_url": unsub_url or "",
                "test_mode": test_mode,
            },
        )

    def get_send(self, send_id: str) -> dict:
        """One-shot snapshot of a send's status + per-batch progress."""
        return self._get(f"/send/{_q(send_id)}")

    def get_send_stats(self, send_id: str) -> dict:
        """Aggregate stats. DB-backed for completed sends; falls back
        to a live Mailgun Metrics API call for in-flight or just-
        finished ones."""
        return self._get(f"/send/{_q(send_id)}/stats")

    def resume_send(self, send_id: str, force: bool = False) -> dict:
        """Resume a paused or failed send. Pass force=True ONLY if a
        batch was left in_flight: Mailgun may already have accepted
        it, so a retry can duplicate-send."""
        path = f"/send/{_q(send_id)}/resume"
        if force:
            path += "?force=1"
        return self._post(path, {})

    def list_send_recipients(
        self,
        send_id: str,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> dict:
        """One page of per-recipient message engagement for a send (one
        row per contact: sent/delivered timestamps, first/last open + click
        with counts, failure/complaint/unsubscribe state). Requires
        EVENTS_ARCHIVE_ENABLED on the server.

        Returns {"items": [...], "next_cursor"?: str}. Keyset paginated by
        contact_id.

        - limit:  page size (default 100, max 500)
        - cursor: opaque cursor from a previous page's next_cursor
        """
        params = []
        if limit:
            params.append(f"limit={limit}")
        if cursor:
            params.append(f"cursor={_q(cursor)}")
        path = f"/send/{_q(send_id)}/recipients"
        if params:
            path += "?" + "&".join(params)
        return self._get(path)

    def get_contact_engagement(
        self,
        id_or_email: str,
        list_id: Optional[str] = None,
    ) -> dict:
        """Per-list engagement counters for one contact. id_or_email
        accepts a contact id (c_*) or email. Pass list_id to narrow
        to one list (accepts id or slug)."""
        path = f"/contacts/{_q(id_or_email)}/engagement"
        if list_id:
            path += f"?list_id={_q(list_id)}"
        return self._get(path)

    def prune_list(
        self,
        list_id: str,
        min_messages_since_engagement: int = 0,
        dormant_for_days: int = 0,
        no_delivery_for_days: int = 0,
        dry_run: Optional[bool] = None,
        limit: Optional[int] = None,
        sample_size: Optional[int] = None,
    ) -> dict:
        """Unsubscribe dormant contacts from a list. dry_run defaults
        to TRUE server-side — explicitly pass dry_run=False to commit.

        At least one criterion must be > 0; multiple are OR'd.

        - min_messages_since_engagement: messages_since_last_engagement >= N
        - dormant_for_days: last open/click older than D days
        - no_delivery_for_days: never delivered to in the last D days

        Returns {list_id, dry_run, candidates, unsubscribed, sample,
        reason_counts}.
        """
        if min_messages_since_engagement <= 0 and dormant_for_days <= 0 and no_delivery_for_days <= 0:
            raise ValueError(
                "at least one of min_messages_since_engagement, "
                "dormant_for_days, no_delivery_for_days must be > 0"
            )
        body: dict = {
            "min_messages_since_engagement": min_messages_since_engagement,
            "dormant_for_days": dormant_for_days,
            "no_delivery_for_days": no_delivery_for_days,
        }
        if dry_run is not None:
            body["dry_run"] = dry_run
        if limit:
            body["limit"] = limit
        if sample_size:
            body["sample_size"] = sample_size
        return self._post(f"/lists/{_q(list_id)}/prune", body)

    # -----------------------------------------------------------------
    # Transport
    # -----------------------------------------------------------------

    def _get(self, path: str) -> dict:
        return self._request("GET", path, None)

    def _post(self, path: str, body: Any) -> dict:
        return self._request("POST", path, body)

    def _delete(self, path: str) -> dict:
        return self._request("DELETE", path, None)

    def _request(self, method: str, path: str, body: Any) -> dict:
        url = self._base_url + path
        headers = {
            "Accept": "application/json",
            "User-Agent": self._user_agent,
        }
        if self._token:
            headers["Authorization"] = "Bearer " + self._token

        data: Optional[bytes] = None
        if body is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps(body).encode("utf-8")

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self._timeout) as resp:
                status = resp.getcode()
                raw = resp.read()
        except urllib.error.HTTPError as e:
            # 4xx/5xx — body is still readable and we want to surface
            # it as MinigunApiError rather than the urllib transport
            # exception, which is much less useful at the callsite.
            status = e.code
            raw = e.read()
        except urllib.error.URLError as e:
            raise MinigunTransportError(f"transport error: {e.reason}") from e
        except (TimeoutError, OSError) as e:
            raise MinigunTransportError(f"transport error: {e}") from e

        decoded: Any = None
        if raw:
            try:
                decoded = json.loads(raw.decode("utf-8"))
            except (ValueError, UnicodeDecodeError):
                decoded = raw

        if not 200 <= status < 300:
            msg = (
                decoded.get("error")
                if isinstance(decoded, dict) and "error" in decoded
                else (decoded if isinstance(decoded, str) else str(decoded))
            )
            raise MinigunApiError(status, decoded, f"MiniGun API {status}: {msg}")

        return decoded if isinstance(decoded, dict) else {}


# ---------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------


def _q(s: str) -> str:
    """URL-encode a path segment. safe='' so '/' inside an email
    address (impossible but defensive) gets encoded too."""
    return urllib.parse.quote(s, safe="")


def _resolve_body(name: str, direct: Optional[str], file: Optional[str]) -> Optional[str]:
    """Resolve a body-or-file pair. Returns None when neither was
    supplied (caller decides whether the field is required); raises
    ValueError when both are supplied or the file is unreadable."""
    if direct is not None and file is not None:
        raise ValueError(f"pass only one of {name} or {name}_file, not both")
    if file is None:
        return direct
    if not os.path.isfile(file):
        raise ValueError(f"{name}_file '{file}' does not exist")
    with open(file, "r", encoding="utf-8") as fh:
        return fh.read()
