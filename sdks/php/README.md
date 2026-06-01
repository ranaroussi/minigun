# MiniGun — PHP SDK

Single-file PHP client for [MiniGun](https://github.com/ranaroussi/minigun). One `require_once`, no Composer dependency, no autoloader to configure. Built on ext-curl (bundled with every supported PHP build).

**Requires:** PHP 7.4+ with `ext-curl` and `ext-json`.

## Install

Drop the file in:

```bash
curl -O https://raw.githubusercontent.com/ranaroussi/minigun/main/sdks/php/minigun.php
```

Then:

```php
require_once __DIR__ . '/minigun.php';
```

That's it. The file declares `Minigun`, `MinigunException`, `MinigunTransportException`, and `MinigunApiException` in the root namespace.

## Quickstart

```php
<?php
require_once __DIR__ . '/minigun.php';

$mg = new Minigun(
    getenv('MINIGUN_API_URL'),
    getenv('MINIGUN_API_TOKEN'),
);

try {
    // Upsert a contact and subscribe them.
    $mg->addContact('newsletter', 'alice@example.com', ['first_name' => 'Alice']);

    // Send a bulk campaign.
    $res = $mg->sendBulk(
        list: 'newsletter',
        subject: 'Big news this week',
        from: 'Ran <ran@example.com>',
        md: file_get_contents(__DIR__ . '/week-12.md'),
    );

    echo "queued send {$res['send_id']} — {$res['total_recipients']} recipients\n";
} catch (MinigunApiException $e) {
    fwrite(STDERR, "API {$e->status}: {$e->getMessage()}\n");
    exit(1);
} catch (MinigunTransportException $e) {
    fwrite(STDERR, "Network error: {$e->getMessage()}\n");
    exit(2);
}
```

## Reference

### Construction

```php
new Minigun(
    string $baseUrl,
    string $token          = '',
    int    $connectTimeout = 10,
    int    $timeout        = 120,
    string $userAgent      = 'minigun-php/0.1',
);
```

- `$baseUrl` — API origin (e.g. `https://mailer.example.com`). Trailing slash optional.
- `$token` — Bearer token. Required when the server has `MINIGUN_API_TOKEN` set.
- Timeouts are in seconds.

### Contacts

```php
$mg->addContact(string $list, string $email, ?array $params = null): array;
$mg->unsubscribeContact(string $list, string $email): array;
$mg->deleteContact(string $idOrEmail): array;
$mg->listContacts(string $list, ?string $cursor = null, int $limit = 50): array;
```

- **`addContact()`** — Upsert. Safe to call repeatedly: existing `params` are merged and any prior unsubscribe is cleared.
- **`unsubscribeContact()`** — Admin-side opt-out. Preserves the row with `subscribed=0` so future re-imports don't silently re-subscribe. Use this for user-initiated unsubscribes.
- **`deleteContact()`** — Hard purge: removes the contact + every subscription + every audit row. Use this for hard-bounce cleanup. The Mailgun webhook (`/webhooks/mailgun`) does this automatically; this method is for scripted/one-off purges. Accepts either `c_XXXXXXXXXX` ids or email addresses.
- **`listContacts()`** — Paginated. Returns `{contacts: [...], next_cursor: string|null}`. Pass the cursor back to walk forward.

### Sends

```php
$mg->sendSingle(
    string $to, string $company,
    /* optional — may come from md frontmatter: */ string $from = '', string $subject = '',
    /* one of: */ ?string $md = null, ?string $mdFile = null,
    ?string $html = null, ?string $htmlFile = null,
    ?string $text = null, ?string $textFile = null,
    ?string $template = null, ?string $templateFile = null,
    ?string $preheader = null, ?string $replyTo = null,
    ?string $domain = null, ?string $list = null,
    bool $testMode = false,
): array;

$mg->sendBulk(
    string $list,
    /* optional — may come from md frontmatter: */ string $subject = '', string $from = '',
    /* one of: */ ?string $md = null, ?string $mdFile = null,
    ?string $html = null, ?string $htmlFile = null,
    /* ... */
    int $batchSize = 500, int $throttleMs = 1000,
    ?string $notifyTo = null,
    string $unsubMode = Minigun::UNSUB_LOCAL,
    ?string $unsubRedir = null, ?string $unsubUrl = null,
    bool $testMode = false,
): array;

$mg->getSend(string $sendId): array;
$mg->getSendStats(string $sendId): array;
$mg->resumeSend(string $sendId, bool $force = false): array;
```

PHP 8's named arguments make the long signatures readable:

```php
$mg->sendBulk(
    list: 'newsletter',
    subject: 'Weekly update',
    from: 'Ran <ran@example.com>',
    mdFile: __DIR__ . '/week-12.md',
    testMode: true,
);
```

For each body field there's a "value-or-file path" pair (e.g. `$md` / `$mdFile`). Pass at most one; passing both throws `InvalidArgumentException`.

`$subject` and `$from` are optional in the signature because they can be supplied via the Markdown frontmatter (a leading `---`/`-----` fenced block with `subject:` / `from:` / `preheader:` / `reply_to:`). An explicit argument wins; the block is stripped from the body. Use named arguments to skip them. If neither the argument nor frontmatter supplies `$subject` and `$from`, an `InvalidArgumentException` is thrown.

Bulk-send unsubscribe modes:

| Mode | Constant | When to use | Required extra arg |
|---|---|---|---|
| Local unsubscribe page | `Minigun::UNSUB_LOCAL` (default) | Standard. Renders the MiniGun unsubscribe / preferences page. | — |
| Redirect after unsub | `Minigun::UNSUB_REDIRECT` | Send the user to your own thank-you page after they opt out. | `unsubRedir` |
| External (your own) | `Minigun::UNSUB_EXTERNAL` | You host the entire unsub flow on your own domain. | `unsubUrl` |

### Errors

```php
try {
    $mg->sendBulk(/* ... */);
} catch (MinigunApiException $e) {
    // 4xx/5xx from the server.
    $e->status; // int, e.g. 400, 404, 500
    $e->body;   // mixed: usually array with an 'error' string
} catch (MinigunTransportException $e) {
    // curl-level failure: DNS, TLS, timeout, connection refused.
} catch (MinigunException $e) {
    // Common base. Catch this if you don't need to branch.
} catch (\InvalidArgumentException $e) {
    // Local validation — you passed mutually-exclusive args, unknown
    // unsub mode, or a missing/unreadable file.
}
```

The split matters for retry policy: transport errors are often worth retrying with backoff; API errors usually aren't.

## See also

- [Top-level README](../../README.md) — server install, deployment, full HTTP API reference.
- [Cross-SDK overview](../README.md) — method-name table across all four languages.
- [Auto list hygiene](../../README.md#automatic-list-hygiene) — the Mailgun webhook is server-side and needs no SDK code; the `deleteContact()` method here is the manual / scripted equivalent.
