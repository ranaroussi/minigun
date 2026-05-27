import { newComplaint } from '../lib/ids';
import { nowISO } from './types';

export type RecordComplaintInput = {
  email: string;
  contactID?: string | null;
  mailgunEventID?: string | null;
  mailgunTimestamp?: string | null;
  payload?: unknown;
};

// Records a spam-complaint event from Mailgun's webhook so that we
// retain forensic evidence after the contact itself has been deleted.
// `mailgun_event_id` is UNIQUE — D1 `INSERT OR IGNORE` makes webhook
// retries idempotent at the storage layer even when our own dispatch
// loop racing somehow tries to insert twice.
export async function recordComplaintEvent(
  db: D1Database,
  input: RecordComplaintInput,
): Promise<void> {
  const id = newComplaint();
  const payloadStr =
    input.payload === undefined || input.payload === null
      ? null
      : typeof input.payload === 'string'
        ? input.payload
        : JSON.stringify(input.payload);
  await db
    .prepare(
      `INSERT OR IGNORE INTO complaint_events
         (id, email, contact_id, mailgun_event_id, mailgun_timestamp, payload, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(
      id,
      input.email.trim().toLowerCase(),
      input.contactID ?? null,
      input.mailgunEventID ?? null,
      input.mailgunTimestamp ?? null,
      payloadStr,
      nowISO(),
    )
    .run();
}
