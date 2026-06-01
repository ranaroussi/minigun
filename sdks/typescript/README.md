# MiniGun — TypeScript SDK

Single-file TypeScript client for [MiniGun](https://github.com/ranaroussi/minigun). Built on the standard `fetch` API with zero runtime dependencies, so the same file runs unchanged on:

- Node 18+ (native `fetch`)
- Bun
- Deno
- Cloudflare Workers
- Modern browsers
- Edge runtimes (Vercel, Netlify, etc.)

**Requires:** an ES2020+ target with `fetch`, `AbortController`, and `URLSearchParams`. For Node < 18, see [Injecting a custom fetch](#injecting-a-custom-fetch).

## Install

Drop the file in:

```bash
curl -O https://raw.githubusercontent.com/ranaroussi/minigun/main/sdks/typescript/minigun.ts
```

Then:

```typescript
import { Minigun, MinigunApiError, MinigunTransportError } from './minigun';
```

No `npm install`. If you'd prefer a published package, vendoring the file into your repo is the supported path — it's a single ~280-line file with no build step required (your existing `tsc` / `tsx` / `vite` pipeline handles it).

## Quickstart

```typescript
import { Minigun, MinigunApiError, MinigunTransportError } from './minigun';

const mg = new Minigun(
  process.env.MINIGUN_API_URL!,
  process.env.MINIGUN_API_TOKEN!,
);

try {
  // Upsert a contact and subscribe them.
  await mg.addContact('newsletter', 'alice@example.com', { first_name: 'Alice' });

  // Send a bulk campaign.
  const body = await fs.promises.readFile('./week-12.md', 'utf8');
  const res = (await mg.sendBulk({
    list: 'newsletter',
    subject: 'Big news this week',
    from: 'Ran <ran@example.com>',
    md: body,
  })) as { send_id: string; total_recipients: number };

  console.log(`queued send ${res.send_id} — ${res.total_recipients} recipients`);
} catch (e) {
  if (e instanceof MinigunApiError) {
    console.error(`API ${e.status}:`, e.message);
    process.exit(1);
  } else if (e instanceof MinigunTransportError) {
    console.error('Network error:', e.message);
    process.exit(2);
  }
  throw e;
}
```

## Reference

### Construction

```typescript
new Minigun(
  baseUrl: string,
  token = '',
  opts: MinigunOptions = {},
);

interface MinigunOptions {
  timeoutMs?: number;        // default: 120_000
  userAgent?: string;        // default: 'minigun-typescript/0.1'
  fetch?: typeof fetch;      // default: globalThis.fetch
}
```

- `baseUrl` — API origin (e.g. `https://mailer.example.com`). Trailing slash optional.
- `token` — Bearer token. Required when the server has `MINIGUN_API_TOKEN` set.
- `timeoutMs` — Overall request timeout in milliseconds (implemented via `AbortController`).

### Contacts

```typescript
mg.addContact(list: string, email: string, params?: Record<string, unknown> | null): Promise<unknown>
mg.unsubscribeContact(list: string, email: string): Promise<unknown>
mg.deleteContact(idOrEmail: string): Promise<unknown>
mg.listContacts(list: string, cursor?: string, limit?: number): Promise<unknown>
```

- **`addContact()`** — Upsert. Safe to call repeatedly: existing `params` are merged and any prior unsubscribe is cleared.
- **`unsubscribeContact()`** — Admin-side opt-out. Preserves the row with `subscribed=0` so future re-imports don't silently re-subscribe. Use this for user-initiated unsubscribes.
- **`deleteContact()`** — Hard purge: removes the contact + every subscription + every audit row. Use this for hard-bounce cleanup. The Mailgun webhook (`/webhooks/mailgun`) does this automatically; this method is for scripted/one-off purges. Accepts either `c_XXXXXXXXXX` ids or email addresses.
- **`listContacts()`** — Paginated. Returns `{contacts: [...], next_cursor: string | null}` (after casting). Default `limit` is 50.

### Sends

```typescript
mg.sendSingle(args: SendSingleArgs): Promise<unknown>
mg.sendBulk(args: SendBulkArgs): Promise<unknown>
mg.getSend(sendId: string): Promise<unknown>
mg.getSendStats(sendId: string): Promise<unknown>
mg.resumeSend(sendId: string, force?: boolean): Promise<unknown>
```

The send methods take an args bag rather than 18 positional params — TypeScript has no keyword arguments, and a bag interface gives you the same readability:

```typescript
await mg.sendBulk({
  list: 'newsletter',
  subject: 'Weekly update',
  from: 'Ran <ran@example.com>',
  md: '# This week...',
  testMode: true,
});
```

The full `SendSingleArgs` / `SendBulkArgs` shapes are exported from the file. For each body field there's a value variant (`md`, `html`, `text`, `template`); pass at most one of `md` / `html` (the others are optional supplements). Unlike the PHP / Python / Go SDKs there are no `*File` companions — Node's `fs.promises.readFile()` is one line, and browsers / Workers don't have a filesystem at all, so this stayed JS-only on purpose.

`subject` and `from` are optional in the interface because they can be supplied via the Markdown frontmatter (a leading `---`/`-----` fenced block with `subject:` / `from:` / `preheader:` / `reply_to:`). An explicit value wins; the block is stripped from `md` before sending. If neither the field nor frontmatter supplies `subject` and `from`, the method throws.

Unsubscribe-mode constants are exported from the module:

```typescript
import { UNSUB_LOCAL, UNSUB_REDIRECT, UNSUB_EXTERNAL } from './minigun';
```

| Mode | Constant | When to use | Required extra arg |
|---|---|---|---|
| Local unsubscribe page | `UNSUB_LOCAL` (default) | Standard. Renders the MiniGun unsub / preferences page. | — |
| Redirect after unsub | `UNSUB_REDIRECT` | Send the user to your own thank-you page after they opt out. | `unsubRedir` |
| External (your own) | `UNSUB_EXTERNAL` | You host the entire unsub flow on your own domain. | `unsubUrl` |

### Errors

```typescript
try {
  await mg.sendBulk({ /* ... */ });
} catch (e) {
  if (e instanceof MinigunApiError) {
    // 4xx/5xx from the server.
    e.status;  // number
    e.body;    // unknown — usually { error: string }
  } else if (e instanceof MinigunTransportError) {
    // fetch threw: DNS, TLS, timeout (abort), refused connection.
    e.cause;   // the original thrown value, if any
  } else if (e instanceof MinigunError) {
    // Common base. Catch this if you don't need to branch.
  } else {
    // Local validation (plain Error) — e.g. both `md` and `html` set,
    // or an unknown unsub mode.
    throw e;
  }
}
```

The split matters for retry policy: transport errors are often worth retrying with backoff; API errors usually aren't.

## Injecting a custom fetch

The SDK uses `globalThis.fetch` by default. On Node < 18 (or any runtime without a global `fetch`), pass one explicitly:

```typescript
import { Minigun } from './minigun';
import nodeFetch from 'node-fetch'; // or 'undici', etc.

const mg = new Minigun(url, token, {
  fetch: nodeFetch as unknown as typeof fetch,
});
```

The same hook is useful in tests for mocking out the network entirely without monkey-patching globals.

## Cloudflare Workers

Just import and use — `fetch` and `AbortController` are both built-in. No polyfills:

```typescript
import { Minigun } from './minigun';

export default {
  async fetch(_req: Request, env: { MG_TOKEN: string }) {
    const mg = new Minigun('https://mailer.example.com', env.MG_TOKEN);
    await mg.sendSingle({
      to: 'alice@example.com',
      from: 'Ran <ran@example.com>',
      subject: 'Welcome',
      company: 'acme',
      md: '# Hi!',
    });
    return new Response('ok');
  },
};
```

## See also

- [Top-level README](../../README.md) — server install, deployment, full HTTP API reference.
- [Cross-SDK overview](../README.md) — method-name table across all four languages.
- [Auto list hygiene](../../README.md#automatic-list-hygiene) — the Mailgun webhook is server-side and needs no SDK code; the `deleteContact()` method here is the manual / scripted equivalent.
