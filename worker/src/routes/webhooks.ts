import { Hono } from 'hono';
import { Env } from '../env';
import { verifyMailgunSignature } from '../lib/mailgun_webhook';
import { recordComplaintEvent } from '../store/complaints';
import { deleteContact } from '../store/contacts';
import { NotFoundError } from '../store/types';

// Mailgun's "events" webhook payload (the format used since their API
// v3 redesign). We only read the fields we dispatch on; the rest stays
// in the raw payload column for forensic value.
type MailgunWebhook = {
  signature?: { timestamp?: string; token?: string; signature?: string };
  'event-data'?: {
    id?: string;
    event?: string;
    severity?: string;
    timestamp?: number;
    recipient?: string;
    // ...lots more we don't care about
    [k: string]: unknown;
  };
};

export function mountWebhooks(app: Hono<{ Bindings: Env }>) {
  app.post('/webhooks/mailgun', async (c) => {
    // Read the raw body so we can both verify the signature and stash
    // the original JSON in the complaint audit log untouched.
    const raw = await c.req.text();
    let body: MailgunWebhook;
    try {
      body = JSON.parse(raw) as MailgunWebhook;
    } catch {
      return c.json({ error: 'invalid JSON' }, 400);
    }

    const sigKey = c.env.MAILGUN_WEBHOOK_SIGNING_KEY ?? '';
    const verdict = await verifyMailgunSignature(sigKey, {
      timestamp: body.signature?.timestamp ?? '',
      token: body.signature?.token ?? '',
      signature: body.signature?.signature ?? '',
    });
    if (!verdict.ok) {
      // Logged but not exposed in the response — never tell an
      // attacker which of "stale", "no key", or "mismatch" tripped.
      console.warn('mailgun webhook rejected', verdict.reason);
      return c.json({ error: 'unauthorized' }, 401);
    }

    const ev = body['event-data'] ?? {};
    const event = typeof ev.event === 'string' ? ev.event : '';
    const severity = typeof ev.severity === 'string' ? ev.severity : '';
    const recipient = typeof ev.recipient === 'string' ? ev.recipient.trim().toLowerCase() : '';
    const mgEventID = typeof ev.id === 'string' ? ev.id : null;
    const mgTimestamp =
      typeof ev.timestamp === 'number' ? String(ev.timestamp) : null;

    // Dispatch matrix:
    //   failed + permanent → hard bounce → DeleteContact
    //   failed + temporary → soft bounce → ignore, Mailgun will retry
    //   complained         → spam complaint → log + DeleteContact
    //   anything else      → 200 OK, no-op
    if (event === 'failed' && severity === 'permanent') {
      if (!recipient) return c.json({ ok: true, action: 'noop-no-recipient' });
      try {
        const result = await deleteContact(c.env.DB, recipient);
        return c.json({
          ok: true,
          action: 'deleted',
          contact_id: result.contact.id,
          subscriptions_removed: result.subscriptions_removed,
        });
      } catch (err) {
        if (err instanceof NotFoundError) {
          return c.json({ ok: true, action: 'already-gone' });
        }
        throw err;
      }
    }

    if (event === 'complained') {
      if (!recipient) return c.json({ ok: true, action: 'noop-no-recipient' });
      let contactID: string | null = null;
      try {
        // Resolve to capture the contact_id in the audit row before delete.
        const result = await deleteContact(c.env.DB, recipient);
        contactID = result.contact.id;
      } catch (err) {
        if (!(err instanceof NotFoundError)) throw err;
        // Already deleted (prior bounce, or webhook retry that already
        // ran). Still record the complaint for the audit trail.
      }
      await recordComplaintEvent(c.env.DB, {
        email: recipient,
        contactID,
        mailgunEventID: mgEventID,
        mailgunTimestamp: mgTimestamp,
        payload: ev,
      });
      return c.json({ ok: true, action: 'complained', contact_id: contactID });
    }

    return c.json({ ok: true, action: 'noop', event });
  });
}
