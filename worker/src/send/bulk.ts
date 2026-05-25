import { Env, publicURL } from '../env';
import { sendMessageWithRetry } from '../lib/mailgun';
import {
  createBatch,
  markBatchStatus,
  nextBatchIndex,
} from '../store/batches';
import {
  advanceSendCursor,
  getSend,
  hasInFlightBatch,
  updateSendStatus,
} from '../store/sends';
import { nextRecipientBatch } from '../store/subscriptions';
import { buildRecipientVars } from './render';

export type StepResult =
  | { state: 'busy' }
  | { state: 'completed' }
  | { state: 'sent'; batch_id: string; recipients: number; last_subscription_id: number };

export async function step(env: Env, sendID: string): Promise<StepResult> {
  if (await hasInFlightBatch(env.DB, sendID)) return { state: 'busy' };

  const snd = await getSend(env.DB, sendID);
  if (snd.status !== 'running') {
    await updateSendStatus(env.DB, sendID, 'running', null);
  }
  if (!snd.list_id || snd.max_subscription_id == null) {
    await updateSendStatus(env.DB, sendID, 'failed', 'bulk send missing list_id or max_subscription_id');
    return { state: 'completed' };
  }

  const recipients = await nextRecipientBatch(
    env.DB,
    snd.list_id,
    snd.last_subscription_id,
    snd.max_subscription_id,
    snd.batch_size,
  );
  if (recipients.length === 0) {
    await updateSendStatus(env.DB, sendID, 'completed', null);
    return { state: 'completed' };
  }

  const batchIndex = await nextBatchIndex(env.DB, sendID);
  const startID = recipients[0]!.subscription_id;
  const endID = recipients[recipients.length - 1]!.subscription_id;
  const batch = await createBatch(env.DB, sendID, batchIndex, startID, endID, recipients.length);

  const recipVars: Record<string, Record<string, unknown>> = {};
  const subIDs: string[] = [];
  const emails: string[] = [];
  for (const r of recipients) {
    emails.push(r.email);
    subIDs.push(String(r.subscription_id));
    recipVars[r.email] = await buildRecipientVars(env, snd, r);
  }

  try {
    const resp = await sendMessageWithRetry(env, {
      domain: snd.sending_domain,
      from: snd.from_header,
      to: emails,
      subject: snd.subject,
      html: snd.body_html ?? '',
      text: snd.body_text ?? '',
      replyTo: snd.reply_to ?? undefined,
      tag: snd.id,
      trackingOpens: true,
      trackingClicks: true,
      trackingUnsubscribeOn: false,
      listUnsubscribe: '<%recipient.unsub_url%>',
      listUnsubscribePost: 'List-Unsubscribe=One-Click',
      recipientVariables: recipVars,
      customVars: {
        minigun_send_id: snd.id,
        minigun_subscription_ids: subIDs.join(','),
        minigun_batch_id: batch.id,
      },
    });
    await markBatchStatus(env.DB, batch.id, 'succeeded', JSON.stringify(resp));
    await advanceSendCursor(env.DB, sendID, endID);
    return {
      state: 'sent',
      batch_id: batch.id,
      recipients: recipients.length,
      last_subscription_id: endID,
    };
  } catch (err) {
    const msg = (err as Error).message;
    await markBatchStatus(env.DB, batch.id, 'failed', msg);
    await updateSendStatus(env.DB, sendID, 'failed', msg);
    throw err;
  }
}

export function scheduleNextStep(
  env: Env,
  ctx: ExecutionContext,
  sendID: string,
  throttleMs: number,
): void {
  const url = `${publicURL(env)}/send/${sendID}/next`;
  ctx.waitUntil(
    (async () => {
      if (throttleMs > 0) await new Promise((r) => setTimeout(r, throttleMs));
      try {
        await fetch(url, {
          method: 'POST',
          headers: { 'x-internal-secret': env.MINIGUN_INTERNAL_SECRET },
        });
      } catch (err) {
        console.error('self-call failed', sendID, err);
      }
    })(),
  );
}
